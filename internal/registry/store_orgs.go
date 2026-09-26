package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Organisation limits.
const (
	MaxOrgsCreatedPerUser = 10
	MaxOrgMembers         = 200
)

// Org is an organisation: a namespace with members.
type Org struct {
	ID        int64
	Login     string
	Name      string
	CreatedBy int64
	CreatedAt time.Time
}

// Member is one person in an organisation.
type Member struct {
	Login string
	Role  string
}

// ErrNameTaken means the login is already a user or an organisation.
var ErrNameTaken = errors.New("that name is taken")

var roleRank = map[string]int{"": 0, "member": 1, "admin": 2, "owner": 3}

// The one definition of "this viewer belongs to this rig" (docs/orgs.md §3). A personal rig belongs to its creator; an
// organisation's rig belongs to the organisation's members, and to nobody else, whoever created it. $1 is the viewer id.
const rigMember = `(CASE WHEN r.org_id IS NULL THEN r.created_by = $1
	ELSE EXISTS (SELECT 1 FROM org_members om WHERE om.org_id = r.org_id AND om.user_id = $1) END)`

// RigRole is the user's role on a rig: "owner" for a personal rig's creator, the organisation role for an organisation's
// rig, "" for anyone else. It does not consider site admins.
func (s *Store) RigRole(ctx context.Context, u *User, rigID int64) string {
	if u == nil {
		return ""
	}
	var role string
	_ = s.DB.QueryRowContext(ctx, `SELECT CASE WHEN r.org_id IS NULL THEN CASE WHEN r.created_by = $2 THEN 'owner' ELSE '' END
		ELSE COALESCE((SELECT om.role FROM org_members om WHERE om.org_id = r.org_id AND om.user_id = $2), '') END FROM rigs r WHERE r.id = $1`, rigID, u.ID).Scan(&role)
	return role
}

// CanManage: may publish versions and yank. Site admins always may.
func (s *Store) CanManage(ctx context.Context, u *User, rig *Rig) bool {
	return u != nil && (u.IsAdmin || s.RigRole(ctx, u, rig.ID) != "")
}

// CanAdminister: may change visibility. A personal rig's creator, an organisation's owners and admins, site admins.
func (s *Store) CanAdminister(ctx context.Context, u *User, rig *Rig) bool {
	return u != nil && (u.IsAdmin || roleRank[s.RigRole(ctx, u, rig.ID)] >= roleRank["admin"])
}

