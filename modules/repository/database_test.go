package repository

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestPDFFilename(t *testing.T) {
	cases := []struct {
		doi  string
		id   int
		want string
	}{
		{"10.1145/3292500", 7, "10.1145_3292500.pdf"},
		{"10.18653/v1/d17-1151", 7, "10.18653_v1_d17-1151.pdf"},
		{"", 7, "7.pdf"},
		{"  ", 7, "7.pdf"},
		{"10.1/../x", 7, "10.1_.._x.pdf"}, // no path separators survive
	}
	for _, c := range cases {
		if got := pdfFilename(c.doi, c.id); got != c.want {
			t.Errorf("pdfFilename(%q, %d) = %q, want %q", c.doi, c.id, got, c.want)
		}
	}
}

func newTestArchive(t *testing.T) *PaperArchive {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	archive := NewPaperArchive(db, filepath.Join(dir, "papers"))
	if err := archive.EnsureReady(); err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestFindByHashDetectsDuplicate(t *testing.T) {
	archive := newTestArchive(t)

	first, err := archive.Create(Paper{Title: "Paper", Hash: "abc123"}, strings.NewReader("%PDF-1.4 body"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := archive.FindByHash("abc123")
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if got.ID != first.ID {
		t.Errorf("FindByHash returned paper %d, want %d", got.ID, first.ID)
	}

	if _, err := archive.FindByHash("does-not-exist"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("FindByHash(unknown) error = %v, want sql.ErrNoRows", err)
	}
	// An empty hash must never match (legacy rows have no hash).
	if _, err := archive.FindByHash(""); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("FindByHash(\"\") error = %v, want sql.ErrNoRows", err)
	}
}

func TestEnsureColumnIsIdempotentAndMigrates(t *testing.T) {
	archive := newTestArchive(t)

	// A second EnsureReady (which calls ensureColumn) must not fail or duplicate.
	if err := archive.EnsureReady(); err != nil {
		t.Fatalf("second EnsureReady: %v", err)
	}

	// Simulate a pre-deduplication database: a papers table without a hash
	// column. ensureColumn should add it.
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE papers ("id" INTEGER PRIMARY KEY, "title" TEXT)`); err != nil {
		t.Fatal(err)
	}
	legacy := NewPaperArchive(db, filepath.Join(dir, "papers"))
	if err := legacy.EnsureReady(); err != nil {
		t.Fatalf("EnsureReady on legacy db: %v", err)
	}
	if _, err := db.Exec("UPDATE papers SET hash = '' WHERE 1=0"); err != nil {
		t.Errorf("hash column missing after migration: %v", err)
	}
}
