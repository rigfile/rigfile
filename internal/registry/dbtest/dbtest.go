// Package dbtest gives each test its own Postgres schema. Tests that need a database call New; when
// RIGFILE_TEST_DATABASE_URL is not set they are skipped, so `go test ./...` still works on a laptop without Postgres.
// (CI provides one as a service container; locally: scripts/registry-test.sh.)
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/rigfile/rigfile/internal/registry"
)

// New returns a migrated database in a fresh schema that is dropped when the test ends.
func New(t testing.TB) *sql.DB {
	t.Helper()
	db, err := registry.Open(context.Background(), DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// DSN returns a connection string for a fresh, empty schema (dropped when the test ends). Callers that run the service
// itself (which migrates on start) use this instead of New.
func DSN(t testing.TB) string {
	t.Helper()
	base := os.Getenv("RIGFILE_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("RIGFILE_TEST_DATABASE_URL is not set (see scripts/registry-test.sh)")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	schema := "t_" + hex.EncodeToString(b[:])
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return u.String()
}