// OrgByLogin finds an organisation that is not disabled.
func (s *Store) OrgByLogin(ctx context.Context, login string) (*Org, error) {
	var o Org
	err := s.DB.QueryRowContext(ctx, `SELECT id, login, name, created_by, created_at FROM orgs WHERE login = $1 AND disabled_at IS NULL`, strings.ToLower(login)).
		Scan(&o.ID, &o.Login, &o.Name, &o.CreatedBy, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

// OrgRole is a user's role in an organisation ("" = not a member).
func (s *Store) OrgRole(ctx context.Context, u *User, orgID int64) string {
	if u == nil {
		return ""
	}
	var role string
	_ = s.DB.QueryRowContext(ctx, `SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`, orgID, u.ID).Scan(&role)
	return role
}

// CreateOrg makes an organisation with u as its first owner.
func (s *Store) CreateOrg(ctx context.Context, u *User, login, name string) (*Org, error) {
	login, name = strings.ToLower(strings.TrimSpace(login)), strings.TrimSpace(name)
	switch {
	case !ownerRe.MatchString(login):
		return nil, fmt.Errorf("%w: the name must be lower-case letters, digits and hyphens", ErrBadInput)
	case IsReservedOwner(login):
		return nil, fmt.Errorf("%w: %s is a reserved name", ErrConflict, login)
	case len(name) > 80:
		return nil, fmt.Errorf("%w: the display name is limited to 80 characters", ErrBadInput)
	}
	var made int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM orgs WHERE created_by = $1`, u.ID).Scan(&made); err != nil {
		return nil, err
	}
	if made >= MaxOrgsCreatedPerUser && !u.IsAdmin {
		return nil, fmt.Errorf("%w: you have already created %d organisations", ErrConflict, MaxOrgsCreatedPerUser)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var o Org
	err = tx.QueryRowContext(ctx, `INSERT INTO orgs (login, name, created_by, created_at) VALUES ($1, $2, $3, $4) RETURNING id, login, name, created_by, created_at`, login, name, u.ID, s.now()).
		Scan(&o.ID, &o.Login, &o.Name, &o.CreatedBy, &o.CreatedAt)
	if isUnique(err) {
		return nil, fmt.Errorf("%w: %s is already a user or an organisation here", ErrNameTaken, login)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO org_members (org_id, user_id, role, added_at) VALUES ($1, $2, 'owner', $3)`, o.ID, u.ID, s.now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.Audit(ctx, u, "org.create", login, nil)
	return &o, nil
}

// Members lists an organisation's members. Only members (and site admins) may ask; anyone else gets ErrNotFound, the same
// as for an organisation that does not exist.
func (s *Store) Members(ctx context.Context, u *User, org *Org) ([]Member, error) {
	if u == nil || (!u.IsAdmin && s.OrgRole(ctx, u, org.ID) == "") {
		return nil, ErrNotFound
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT us.login, m.role FROM org_members m JOIN users us ON us.id = m.user_id WHERE m.org_id = $1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, us.login`, org.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Login, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UserOrgs lists the organisations a user belongs to, with their role.
func (s *Store) UserOrgs(ctx context.Context, u *User) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT o.login, m.role FROM org_members m JOIN orgs o ON o.id = m.org_id WHERE m.user_id = $1 AND o.disabled_at IS NULL ORDER BY o.login`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Login, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMember adds a member or changes their role. Owners may do anything; admins may add and change plain members only;
// nobody may leave an organisation without an owner.
func (s *Store) SetMember(ctx context.Context, actor *User, org *Org, login, role string) error {
	if _, ok := roleRank[role]; !ok || role == "" {
		return fmt.Errorf("%w: the role must be owner, admin or member", ErrBadInput)
	}
	mine := s.OrgRole(ctx, actor, org.ID)
	if roleRank[mine] < roleRank["admin"] {
		return ErrNotFoundOrForbidden(mine)
	}
	target, err := s.UserByLogin(ctx, login)
	if err != nil {
		return err
	}
	cur := s.OrgRole(ctx, target, org.ID)
	if mine == "admin" && (role != "member" || roleRank[cur] > roleRank["member"]) {
		return fmt.Errorf("%w: admins manage plain members only", ErrForbidden)
	}
	if cur == "owner" && role != "owner" {
		if last, err := s.lastOwner(ctx, org.ID); err != nil || last {
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: an organisation needs at least one owner", ErrConflict)
		}
	}
	if cur == "" {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org_members WHERE org_id = $1`, org.ID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxOrgMembers {
			return fmt.Errorf("%w: an organisation holds at most %d members", ErrConflict, MaxOrgMembers)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org_members (org_id, user_id, role, added_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`, org.ID, target.ID, role, s.now()); err != nil {
		return err
	}
	s.Audit(ctx, actor, "org.member", org.Login, map[string]string{"user": target.Login, "role": role})
	return nil
}

// ErrNotFoundOrForbidden hides an organisation from people who are not in it and refuses plain members.
func ErrNotFoundOrForbidden(role string) error {
	if role == "" {
		return ErrNotFound
	}
	return ErrForbidden
}

func (s *Store) lastOwner(ctx context.Context, orgID int64) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org_members WHERE org_id = $1 AND role = 'owner'`, orgID).Scan(&n)
	return n <= 1, err
}

// RemoveMember removes a member. Anyone may remove themselves; owners remove anyone; admins remove plain members.
func (s *Store) RemoveMember(ctx context.Context, actor *User, org *Org, login string) error {
	mine := s.OrgRole(ctx, actor, org.ID)
	if mine == "" {
		return ErrNotFound
	}
	target, err := s.UserByLogin(ctx, login)
	if err != nil {
		return err
	}
	cur := s.OrgRole(ctx, target, org.ID)
	if cur == "" {
		return ErrNotFound
	}
	self := target.ID == actor.ID
	switch {
	case self:
	case mine == "owner":
	case mine == "admin" && cur == "member":
	default:
		return fmt.Errorf("%w: you cannot remove this member", ErrForbidden)
	}
	if cur == "owner" {
		if last, err := s.lastOwner(ctx, org.ID); err != nil || last {
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: an organisation needs at least one owner", ErrConflict)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM org_members WHERE org_id = $1 AND user_id = $2`, org.ID, target.ID); err != nil {
		return err
	}
	s.Audit(ctx, actor, "org.member.remove", org.Login, map[string]string{"user": target.Login})
	return nil
}

// OrgRigs lists the rigs an organisation owns that the viewer may see.
func (s *Store) OrgRigs(ctx context.Context, org *Org, viewer Viewer) ([]RigSummary, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+rigCols+`, (SELECT v.version FROM versions v WHERE v.rig_id = r.id AND v.status = 'published' ORDER BY v.created_at DESC, v.id DESC LIMIT 1)
		FROM rigs r
		WHERE r.org_id = $3 AND `+rigVisibleAdm+`
		  AND (r.visibility = 'private' OR EXISTS (SELECT 1 FROM versions v WHERE v.rig_id = r.id AND v.status = 'published'))
		ORDER BY r.created_at DESC`, viewer.ID, viewer.Admin, org.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSummaries(rows)
}

// SetOrgDisabled disables or re-enables an organisation (admin action). A disabled organisation's rigs vanish for everyone
// but site admins (the visibility predicate), and it stops accepting new members and versions.
func (s *Store) SetOrgDisabled(ctx context.Context, login string, disabled bool) error {
	var at any
	if disabled {
		at = s.now()
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE orgs SET disabled_at = $2 WHERE login = $1`, strings.ToLower(login), at)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
