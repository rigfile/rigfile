package registry_test

import (
	"context"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/registry"
	"github.com/digitaldreamer3462/rigfile/internal/registry/dbtest"
)

func TestMigrationsApplyOnceAndConstraintsHold(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	// running again is a no-op
	if err := registry.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("migrations recorded: %d %v", n, err)
	}
	if _, err := db.Exec(`INSERT INTO users (github_id, login) VALUES (1, 'Jia')`); err == nil {
		t.Fatal("a login must be lowercase")
	}
	if _, err := db.Exec(`INSERT INTO users (github_id, login) VALUES (1, 'jia')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (github_id, login) VALUES (1, 'other')`); err == nil {
		t.Fatal("a GitHub id is unique")
	}
	var uid int64
	_ = db.QueryRow(`SELECT id FROM users WHERE login='jia'`).Scan(&uid)
	if _, err := db.Exec(`INSERT INTO rigs (owner, name, created_by) VALUES ('jia', 'Bad Name', $1)`, uid); err == nil {
		t.Fatal("rig names are validated by the database too")
	}
	if _, err := db.Exec(`INSERT INTO rigs (owner, name, created_by) VALUES ('jia', 'demo', $1)`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rigs (owner, name, created_by) VALUES ('jia', 'demo', $1)`, uid); err == nil {
		t.Fatal("owner/name is unique")
	}
	var rid int64
	_ = db.QueryRow(`SELECT id FROM rigs`).Scan(&rid)
	ins := func(v string) error {
		_, err := db.Exec(`INSERT INTO versions (rig_id, version, tarball_sha256, size, manifest_yaml) VALUES ($1, $2, repeat('a',64), 1, 'x')`, rid, v)
		return err
	}
	if err := ins("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := ins("1.0.0"); err == nil {
		t.Fatal("a version is unique per rig: immutability starts in the schema")
	}
	if err := ins("v1.0"); err == nil {
		t.Fatal("versions are validated by the database")
	}
}
