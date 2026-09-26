package registry

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReportReasons are the categories a report can carry.
var ReportReasons = []string{"malware", "secret", "illegal", "abuse", "impersonation", "other"}

// Report is an abuse report about a rig or version.
type Report struct {
	ID        int64
	RigRef    string
	Version   string
	Reporter  string
	Reason    string
	Details   string
	Status    string
	CreatedAt time.Time
}

// CreateReport records a report from a signed-in user. The rig must be visible to the reporter (so reports cannot be
// used to probe for private rigs).
func (s *Store) CreateReport(ctx context.Context, u *User, owner, name, version, reason, details string) error {
	valid := false
	for _, r := range ReportReasons {
		valid = valid || r == reason
	}
	if !valid {
		return fmt.Errorf("choose a reason from the list")
	}
	rig, err := s.GetRig(ctx, owner, name, ViewerOf(u))
	if err != nil {
		return ErrNotFound
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO reports (rig_id, rig_ref, version, reporter_id, reason, details, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		rig.ID, owner+"/"+name, version, u.ID, reason, trunc(details, 2000), s.now()); err != nil {
		return err
	}
	s.Audit(ctx, u, "report.create", owner+"/"+name, map[string]string{"reason": reason, "version": version})
	return nil
}

// OpenReports lists reports still waiting for an admin.
func (s *Store) OpenReports(ctx context.Context) ([]Report, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.id, r.rig_ref, r.version, coalesce(u.login, ''), r.reason, r.details, r.status, r.created_at
		FROM reports r LEFT JOIN users u ON u.id = r.reporter_id WHERE r.status = 'open' ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		var x Report
		if err := rows.Scan(&x.ID, &x.RigRef, &x.Version, &x.Reporter, &x.Reason, &x.Details, &x.Status, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ResolveReport closes a report as actioned or dismissed.
func (s *Store) ResolveReport(ctx context.Context, id int64, status string, admin *User) error {
	if admin == nil || !admin.IsAdmin {
		return ErrForbidden
	}
	if status != "actioned" && status != "dismissed" {
		return fmt.Errorf("status must be actioned or dismissed")
	}
	var by any // the operator's command-line identity has no account row
	if admin.ID != 0 {
		by = admin.ID
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE reports SET status = $2, handled_by = $3, handled_at = $4 WHERE id = $1 AND status = 'open'`, id, status, by, s.now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.Audit(ctx, admin, "report."+status, fmt.Sprint(id), nil)
	return nil
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	At     time.Time
	Actor  string
	Action string
	Target string
	Detail string
}

// RecentAudit returns the newest audit entries.
func (s *Store) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT at, actor_login, action, target, detail::text FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.At, &a.Actor, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BootstrapUser creates (or finds) an account without GitHub, for the operator's break-glass tooling and for tests of a
// deployment (`rigfile-registry admin create-user`). The account still needs a token to do anything.
func (s *Store) BootstrapUser(ctx context.Context, githubID int64, login string, admin bool) (*User, error) {
	login = strings.ToLower(login)
	if !ownerRe.MatchString(login) {
		return nil, fmt.Errorf("%q is not a valid login", login)
	}
	u, err := s.UpsertUser(ctx, GitHubUser{ID: githubID, Login: login}, admin)
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, nil, "admin.bootstrap-user", login, map[string]bool{"admin": admin})
	return u, nil
}
