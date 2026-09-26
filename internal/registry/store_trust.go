package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/similar"
)

// SimilarCandidates lists the public rigs a new name is compared with: published, public, not removed.
func (s *Store) SimilarCandidates(ctx context.Context) ([]similar.Candidate, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.owner, r.name, (SELECT count(*) FROM stars st WHERE st.rig_id = r.id), u.verified_at IS NOT NULL
		FROM rigs r JOIN users u ON u.id = r.created_by
		WHERE r.removed_at IS NULL AND r.visibility = 'public' AND u.disabled_at IS NULL
		  AND EXISTS (SELECT 1 FROM versions v WHERE v.rig_id = r.id AND v.status = 'published')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []similar.Candidate
	for rows.Next() {
		var c similar.Candidate
		if err := rows.Scan(&c.Owner, &c.Name, &c.Stars, &c.Verified); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- verified publishers ----------------------------------------------------------------------------------------

// SetVerified marks a publisher as verified by an administrator (kind: domain, organisation or person). The note is
// for the operator's records and is never shown publicly.
func (s *Store) SetVerified(ctx context.Context, login, kind, note string, admin *User) error {
	if admin == nil || !admin.IsAdmin {
		return ErrForbidden
	}
	if kind != "domain" && kind != "organisation" && kind != "person" {
		return fmt.Errorf("kind must be domain, organisation or person")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET verified_at = $2, verified_kind = $3, verified_note = $4 WHERE login = $1 AND disabled_at IS NULL`, strings.ToLower(login), s.now(), kind, trunc(note, 300))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.Audit(ctx, admin, "publisher.verify", strings.ToLower(login), map[string]string{"kind": kind})
	return nil
}

// ClearVerified removes the badge.
func (s *Store) ClearVerified(ctx context.Context, login string, admin *User) error {
	if admin == nil || !admin.IsAdmin {
		return ErrForbidden
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET verified_at = NULL, verified_kind = '', verified_note = '' WHERE login = $1`, strings.ToLower(login))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.Audit(ctx, admin, "publisher.unverify", strings.ToLower(login), nil)
	return nil
}

// ---- settings and the publishing pause ---------------------------------------------------------------------------

// Setting reads a runtime setting ("" when unset).
func (s *Store) Setting(ctx context.Context, key string) string {
	var v string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetSetting writes a runtime setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, key, value, s.now())
	return err
}

// PublishingPaused reports the incident switch: while set, uploads are refused (docs/incident-response.md).
func (s *Store) PublishingPaused(ctx context.Context) (bool, string) {
	v := s.Setting(ctx, "publishing_paused")
	return v != "", v
}

// PausePublishing stops uploads with a message; ResumePublishing lifts it. Both are audited.
func (s *Store) PausePublishing(ctx context.Context, reason string, actor *User) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("give a reason")
	}
	if err := s.SetSetting(ctx, "publishing_paused", trunc(reason, 300)); err != nil {
		return err
	}
	s.Audit(ctx, actor, "publishing.pause", "registry", map[string]string{"reason": reason})
	return nil
}

// ResumePublishing lifts the pause.
func (s *Store) ResumePublishing(ctx context.Context, actor *User) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM settings WHERE key = 'publishing_paused'`); err != nil {
		return err
	}
	s.Audit(ctx, actor, "publishing.resume", "registry", nil)
	return nil
}

// RevokeTokens revokes every API token of one account (login) or of all accounts (login == "").
func (s *Store) RevokeTokens(ctx context.Context, login string, actor *User) (int64, error) {
	var res sql.Result
	var err error
	if login == "" {
		res, err = s.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = $1 WHERE revoked_at IS NULL`, s.now())
	} else {
		res, err = s.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = $2 WHERE revoked_at IS NULL AND user_id = (SELECT id FROM users WHERE login = $1)`, strings.ToLower(login), s.now())
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.Audit(ctx, actor, "tokens.revoke", firstNonEmpty(login, "ALL"), map[string]int64{"count": n})
	return n, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- the moderation queue -----------------------------------------------------------------------------------------

// HeldVersion is a version waiting for an administrator.
type HeldVersion struct {
	ID        int64
	Ref       string
	Version   string
	Reason    string
	CreatedAt time.Time
}

// HeldVersions lists versions waiting for review.
func (s *Store) HeldVersions(ctx context.Context) ([]HeldVersion, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT v.id, r.owner || '/' || r.name, v.version, v.held_reason, v.created_at FROM versions v JOIN rigs r ON r.id = v.rig_id WHERE v.status = 'held' ORDER BY v.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HeldVersion
	for rows.Next() {
		var h HeldVersion
		if err := rows.Scan(&h.ID, &h.Ref, &h.Version, &h.Reason, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DecideHeld releases a held version (published) or rejects it, with an audit entry.
func (s *Store) DecideHeld(ctx context.Context, versionID int64, release bool, note string, admin *User) error {
	if admin == nil || !admin.IsAdmin {
		return ErrForbidden
	}
	status := "rejected"
	if release {
		status = "published"
	}
	var ref string
	err := s.DB.QueryRowContext(ctx, `
		UPDATE versions v SET status = $2, held_reason = CASE WHEN $2 = 'rejected' THEN v.held_reason || ' | rejected by review: ' || $3 ELSE v.held_reason END
		FROM rigs r WHERE r.id = v.rig_id AND v.id = $1 AND v.status = 'held' RETURNING r.owner || '/' || r.name || '@' || v.version`, versionID, status, trunc(note, 200)).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	action := map[bool]string{true: "held.release", false: "held.reject"}[release]
	s.Audit(ctx, admin, action, ref, map[string]string{"note": note})
	return nil
}

// ApprovePublic lets a rig go (or stay) public after an administrator reviewed the reasons it needed review.
func (s *Store) ApprovePublic(ctx context.Context, owner, name string, admin *User) error {
	if admin == nil || !admin.IsAdmin {
		return ErrForbidden
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE rigs SET public_approved = true, visibility = 'public' WHERE owner = $1 AND name = $2 AND removed_at IS NULL`, owner, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.Audit(ctx, admin, "rig.approve-public", owner+"/"+name, nil)
	return nil
}

