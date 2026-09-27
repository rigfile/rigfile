package registry

import (
	"context"
	"strings"
)

// Derived lists the public rigs whose newest published version builds on owner/name (a `from:` entry naming it, with or
// without a version range), most starred first, and how many there are. Only what Search would already show is returned:
// public, not removed, with a published version. A version that dropped the `from:` entry no longer counts.
func (s *Store) Derived(ctx context.Context, owner, name string, viewer Viewer, limit int) ([]RigSummary, int, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	ref := strings.ToLower(owner + "/" + name)
	const where = `
		FROM rigs r
		WHERE $1::bigint >= 0 AND r.removed_at IS NULL AND r.visibility = 'public' AND NOT (lower(r.owner) = $2 AND lower(r.name) = $3)
		  AND EXISTS (
		    SELECT 1 FROM versions v
		    WHERE v.id = (SELECT v2.id FROM versions v2 WHERE v2.rig_id = r.id AND v2.status = 'published' ORDER BY v2.created_at DESC, v2.id DESC LIMIT 1)
		      AND EXISTS (SELECT 1 FROM unnest(v.layers) l WHERE lower(split_part(l, '@', 1)) = $4))`
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) `+where, viewer.ID, strings.ToLower(owner), strings.ToLower(name), ref).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+rigCols+`, (SELECT v.version FROM versions v WHERE v.rig_id = r.id AND v.status = 'published' ORDER BY v.created_at DESC, v.id DESC LIMIT 1) `+where+`
		ORDER BY (SELECT count(*) FROM stars s WHERE s.rig_id = r.id) DESC, r.created_at DESC LIMIT $5`,
		viewer.ID, strings.ToLower(owner), strings.ToLower(name), ref, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanSummaries(rows)
	return out, total, err
}
