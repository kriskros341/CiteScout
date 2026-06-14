package repository

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"citescout/modules/doi"
)

// fakeResolver returns a fixed DOI for every query.
type fakeResolver struct{}

func (fakeResolver) FindDOI(ctx context.Context, query doi.Query) (string, error) {
	return "10.1234/test", nil
}

func TestDOIQueueFillsMissingDOIs(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	archive := NewPaperArchive(db, filepath.Join(dir, "papers"))
	if err := archive.EnsureReady(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO papers (title) VALUES ('p')"); err != nil {
		t.Fatal(err)
	}
	const paperID = 1
	citations := []Citation{
		{Number: 1, Title: "Some title", Text: "raw ref"},         // missing DOI -> should fill
		{Number: 2, Title: "Has doi", Text: "raw", DOI: "10.9/x"}, // already has DOI -> untouched
		{Number: 3, Text: "no title"},                             // no title -> skipped
	}
	if err := archive.ReplaceCitations(paperID, citations); err != nil {
		t.Fatal(err)
	}

	stored, err := archive.CitationsForPaper(paperID)
	if err != nil {
		t.Fatal(err)
	}

	q := NewDOIQueue(archive, fakeResolver{}, 8)
	q.Start(2)
	for _, c := range stored {
		if !q.Enqueue(c.ID) {
			t.Fatalf("Enqueue(%d) returned false", c.ID)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for _, c := range stored {
			if q.Status(c.ID) != DOIStatusDone {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got, err := archive.CitationsForPaper(paperID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].DOI != "10.1234/test" {
		t.Errorf("citation 1 DOI = %q, want filled", got[0].DOI)
	}
	if got[1].DOI != "10.9/x" {
		t.Errorf("citation 2 DOI = %q, want unchanged", got[1].DOI)
	}
	if got[2].DOI != "" {
		t.Errorf("citation 3 DOI = %q, want empty (no title)", got[2].DOI)
	}
}
