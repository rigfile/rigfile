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

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/publish"
	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
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

// FinishScan records the result: the version becomes published or rejected, and the job is done.
func (s *Store) FinishScan(ctx context.Context, versionID int64, status string, findings, warnings []Finding) error {
	fb, _ := json.Marshal(nz2(findings))
	wb, _ := json.Marshal(nz2(warnings))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// only a pending version can be scanned into a final state (an admin may have removed it meanwhile)
	res, err := tx.ExecContext(ctx, `UPDATE versions SET status = $2, scan_findings = $3, scan_warnings = $4, scanned_at = $5 WHERE id = $1 AND status = 'pending'`,
		versionID, status, string(fb), string(wb), s.now())
	if err != nil {
		return err
	}
	_ = res
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'done', locked_by = NULL, locked_until = NULL WHERE version_id = $1`, versionID); err != nil {
		return err
	}
	return tx.Commit()
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
	return s.FinishScan(ctx, versionID, "rejected", []Finding{{Kind: "internal", Message: "the scan could not be completed; publish again"}}, nil)
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
//   - unpinned packages reject a public rig and only warn on a private one.
func (sc *Scanner) ScanVersion(ctx context.Context, versionID int64) (status string, findings, warnings []Finding, err error) {
	var sha string
	var public bool
	err = sc.Store.DB.QueryRowContext(ctx, `SELECT v.tarball_sha256, r.visibility = 'public' FROM versions v JOIN rigs r ON r.id = v.rig_id WHERE v.id = $1`, versionID).Scan(&sha, &public)
	if err != nil {
		return "", nil, nil, err
	}
	rc, _, err := sc.Blobs.Get(ctx, sha)
	if err != nil {
		return "", nil, nil, fmt.Errorf("blob: %w", err)
	}
	defer rc.Close()
	dir, err := os.MkdirTemp("", "rigfile-scan-")
	if err != nil {
		return "", nil, nil, err
	}
	defer os.RemoveAll(dir)
	if err := source.Extract(io.LimitReader(rc, 64<<20), dir+"/rig", false, sc.Limits); err != nil {
		return "rejected", []Finding{{Kind: "manifest", Message: "the archive could not be unpacked safely: " + err.Error()}}, nil, nil
	}
	scanner, err := sc.Scan()
	if err != nil {
		return "", nil, nil, err
	}
	a, err := publish.AuditDir(dir+"/rig", scanner)
	if err != nil {
		return "rejected", []Finding{{Kind: "manifest", Message: "the rig does not validate: " + err.Error()}}, nil, nil
	}
	for _, f := range a.Secrets {
		findings = append(findings, Finding{Kind: "secret", Rule: f.Rule, File: f.File, Line: f.Line, Message: f.Sample})
	}
	for _, p := range a.Problems {
		if p.Level == manifest.Error {
			findings = append(findings, Finding{Kind: "manifest", File: "rigfile.yaml", Message: p.Where + ": " + p.Msg})
		}
	}
	for _, p := range a.Unpinned {
		f := Finding{Kind: "unpinned", File: "rigfile.yaml", Message: p.Where + ": " + p.Msg}
		if public {
			findings = append(findings, f)
		} else {
			warnings = append(warnings, f)
		}
	}
	if len(findings) > 0 {
		return "rejected", findings, warnings, nil
	}
	return "published", nil, warnings, nil
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
	status, findings, warnings, err := sc.ScanVersion(sctx, id)
	if err != nil {
		sc.logf("scan failed", "version", id, "attempt", attempts, "err", err)
		return true, sc.Store.RetryOrFail(ctx, id, attempts, err)
	}
	sc.logf("scanned", "version", id, "status", status, "findings", len(findings), "warnings", len(warnings))
	return true, sc.Store.FinishScan(ctx, id, status, findings, warnings)
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
