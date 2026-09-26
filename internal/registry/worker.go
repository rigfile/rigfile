package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/analyze"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/publish"
	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/similar"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

// A scan job is claimed with FOR UPDATE SKIP LOCKED, so several workers (or instances) never take the same one; a
// worker that dies leaves the job "running" until locked_until, after which another worker picks it up again.

// ClaimJob takes the next ready job, or returns ErrNotFound when there is none.
func (s *Store) ClaimJob(ctx context.Context, worker string, lockFor time.Duration) (versionID int64, attempts int, err error) {
	err = s.DB.QueryRowContext(ctx, `
		UPDATE jobs SET state = 'running', locked_by = $1, locked_until = $2, attempts = attempts + 1
		WHERE id = (
			SELECT id FROM jobs
			WHERE (state = 'queued' AND run_after <= $3) OR (state = 'running' AND locked_until < $3)
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING version_id, attempts`, worker, s.now().Add(lockFor), s.now()).Scan(&versionID, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return versionID, attempts, err
}

// ScanResult is everything a scan concludes about one version.
type ScanResult struct {
	Status     string // published | rejected | held
	Findings   []Finding
	Warnings   []Finding
	Analysis   []AnalysisFinding
	Similar    []SimilarRig
	HeldReason string
}

// FinishScan records the result: the version becomes published, held or rejected, and the job is done.
func (s *Store) FinishScan(ctx context.Context, versionID int64, r ScanResult) error {
	fb, _ := json.Marshal(nz2(r.Findings))
	wb, _ := json.Marshal(nz2(r.Warnings))
	ab, _ := json.Marshal(nzA(r.Analysis))
	sb, _ := json.Marshal(nzS(r.Similar))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// only a pending version can be scanned into a final state (an admin may have removed it meanwhile)
	res, err := tx.ExecContext(ctx, `UPDATE versions SET status = $2, scan_findings = $3, scan_warnings = $4, scan_analysis = $6, similar_to = $7, held_reason = $8, scanned_at = $5 WHERE id = $1 AND status = 'pending'`,
		versionID, r.Status, string(fb), string(wb), s.now(), string(ab), string(sb), r.HeldReason)
	if err != nil {
		return err
	}
	_ = res
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'done', locked_by = NULL, locked_until = NULL WHERE version_id = $1`, versionID); err != nil {
		return err
	}
	return tx.Commit()
}

func nzA(f []AnalysisFinding) []AnalysisFinding {
	if f == nil {
		return []AnalysisFinding{}
	}
	return f
}

func nzS(f []SimilarRig) []SimilarRig {
	if f == nil {
		return []SimilarRig{}
	}
	return f
}

func nz2(f []Finding) []Finding {
	if f == nil {
		return []Finding{}
	}
	return f
}

// RetryOrFail puts a failed job back (with a delay) or, after three attempts, rejects the version with an internal finding.
func (s *Store) RetryOrFail(ctx context.Context, versionID int64, attempts int, cause error) error {
	if attempts < 3 {
		_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state = 'queued', locked_by = NULL, locked_until = NULL, run_after = $2, last_error = $3 WHERE version_id = $1`,
			versionID, s.now().Add(time.Duration(attempts)*30*time.Second), trunc(cause.Error(), 300))
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state = 'failed', last_error = $2 WHERE version_id = $1`, versionID, trunc(cause.Error(), 300)); err != nil {
		return err
	}
	return s.FinishScan(ctx, versionID, ScanResult{Status: "rejected", Findings: []Finding{{Kind: "internal", Message: "the scan could not be completed; publish again"}}})
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Scanner runs the scan for one stored version.
type Scanner struct {
	Store  *Store
	Blobs  blob.Store
	Scan   func() (*scan.Scanner, error)
	Log    *slog.Logger
	Limits source.Limits
}

// ScanVersion unpacks the stored tarball into a temporary directory and applies the publishing rules:
//   - any secret finding rejects the version (a secret is never acceptable, public or private);
//   - a manifest error rejects it;
//   - unpinned packages reject a public rig and only warn on a private one;
//   - static analysis results are stored with the version; a danger-level result on a PUBLIC rig holds the version for an
//     administrator's review (docs/trust.md §7);
//   - names confusably close to other rigs are recorded (docs/trust.md §4).
func (sc *Scanner) ScanVersion(ctx context.Context, versionID int64) (ScanResult, error) {
	var sha, owner, name string
	var public bool
	err := sc.Store.DB.QueryRowContext(ctx, `SELECT v.tarball_sha256, r.owner, r.name, r.visibility = 'public' FROM versions v JOIN rigs r ON r.id = v.rig_id WHERE v.id = $1`, versionID).Scan(&sha, &owner, &name, &public)
	if err != nil {
		return ScanResult{}, err
	}
	rc, _, err := sc.Blobs.Get(ctx, sha)
	if err != nil {
		return ScanResult{}, fmt.Errorf("blob: %w", err)
	}
	defer rc.Close()
	dir, err := os.MkdirTemp("", "rigfile-scan-")
	if err != nil {
		return ScanResult{}, err
	}
	defer os.RemoveAll(dir)
	reject := func(f Finding) (ScanResult, error) {
		return ScanResult{Status: "rejected", Findings: []Finding{f}}, nil
	}
	if err := source.Extract(io.LimitReader(rc, 64<<20), dir+"/rig", false, sc.Limits); err != nil {
		return reject(Finding{Kind: "manifest", Message: "the archive could not be unpacked safely: " + err.Error()})
	}
	scanner, err := sc.Scan()
	if err != nil {
		return ScanResult{}, err
	}
	a, err := publish.AuditDir(dir+"/rig", scanner)
	if err != nil {
		return reject(Finding{Kind: "manifest", Message: "the rig does not validate: " + err.Error()})
	}
	var res ScanResult
	for _, f := range a.Secrets {
		res.Findings = append(res.Findings, Finding{Kind: "secret", Rule: f.Rule, File: f.File, Line: f.Line, Message: f.Sample})
	}
	for _, p := range a.Problems {
		if p.Level == manifest.Error {
			res.Findings = append(res.Findings, Finding{Kind: "manifest", File: "rigfile.yaml", Message: p.Where + ": " + p.Msg})
		}
	}
	for _, p := range a.Unpinned {
		f := Finding{Kind: "unpinned", File: "rigfile.yaml", Message: p.Where + ": " + p.Msg}
		if public {
			res.Findings = append(res.Findings, f)
		} else {
			res.Warnings = append(res.Warnings, f)
		}
	}
	// static analysis and similar names: information for readers, and (danger on a public rig) a reason to hold
	if rep, err := analyze.Dir(dir + "/rig"); err == nil {
		for _, f := range rep.Findings {
			res.Analysis = append(res.Analysis, AnalysisFinding{Level: string(f.Level), Rule: f.Rule, File: f.File, Line: f.Line, Count: f.Count, Message: f.Message})
		}
	}
	if cands, err := sc.Store.SimilarCandidates(ctx); err == nil {
		for _, m := range similar.Find(owner, name, cands) {
			res.Similar = append(res.Similar, SimilarRig{Ref: m.Ref, Kind: m.Kind, Stars: m.Stars, Verified: m.Verified})
		}
	}
	switch {
	case len(res.Findings) > 0:
		res.Status = "rejected"
	case public && hasDanger(res.Analysis):
		res.Status, res.HeldReason = "held", "static analysis found danger-level patterns: "+dangerRules(res.Analysis)
	default:
		res.Status = "published"
	}
	return res, nil
}

func hasDanger(a []AnalysisFinding) bool {
	for _, f := range a {
		if f.Level == string(analyze.Danger) {
			return true
		}
	}
	return false
}

func dangerRules(a []AnalysisFinding) string {
	seen := map[string]bool{}
	var out []string
	for _, f := range a {
		if f.Level == string(analyze.Danger) && !seen[f.Rule] {
			seen[f.Rule] = true
			out = append(out, f.Rule)
		}
	}
	return strings.Join(out, ", ")
}

// RunOnce processes at most one job; it reports whether it did any work.
func (sc *Scanner) RunOnce(ctx context.Context, worker string) (bool, error) {
	id, attempts, err := sc.Store.ClaimJob(ctx, worker, 5*time.Minute)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := sc.ScanVersion(sctx, id)
	if err != nil {
		sc.logf("scan failed", "version", id, "attempt", attempts, "err", err)
		return true, sc.Store.RetryOrFail(ctx, id, attempts, err)
	}
	sc.logf("scanned", "version", id, "status", res.Status, "findings", len(res.Findings), "warnings", len(res.Warnings), "analysis", len(res.Analysis))
	return true, sc.Store.FinishScan(ctx, id, res)
}

func (sc *Scanner) logf(msg string, args ...any) {
	if sc.Log != nil {
		sc.Log.Info(msg, args...)
	}
}

// Run polls for jobs until ctx ends, with `workers` concurrent loops.
func (sc *Scanner) Run(ctx context.Context, workers int) {
	if workers < 1 {
		workers = 1
	}
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			name := fmt.Sprintf("worker-%d-%d", os.Getpid(), n)
			for ctx.Err() == nil {
				did, err := sc.RunOnce(ctx, name)
				if err != nil && ctx.Err() == nil {
					sc.logf("worker error", "err", err.Error())
				}
				if !did || err != nil {
					select {
					case <-ctx.Done():
					case <-time.After(2 * time.Second):
					}
				}
			}
		}(i)
	}
	for i := 0; i < workers; i++ {
		<-done
	}
}
