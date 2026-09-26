package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"

	"github.com/digitaldreamer3462/rigfile/internal/layers"
)

// Viewer is who is asking. The zero value is an anonymous visitor.
type Viewer struct {
	ID    int64
	Admin bool
}

// ViewerOf builds a Viewer from a user (nil = anonymous).
func ViewerOf(u *User) Viewer {
	if u == nil {
		return Viewer{}
	}
	return Viewer{ID: u.ID, Admin: u.IsAdmin}
}

// Finding is one result of a scan. It never contains a value, only where and which rule.
type Finding struct {
	Kind    string `json:"kind"` // secret | manifest | unpinned | internal
	Rule    string `json:"rule,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message,omitempty"`
}

// Rig is a registry entry.
type Rig struct {
	ID          int64
	Owner, Name string
	Description string
	Visibility  string
	CreatedBy   int64
	CreatedAt   time.Time
	Stars       int
	Starred     bool // by the viewer
}

// Version is one immutable release of a rig.
type Version struct {
	ID, RigID     int64
	Version       string
	Status        string
	TarballSHA256 string
	Size          int64
	ManifestYAML  string
	Description   string
	Readme        string
	Targets       []string
	NeedsSecrets  []string
	NeedsLogins   []string
	Layers        []string
	Findings      []Finding
	Warnings      []Finding
	CreatedAt     time.Time
	ScannedAt     *time.Time
	YankedAt      *time.Time
	YankReason    string
	Analysis      []AnalysisFinding // static analysis results (docs/trust.md §2)
	SimilarTo     []SimilarRig
	HeldReason    string
	SignerIssuer  string
	SignerSubject string
	// SignerIsPublisher is true when the signature verified AND its certificate identity belongs to the publisher.
	SignerIsPublisher bool
	SignatureError    string
}

