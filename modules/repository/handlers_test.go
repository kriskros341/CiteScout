package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"citescout/modules/grobid"
	"citescout/modules/openalex"
)

// fakeAnalyzer is a stand-in for GROBID: it returns canned header and references
// without touching a real server (but it does drain the PDF reader, as the real
// client would).
type fakeAnalyzer struct {
	header grobid.Header
	refs   []grobid.Reference
}

func (f fakeAnalyzer) ProcessHeader(ctx context.Context, pdf io.Reader) (grobid.Header, error) {
	io.Copy(io.Discard, pdf)
	return f.header, nil
}

func (f fakeAnalyzer) ProcessReferences(ctx context.Context, pdf io.Reader) ([]grobid.Reference, error) {
	io.Copy(io.Discard, pdf)
	return f.refs, nil
}

// fakeFinder is a stand-in for the OpenAlex client.
type fakeFinder struct {
	works []openalex.Work
}

func (f fakeFinder) CitingWorks(ctx context.Context, doi string) ([]openalex.Work, error) {
	return f.works, nil
}

// testServer builds handlers over a fresh temp archive and returns a router.
func testServer(t *testing.T, analyzer paperAnalyzer, finder CitingWorksFinder) (*http.ServeMux, *PaperArchive) {
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
	h := NewPaperArchiveHandlers(archive, analyzer, nil, finder)
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, archive
}

// uploadPDF builds a multipart upload request carrying a small PDF payload.
func uploadPDF(t *testing.T, body []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("pdf", "paper.pdf")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(body)
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/papers/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

const samplePDF = "%PDF-1.4\nfake pdf body for tests\n"

func TestUploadAndListPaper(t *testing.T) {
	mux, _ := testServer(t, fakeAnalyzer{header: grobid.Header{Title: "A Paper", Author: "Jan Kowalski", Year: 2021}}, nil)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, uploadPDF(t, []byte(samplePDF)))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, want 303; body: %s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/papers/1" {
		t.Errorf("Location = %q, want /papers/1", loc)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/papers/", nil))
	var papers []Paper
	if err := json.Unmarshal(rec.Body.Bytes(), &papers); err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 || papers[0].Title != "A Paper" {
		t.Fatalf("listed papers = %+v, want one titled 'A Paper'", papers)
	}
}

func TestUploadDeduplicatesByHash(t *testing.T) {
	mux, archive := testServer(t, fakeAnalyzer{header: grobid.Header{Title: "Dup"}}, nil)

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, uploadPDF(t, []byte(samplePDF)))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("upload %d status = %d, want 303", i, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/papers/1" {
			t.Errorf("upload %d Location = %q, want /papers/1 (same paper)", i, loc)
		}
	}

	papers, err := archive.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 {
		t.Fatalf("archive holds %d papers, want 1 (duplicate rejected)", len(papers))
	}
}

func TestTagsAndFiltering(t *testing.T) {
	mux, _ := testServer(t, fakeAnalyzer{header: grobid.Header{
		Title: "Neural Nets", Author: "Ada Lovelace", Abstract: "about deep learning",
	}}, nil)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, uploadPDF(t, []byte(samplePDF)))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d", rec.Code)
	}

	// Tag the paper.
	form := strings.NewReader("tags=ml, nlp")
	req := httptest.NewRequest(http.MethodPost, "/api/papers/1/tags", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("set tags status = %d, want 303", rec.Code)
	}

	cases := []struct {
		query string
		want  int
	}{
		{"tag=ml", 1},
		{"tag=missing", 0},
		{"author=Lovelace", 1},
		{"author=Nobody", 0},
		{"q=deep", 1},   // abstract match
		{"q=Neural", 1}, // title match
		{"q=zzzzzz", 0}, // no match
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/papers/?"+c.query, nil))
		var papers []Paper
		if err := json.Unmarshal(rec.Body.Bytes(), &papers); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if len(papers) != c.want {
			t.Errorf("filter %q returned %d papers, want %d", c.query, len(papers), c.want)
		}
	}
}

func TestCitingWorksFragment(t *testing.T) {
	finder := fakeFinder{works: []openalex.Work{{Title: "Citing Work", DOI: "10.1/citing", Year: 2024}}}
	mux, _ := testServer(t, fakeAnalyzer{header: grobid.Header{Title: "Cited", DOI: "10.1/cited"}}, finder)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, uploadPDF(t, []byte(samplePDF)))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/papers/1/citing-works", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("citing-works status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Citing Work") {
		t.Errorf("citing-works fragment missing the work title; got: %s", rec.Body)
	}
}

func TestValidateFetchURLRejectsNonPublic(t *testing.T) {
	bad := []string{
		"http://127.0.0.1/x",
		"http://localhost/x",
		"http://10.0.0.1/x",
		"http://169.254.169.254/latest/meta-data", // cloud metadata
		"ftp://example.com/x",                     // wrong scheme
		"file:///etc/passwd",                      // wrong scheme
		"/etc/passwd",                             // no scheme/host
	}
	for _, raw := range bad {
		if err := validateFetchURL(raw); err == nil {
			t.Errorf("validateFetchURL(%q) = nil, want error", raw)
		}
	}
}