// reviewReasons says why a rig may not go public without an administrator: danger-level analysis on its newest published
// version, or a name confusably close to a notable rig. Empty means it may.
func (s *Store) reviewReasons(ctx context.Context, rig *Rig, latest *Version) []string {
	var reasons []string
	if latest != nil && hasDanger(latest.Analysis) {
		reasons = append(reasons, "static analysis found danger-level patterns ("+dangerRules(latest.Analysis)+")")
	}
	cands, err := s.SimilarCandidates(ctx)
	if err == nil {
		for _, m := range similar.Find(rig.Owner, rig.Name, cands) {
			if m.Strong() && m.Notable(5) {
				reasons = append(reasons, fmt.Sprintf("the name is confusingly close to %s", m.Ref))
			}
		}
	}
	return reasons
}

// autoReport files a review request in the moderation queue on behalf of the system.
func (s *Store) autoReport(ctx context.Context, rigID int64, ref, details string) {
	var exists bool
	_ = s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM reports WHERE rig_id = $1 AND status = 'open' AND reporter_id IS NULL)`, rigID).Scan(&exists)
	if exists {
		return
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO reports (rig_id, rig_ref, version, reporter_id, reason, details, created_at) VALUES ($1, $2, '', NULL, 'other', $3, $4)`, rigID, ref, trunc("automatic review request: "+details, 2000), s.now())
}

// Trust is the set of facts shown to someone deciding whether to pull a rig (docs/trust.md §6b). Numbers, not a score.
type Trust struct {
	RigCreatedAt     time.Time      `json:"rig_created_at"`
	VersionCreatedAt time.Time      `json:"version_created_at"`
	Versions         int            `json:"versions"`
	Stars            int            `json:"stars"`
	Publisher        TrustPub       `json:"publisher"`
	Signature        TrustSig       `json:"signature"`
	Analysis         map[string]int `json:"analysis"`
	SimilarTo        []SimilarRig   `json:"similar_to"`
	History          TrustHistory   `json:"history"`
}

// TrustPub describes the publisher.
type TrustPub struct {
	Login        string    `json:"login"`
	FirstSeen    time.Time `json:"first_seen"`
	PublicRigs   int       `json:"public_rigs"`
	Verified     bool      `json:"verified"`
	VerifiedKind string    `json:"verified_kind,omitempty"`
}

// TrustSig is the signature status of the version.
type TrustSig struct {
	Signed      bool   `json:"signed"`
	ByPublisher bool   `json:"by_publisher"`
	Issuer      string `json:"issuer,omitempty"`
	Subject     string `json:"subject,omitempty"`
	Error       string `json:"error,omitempty"`
}

// TrustHistory counts past moderation events.
type TrustHistory struct {
	YankedVersions           int `json:"yanked_versions"`
	RemovedVersionsPublisher int `json:"removed_versions_by_publisher"`
}

// Trust gathers the facts for a version the viewer is allowed to see.
func (s *Store) Trust(ctx context.Context, rig *Rig, v *Version) (*Trust, error) {
	t := &Trust{RigCreatedAt: rig.CreatedAt, VersionCreatedAt: v.CreatedAt, Stars: rig.Stars, Analysis: map[string]int{"danger": 0, "caution": 0, "notice": 0}}
	var verifiedAt sql.NullTime
	if err := s.DB.QueryRowContext(ctx, `SELECT u.login, u.created_at, u.verified_at, u.verified_kind FROM users u WHERE u.id = $1`, rig.CreatedBy).
		Scan(&t.Publisher.Login, &t.Publisher.FirstSeen, &verifiedAt, &t.Publisher.VerifiedKind); err != nil {
		return nil, err
	}
	t.Publisher.Verified = verifiedAt.Valid
	if !verifiedAt.Valid {
		t.Publisher.VerifiedKind = ""
	}
	_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM rigs WHERE created_by = $1 AND visibility = 'public' AND removed_at IS NULL`, rig.CreatedBy).Scan(&t.Publisher.PublicRigs)
	_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM versions WHERE rig_id = $1 AND status IN ('published','yanked')`, rig.ID).Scan(&t.Versions)
	_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM versions WHERE rig_id = $1 AND status = 'yanked'`, rig.ID).Scan(&t.History.YankedVersions)
	_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM versions v JOIN rigs r ON r.id = v.rig_id WHERE r.created_by = $1 AND v.status = 'removed'`, rig.CreatedBy).Scan(&t.History.RemovedVersionsPublisher)
	for _, f := range v.Analysis {
		t.Analysis[f.Level]++
	}
	for _, m := range v.SimilarTo {
		if m.Verified || m.Stars >= 5 {
			t.SimilarTo = append(t.SimilarTo, m)
		}
	}
	t.Signature = TrustSig{Signed: v.SignerSubject != "", ByPublisher: v.SignerIsPublisher, Issuer: v.SignerIssuer, Subject: v.SignerSubject, Error: v.SignatureError}
	return t, nil
}