// AnalysisFinding is one static analysis result stored with a version (no file content, only where and which rule).
type AnalysisFinding struct {
	Level   string `json:"level"`
	Rule    string `json:"rule"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Count   int    `json:"count"`
	Message string `json:"message"`
}

// SimilarRig is a rig whose name could be mistaken for this one.
type SimilarRig struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Stars    int    `json:"stars"`
	Verified bool   `json:"verified"`
}

// FileEntry is one line of a version's file index.
type FileEntry struct {
	Path   string
	Size   int64
	SHA256 string
	IsText bool
}

// ErrForbidden means the viewer may see the thing but not do this to it.
var ErrForbidden = errors.New("forbidden")

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// The visibility predicate. Every read of rigs and versions goes through these two fragments (docs/registry.md §2):
//
//	rig:     not removed, and (public, or the viewer owns it, or the viewer is an admin)
//	version: published or yanked; the owner (and admins) also see pending and rejected; admins also see removed
//
// $1 is the viewer id (0 = anonymous) and $2 whether the viewer is an admin.
const (
	rigVisibleAdm  = `(($2::boolean) OR r.removed_at IS NULL AND (r.visibility = 'public' OR r.created_by = $1))`
	versionVisible = `(v.status IN ('published','yanked') OR (v.status IN ('pending','rejected','held') AND (r.created_by = $1 OR $2::boolean)) OR (v.status = 'removed' AND $2::boolean))`
)

const rigCols = `r.id, r.owner, r.name, r.description, r.visibility, r.created_by, r.created_at,
	(SELECT count(*) FROM stars s WHERE s.rig_id = r.id),
	EXISTS (SELECT 1 FROM stars s WHERE s.rig_id = r.id AND s.user_id = $1)`

func scanRig(row interface{ Scan(...any) error }) (*Rig, error) {
	var r Rig
	if err := row.Scan(&r.ID, &r.Owner, &r.Name, &r.Description, &r.Visibility, &r.CreatedBy, &r.CreatedAt, &r.Stars, &r.Starred); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetRig returns the rig if the viewer may see it. A private or missing rig is the same ErrNotFound.
func (s *Store) GetRig(ctx context.Context, owner, name string, v Viewer) (*Rig, error) {
	r, err := scanRig(s.DB.QueryRowContext(ctx, `SELECT `+rigCols+` FROM rigs r WHERE r.owner = $3 AND r.name = $4 AND `+rigVisibleAdm, v.ID, v.Admin, owner, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

const versionCols = `v.id, v.rig_id, v.version, v.status, v.tarball_sha256, v.size, v.manifest_yaml, v.description, v.readme,
	v.targets, v.needs_secrets, v.needs_logins, v.layers, v.scan_findings, v.scan_warnings, v.created_at, v.scanned_at, v.yanked_at, v.yank_reason,
	v.scan_analysis, v.similar_to, v.held_reason, v.signer_issuer, v.signer_subject, v.signer_is_publisher, v.signature_error`

func scanVersion(row interface{ Scan(...any) error }) (*Version, error) {
	var v Version
	var findings, warnings, analysis, similar []byte
	var scanned, yanked sql.NullTime
	if err := row.Scan(&v.ID, &v.RigID, &v.Version, &v.Status, &v.TarballSHA256, &v.Size, &v.ManifestYAML, &v.Description, &v.Readme,
		pq.Array(&v.Targets), pq.Array(&v.NeedsSecrets), pq.Array(&v.NeedsLogins), pq.Array(&v.Layers), &findings, &warnings, &v.CreatedAt, &scanned, &yanked, &v.YankReason,
		&analysis, &similar, &v.HeldReason, &v.SignerIssuer, &v.SignerSubject, &v.SignerIsPublisher, &v.SignatureError); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(findings, &v.Findings)
	_ = json.Unmarshal(warnings, &v.Warnings)
	_ = json.Unmarshal(analysis, &v.Analysis)
	_ = json.Unmarshal(similar, &v.SimilarTo)
	if scanned.Valid {
		v.ScannedAt = &scanned.Time
	}
	if yanked.Valid {
		v.YankedAt = &yanked.Time
	}
	return &v, nil
}

// ListVersions returns the versions of a rig the viewer may see, newest first.
func (s *Store) ListVersions(ctx context.Context, rigID int64, viewer Viewer) ([]Version, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+versionCols+` FROM versions v JOIN rigs r ON r.id = v.rig_id WHERE v.rig_id = $3 AND `+rigVisibleAdm+` AND `+versionVisible, viewer.ID, viewer.Admin, rigID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return CompareVersions(out[i].Version, out[j].Version) > 0 })
	return out, rows.Err()
}

// GetVersion returns one version if the viewer may see the rig and the version.
func (s *Store) GetVersion(ctx context.Context, owner, name, ver string, viewer Viewer) (*Rig, *Version, error) {
	rig, err := s.GetRig(ctx, owner, name, viewer)
	if err != nil {
		return nil, nil, err
	}
	v, err := scanVersion(s.DB.QueryRowContext(ctx, `SELECT `+versionCols+` FROM versions v JOIN rigs r ON r.id = v.rig_id WHERE v.rig_id = $3 AND v.version = $4 AND `+versionVisible, viewer.ID, viewer.Admin, rig.ID, ver))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	return rig, v, err
}

// Resolve picks the newest published (not yanked) version satisfying rng (layers.Satisfies rules; "" = any).
func (s *Store) Resolve(ctx context.Context, owner, name, rng string, viewer Viewer) (*Rig, *Version, error) {
	rig, err := s.GetRig(ctx, owner, name, viewer)
	if err != nil {
		return nil, nil, err
	}
	vs, err := s.ListVersions(ctx, rig.ID, viewer)
	if err != nil {
		return nil, nil, err
	}
	for i := range vs {
		if vs[i].Status == "published" && layers.Satisfies(vs[i].Version, rng) {
			return rig, &vs[i], nil
		}
	}
	return nil, nil, ErrNotFound
}

// NewVersion is what an upload has established by the time it is stored.
type NewVersion struct {
	Owner, Name  string
	Version      string
	UserID       int64
	Admin        bool
	TarballSHA   string
	Size         int64
	ManifestYAML string
	Description  string
	Readme       string
	Targets      []string
	NeedsSecrets []string
	NeedsLogins  []string
	Layers       []string
	Files        []FileEntry
	// a verified Sigstore signature, if the upload carried one
	Bundle            string
	SignerIssuer      string
	SignerSubject     string
	SignerIsPublisher bool
}

// CreateVersion creates the rig on first upload (private), inserts the version as pending and queues its scan, in one
// transaction. A duplicate owner/name@version is ErrConflict (the unique constraint, so it holds under concurrency);
// uploading to a rig someone else owns is ErrForbidden.
func (s *Store) CreateVersion(ctx context.Context, n NewVersion) (*Version, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var rigID, createdBy int64
	var removed sql.NullTime
	err = tx.QueryRowContext(ctx, `
		INSERT INTO rigs (owner, name, description, created_by, created_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (owner, name) DO UPDATE SET description = rigs.description
		RETURNING id, created_by, removed_at`, n.Owner, n.Name, n.Description, n.UserID, s.now()).Scan(&rigID, &createdBy, &removed)
	if err != nil {
		return nil, err
	}
	if createdBy != n.UserID && !n.Admin {
		return nil, ErrForbidden
	}
	if removed.Valid {
		return nil, ErrForbidden
	}
	var vid int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO versions (rig_id, version, status, tarball_sha256, size, manifest_yaml, description, readme, targets, needs_secrets, needs_logins, layers, created_at,
		                      bundle, signer_issuer, signer_subject, signer_is_publisher)
		VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), $14, $15, $16) RETURNING id`,
		rigID, n.Version, n.TarballSHA, n.Size, n.ManifestYAML, n.Description, n.Readme,
		pq.Array(nz(n.Targets)), pq.Array(nz(n.NeedsSecrets)), pq.Array(nz(n.NeedsLogins)), pq.Array(nz(n.Layers)), s.now(),
		n.Bundle, n.SignerIssuer, n.SignerSubject, n.SignerIsPublisher).Scan(&vid)
	if err != nil {
		if isUnique(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	for _, f := range n.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO version_files (version_id, path, size, sha256, is_text) VALUES ($1, $2, $3, $4, $5)`, vid, f.Path, f.Size, f.SHA256, f.IsText); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (version_id, run_after) VALUES ($1, $2)`, vid, s.now()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rigs SET description = $2 WHERE id = $1 AND created_by = $3`, rigID, n.Description, n.UserID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Version{ID: vid, RigID: rigID, Version: n.Version, Status: "pending", TarballSHA256: n.TarballSHA, Size: n.Size}, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// VersionFiles lists the file index of a version.
func (s *Store) VersionFiles(ctx context.Context, versionID int64) ([]FileEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path, size, sha256, is_text FROM version_files WHERE version_id = $1 ORDER BY path`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileEntry
	for rows.Next() {
		var f FileEntry
		if err := rows.Scan(&f.Path, &f.Size, &f.SHA256, &f.IsText); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Yank hides a published version from resolution; exact-version pulls keep working. Owner or admin only.
func (s *Store) Yank(ctx context.Context, owner, name, ver, reason string, actor *User) error {
	rig, err := s.GetRig(ctx, owner, name, ViewerOf(actor))
	if err != nil {
		return err
	}
	if rig.CreatedBy != actor.ID && !actor.IsAdmin {
		return ErrForbidden
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE versions SET status = 'yanked', yanked_at = $3, yank_reason = $4 WHERE rig_id = $1 AND version = $2 AND status = 'published'`, rig.ID, ver, s.now(), reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.Audit(ctx, actor, "version.yank", owner+"/"+name+"@"+ver, map[string]string{"reason": reason})
	return nil
}

// RemoveVersion is the takedown: the version is unavailable to everyone but admins (admin action).
func (s *Store) RemoveVersion(ctx context.Context, owner, name, ver, reason string, actor *User) error {
	if actor == nil || !actor.IsAdmin {
		return ErrForbidden
	}
	var res sql.Result
	var err error
	if ver == "" {
		res, err = s.DB.ExecContext(ctx, `UPDATE rigs SET removed_at = $3 WHERE owner = $1 AND name = $2 AND removed_at IS NULL`, owner, name, s.now())
	} else {
		res, err = s.DB.ExecContext(ctx, `UPDATE versions v SET status = 'removed' FROM rigs r WHERE r.id = v.rig_id AND r.owner = $1 AND r.name = $2 AND v.version = $3 AND v.status <> 'removed'`, owner, name, ver)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	target := owner + "/" + name
	if ver != "" {
		target += "@" + ver
	}
	s.Audit(ctx, actor, "takedown", target, map[string]string{"reason": reason})
	return nil
}

// SetVisibility changes a rig's visibility (owner or admin). Going public requires the newest published version to
// have no unpinned-package warning and no findings: what a public rig must satisfy is decided by the scan, not the owner.
func (s *Store) SetVisibility(ctx context.Context, owner, name, vis string, actor *User) error {
	if vis != "public" && vis != "private" {
		return fmt.Errorf("visibility must be public or private")
	}
	rig, err := s.GetRig(ctx, owner, name, ViewerOf(actor))
	if err != nil {
		return err
	}
	if rig.CreatedBy != actor.ID && !actor.IsAdmin {
		return ErrForbidden
	}
	if vis == "public" {
		vs, err := s.ListVersions(ctx, rig.ID, ViewerOf(actor))
		if err != nil {
			return err
		}
		var latest *Version
		for i := range vs {
			if vs[i].Status == "published" {
				latest = &vs[i]
				break
			}
		}
		if latest == nil {
			return fmt.Errorf("%w: there is no published version yet; wait for the scan to finish", ErrConflict)
		}
		for _, w := range latest.Warnings {
			if w.Kind == "unpinned" {
				return fmt.Errorf("%w: %s@%s has unpinned packages; a public rig must pin what it runs (publish a fixed version first)", ErrConflict, rig.Name, latest.Version)
			}
		}
		if !rigApproved(ctx, s, rig.ID) {
			if reasons := s.reviewReasons(ctx, rig, latest); len(reasons) > 0 {
				s.autoReport(ctx, rig.ID, owner+"/"+name, strings.Join(reasons, "; "))
				return fmt.Errorf("%w: this rig needs an administrator's review before it can be public (%s). A review request has been filed; you will be able to make it public once it is approved", ErrConflict, strings.Join(reasons, "; "))
			}
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE rigs SET visibility = $2 WHERE id = $1`, rig.ID, vis); err != nil {
		return err
	}
	s.Audit(ctx, actor, "rig.visibility", owner+"/"+name, map[string]string{"to": vis})
	return nil
}

// SetStar stars or unstars a rig the user can see.
func (s *Store) SetStar(ctx context.Context, owner, name string, u *User, on bool) error {
	rig, err := s.GetRig(ctx, owner, name, ViewerOf(u))
	if err != nil {
		return err
	}
	if on {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO stars (user_id, rig_id, created_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, u.ID, rig.ID, s.now())
	} else {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM stars WHERE user_id = $1 AND rig_id = $2`, u.ID, rig.ID)
	}
	return err
}

// RigSummary is a search or profile row.
type RigSummary struct {
	Rig
	Latest string
	Owner_ string
}

func likeEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(q)
}

// Search lists public rigs with at least one published version, matching q in owner, name or description; empty q
// lists the newest. Private rigs never appear here, not even to their owner (they are listed on the profile).
func (s *Store) Search(ctx context.Context, q string, limit int, viewer Viewer) ([]RigSummary, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	pat := "%" + likeEscape(strings.ToLower(strings.TrimSpace(q))) + "%"
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+rigCols+`, (SELECT v.version FROM versions v WHERE v.rig_id = r.id AND v.status = 'published' ORDER BY v.created_at DESC LIMIT 1)
		FROM rigs r
		WHERE r.removed_at IS NULL AND r.visibility = 'public'
		  AND EXISTS (SELECT 1 FROM versions v WHERE v.rig_id = r.id AND v.status = 'published')
		  AND (lower(r.owner) LIKE $2 OR lower(r.name) LIKE $2 OR lower(r.description) LIKE $2 OR lower(r.owner || '/' || r.name) LIKE $2)
		ORDER BY (SELECT count(*) FROM stars s WHERE s.rig_id = r.id) DESC, r.created_at DESC
		LIMIT $3`, viewer.ID, pat, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSummaries(rows)
}

func scanSummaries(rows *sql.Rows) ([]RigSummary, error) {
	var out []RigSummary
	for rows.Next() {
		var r RigSummary
		var latest sql.NullString
		if err := rows.Scan(&r.ID, &r.Owner, &r.Name, &r.Description, &r.Visibility, &r.CreatedBy, &r.CreatedAt, &r.Stars, &r.Starred, &latest); err != nil {
			return nil, err
		}
		r.Latest = latest.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// OwnedRigs lists the rigs a user owns that the viewer may see (public ones for strangers; all for the owner).
func (s *Store) OwnedRigs(ctx context.Context, login string, viewer Viewer) ([]RigSummary, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+rigCols+`, (SELECT v.version FROM versions v WHERE v.rig_id = r.id AND v.status = 'published' ORDER BY v.created_at DESC LIMIT 1)
		FROM rigs r JOIN users u ON u.id = r.created_by
		WHERE u.login = $3 AND `+rigVisibleAdm+`
		  AND (r.visibility = 'private' OR EXISTS (SELECT 1 FROM versions v WHERE v.rig_id = r.id AND v.status = 'published'))
		ORDER BY r.created_at DESC`, viewer.ID, viewer.Admin, strings.ToLower(login))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSummaries(rows)
}

func rigApproved(ctx context.Context, s *Store, id int64) bool {
	var ok bool
	_ = s.DB.QueryRowContext(ctx, `SELECT public_approved FROM rigs WHERE id = $1`, id).Scan(&ok)
	return ok
}

// Bundle returns the stored Sigstore bundle of a version ("" when unsigned).
func (s *Store) Bundle(ctx context.Context, versionID int64) string {
	var b sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT bundle FROM versions WHERE id = $1`, versionID).Scan(&b)
	return b.String
}
