package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Collection limits: a collection is a reading list, not a mirror.
const (
	MaxCollectionsPerUser = 50
	MaxCollectionItems    = 100
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Collection is a curated list of rigs.
type Collection struct {
	ID          int64
	Owner       string // the owner's login
	OwnerID     int64
	Slug        string
	Title       string
	Description string
	Visibility  string
	CreatedAt   time.Time
}

// CollectionItem is one rig in a collection. Only rigs the viewer may see are ever returned.
type CollectionItem struct {
	RigSummary
	Note    string
	AddedAt time.Time
}

const collectionCols = `c.id, u.login, c.owner_id, c.slug, c.title, c.description, c.visibility, c.created_at`

func scanCollection(row interface{ Scan(...any) error }) (*Collection, error) {
	var c Collection
	err := row.Scan(&c.ID, &c.Owner, &c.OwnerID, &c.Slug, &c.Title, &c.Description, &c.Visibility, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// collectionVisible is the collection predicate: not removed, and public (by an enabled user) or the viewer's own; admins
// see all. Same shape as the rig predicate. $1 is the viewer id and $2 whether the viewer is an admin.
const collectionVisible = `(($2::boolean) OR c.removed_at IS NULL AND ((c.visibility = 'public' AND u.disabled_at IS NULL) OR c.owner_id = $1))`

// GetCollection returns a collection the viewer may see; a private or missing one is the same ErrNotFound.
func (s *Store) GetCollection(ctx context.Context, owner, slug string, v Viewer) (*Collection, error) {
	return scanCollection(s.DB.QueryRowContext(ctx, `SELECT `+collectionCols+` FROM collections c JOIN users u ON u.id = c.owner_id
		WHERE u.login = $3 AND c.slug = $4 AND `+collectionVisible+` ORDER BY (c.removed_at IS NULL) DESC LIMIT 1`, v.ID, v.Admin, strings.ToLower(owner), slug))
}

// CollectionItems lists the rigs of a collection that the viewer may see, in the order they were added. A rig the viewer
// cannot see is omitted without a trace (not counted, not a gap).
func (s *Store) CollectionItems(ctx context.Context, c *Collection, v Viewer) ([]CollectionItem, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+rigCols+`, (SELECT v.version FROM versions v WHERE v.rig_id = r.id AND v.status = 'published' ORDER BY v.created_at DESC, v.id DESC LIMIT 1), i.note, i.added_at
		FROM collection_items i JOIN rigs r ON r.id = i.rig_id
		WHERE i.collection_id = $3 AND `+rigVisibleAdm+`
		  AND (r.visibility = 'private' OR EXISTS (SELECT 1 FROM versions v WHERE v.rig_id = r.id AND v.status = 'published'))
		ORDER BY i.added_at, r.id`, v.ID, v.Admin, c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectionItem
	for rows.Next() {
		var it CollectionItem
		var latest sql.NullString
		if err := rows.Scan(&it.ID, &it.Owner, &it.Name, &it.Description, &it.Visibility, &it.CreatedBy, &it.CreatedAt, &it.Stars, &it.Starred, &latest, &it.Note, &it.AddedAt); err != nil {
			return nil, err
		}
		it.Latest = latest.String
		out = append(out, it)
	}
	return out, rows.Err()
}

// UserCollections lists a user's collections that the viewer may see.
func (s *Store) UserCollections(ctx context.Context, login string, v Viewer) ([]Collection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+collectionCols+` FROM collections c JOIN users u ON u.id = c.owner_id
		WHERE u.login = $3 AND `+collectionVisible+` ORDER BY c.created_at, c.id`, v.ID, v.Admin, strings.ToLower(login))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// NewCollection describes a collection to create.
type NewCollection struct{ Slug, Title, Description, Visibility string }

// CreateCollection makes a collection owned by u. ErrConflict when the slug is taken or the user has too many.
func (s *Store) CreateCollection(ctx context.Context, u *User, n NewCollection) (*Collection, error) {
	if n.Visibility == "" {
		n.Visibility = "public"
	}
	n.Title, n.Description = strings.TrimSpace(n.Title), strings.TrimSpace(n.Description)
	switch {
	case !slugRe.MatchString(n.Slug):
		return nil, fmt.Errorf("%w: the slug must be lower-case letters, digits and hyphens", ErrBadInput)
	case n.Title == "" || len(n.Title) > 80:
		return nil, fmt.Errorf("%w: the title must be 1 to 80 characters", ErrBadInput)
	case len(n.Description) > 500:
		return nil, fmt.Errorf("%w: the description is limited to 500 characters", ErrBadInput)
	case n.Visibility != "public" && n.Visibility != "private":
		return nil, fmt.Errorf("%w: visibility must be public or private", ErrBadInput)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM collections WHERE owner_id = $1 AND removed_at IS NULL`, u.ID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= MaxCollectionsPerUser {
		return nil, fmt.Errorf("%w: you already have %d collections", ErrConflict, MaxCollectionsPerUser)
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO collections (owner_id, slug, title, description, visibility, created_at) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		u.ID, n.Slug, n.Title, n.Description, n.Visibility, s.now()).Scan(&id)
	if isUnique(err) {
		return nil, fmt.Errorf("%w: you already have a collection called %s", ErrConflict, n.Slug)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(ctx, u, "collection.create", u.Login+"/"+n.Slug, map[string]string{"visibility": n.Visibility})
	return s.GetCollection(ctx, u.Login, n.Slug, ViewerOf(u))
}

// ErrBadInput marks a request the caller can fix.
var ErrBadInput = errors.New("bad input")

func (s *Store) ownCollection(ctx context.Context, u *User, slug string) (*Collection, error) {
	c, err := s.GetCollection(ctx, u.Login, slug, ViewerOf(u))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// AddToCollection adds a rig the OWNER can see. Adding again updates the note.
func (s *Store) AddToCollection(ctx context.Context, u *User, slug, rigOwner, rigName, note string) error {
	c, err := s.ownCollection(ctx, u, slug)
	if err != nil {
		return err
	}
	note = strings.TrimSpace(note)
	if len(note) > 200 {
		return fmt.Errorf("%w: a note is limited to 200 characters", ErrBadInput)
	}
	rig, err := s.GetRig(ctx, rigOwner, rigName, ViewerOf(u))
	if err != nil {
		return err
	}
	var n int
	var exists bool
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM collection_items WHERE collection_id = $1`, c.ID).Scan(&n); err != nil {
		return err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM collection_items WHERE collection_id = $1 AND rig_id = $2)`, c.ID, rig.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists && n >= MaxCollectionItems {
		return fmt.Errorf("%w: a collection holds at most %d rigs", ErrConflict, MaxCollectionItems)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO collection_items (collection_id, rig_id, note, added_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (collection_id, rig_id) DO UPDATE SET note = EXCLUDED.note`, c.ID, rig.ID, note, s.now()); err != nil {
		return err
	}
	s.Audit(ctx, u, "collection.add", u.Login+"/"+slug, map[string]string{"rig": rigOwner + "/" + rigName})
	return nil
}

// RemoveFromCollection takes a rig out of the user's own collection (idempotent).
func (s *Store) RemoveFromCollection(ctx context.Context, u *User, slug, rigOwner, rigName string) error {
	c, err := s.ownCollection(ctx, u, slug)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM collection_items WHERE collection_id = $1 AND rig_id = (SELECT id FROM rigs WHERE owner = $2 AND name = $3)`, c.ID, strings.ToLower(rigOwner), strings.ToLower(rigName))
	if err == nil {
		s.Audit(ctx, u, "collection.remove", u.Login+"/"+slug, map[string]string{"rig": rigOwner + "/" + rigName})
	}
	return err
}

// DeleteCollection removes a collection (soft: the row stays for the audit trail; the slug is free again).
func (s *Store) DeleteCollection(ctx context.Context, u *User, slug string) error {
	c, err := s.ownCollection(ctx, u, slug)
	if err != nil {
		return err
	}
	if c.OwnerID != u.ID && !u.IsAdmin {
		return ErrForbidden
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE collections SET removed_at = $2 WHERE id = $1`, c.ID, s.now())
	if err == nil {
		s.Audit(ctx, u, "collection.delete", u.Login+"/"+slug, nil)
	}
	return err
}
