package repository

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PaperArchive persists scientific papers: their metadata in the database and
// their PDF files in a configurable storage directory. It contains only data
// access logic and is unaware of HTTP.
type PaperArchive struct {
	db         *sql.DB
	storageDir string
}

// NewPaperArchive creates a repository backed by the given database connection,
// storing uploaded PDFs under storageDir (e.g. "data/papers", which may be a
// mounted Google Drive folder).
func NewPaperArchive(db *sql.DB, storageDir string) *PaperArchive {
	return &PaperArchive{db: db, storageDir: storageDir}
}

// EnsureReady creates the storage directory and the "papers" table if they do
// not already exist.
func (a *PaperArchive) EnsureReady() error {
	if err := os.MkdirAll(a.storageDir, 0o755); err != nil {
		return err
	}
	const createPapersSQL = `CREATE TABLE IF NOT EXISTS papers (
		"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		"title" TEXT,
		"author" TEXT,
		"year" INTEGER,
		"abstract" TEXT,
		"doi" TEXT,
		"filename" TEXT
	);`
	if _, err := a.db.Exec(createPapersSQL); err != nil {
		return err
	}

	const createCitationsSQL = `CREATE TABLE IF NOT EXISTS citations (
		"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		"paper_id" INTEGER,
		"citation_number" INTEGER,
		"citation_title" TEXT,
		"citation_text" TEXT,
		"doi" TEXT,
		FOREIGN KEY(paper_id) REFERENCES papers(id)
	);`
	if _, err := a.db.Exec(createCitationsSQL); err != nil {
		return err
	}

	const createOccurrencesSQL = `CREATE TABLE IF NOT EXISTS citation_occurrences (
		"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		"citation_id" INTEGER,
		"occurrence_text" TEXT,
		"page" INTEGER,
		FOREIGN KEY(citation_id) REFERENCES citations(id)
	);`
	_, err := a.db.Exec(createOccurrencesSQL)
	return err
}

// OpenPDF opens a stored PDF file for reading. The caller must close it.
func (a *PaperArchive) OpenPDF(filename string) (*os.File, error) {
	return os.Open(a.PDFPath(filename))
}

// PDFPath returns the path to a stored PDF file within the storage directory.
func (a *PaperArchive) PDFPath(filename string) string {
	return filepath.Join(a.storageDir, filename)
}

// pdfFilename derives a PDF file name from the paper's DOI (filesystem-safe),
// falling back to "<id>.pdf" when no DOI is known.
func pdfFilename(doi string, id int) string {
	doi = strings.TrimSpace(doi)
	if doi == "" {
		return fmt.Sprintf("%d.pdf", id)
	}
	// Replace anything outside a safe set (DOIs contain "/", ":", etc.).
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, doi)
	return safe + ".pdf"
}

// List returns every paper held in the archive.
func (a *PaperArchive) List() ([]Paper, error) {
	rows, err := a.db.Query("SELECT id, title, author, year, abstract, doi, filename FROM papers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var papers []Paper
	for rows.Next() {
		var paper Paper
		if err := rows.Scan(&paper.ID, &paper.Title, &paper.Author, &paper.Year, &paper.Abstract, &paper.DOI, &paper.Filename); err != nil {
			return nil, err
		}
		papers = append(papers, paper)
	}
	return papers, rows.Err()
}

// Get returns a single paper identified by its ID. It returns sql.ErrNoRows
// when no paper with the given ID exists.
func (a *PaperArchive) Get(id int) (Paper, error) {
	var paper Paper
	err := a.db.QueryRow("SELECT id, title, author, year, abstract, doi, filename FROM papers WHERE id = ?", id).
		Scan(&paper.ID, &paper.Title, &paper.Author, &paper.Year, &paper.Abstract, &paper.DOI, &paper.Filename)
	return paper, err
}

// Create archives a new paper: it stores the metadata, writes the PDF read from
// pdf into the storage directory (named after the DOI when known, otherwise
// "<id>.pdf"), and returns the saved paper. If the file cannot be written, the
// metadata row is rolled back.
func (a *PaperArchive) Create(paper Paper, pdf io.Reader) (Paper, error) {
	result, err := a.db.Exec(
		"INSERT INTO papers (title, author, year, abstract, doi, filename) VALUES (?, ?, ?, ?, ?, '')",
		paper.Title, paper.Author, paper.Year, paper.Abstract, paper.DOI,
	)
	if err != nil {
		return Paper{}, err
	}

	lastID, err := result.LastInsertId()
	if err != nil {
		return Paper{}, err
	}
	id := int(lastID)

	filename := pdfFilename(paper.DOI, id)
	if err := a.writePDF(filename, pdf); err != nil {
		a.db.Exec("DELETE FROM papers WHERE id = ?", id) // best-effort rollback
		return Paper{}, err
	}

	if _, err := a.db.Exec("UPDATE papers SET filename = ? WHERE id = ?", filename, id); err != nil {
		os.Remove(a.PDFPath(filename))
		a.db.Exec("DELETE FROM papers WHERE id = ?", id)
		return Paper{}, err
	}

	paper.ID = id
	paper.Filename = filename
	return paper, nil
}

// Update replaces the stored metadata of an existing paper (the PDF is left
// untouched). It reports whether a paper with the given ID was found.
func (a *PaperArchive) Update(id int, paper Paper) (bool, error) {
	result, err := a.db.Exec(
		"UPDATE papers SET title = ?, author = ?, year = ?, abstract = ? WHERE id = ?",
		paper.Title, paper.Author, paper.Year, paper.Abstract, id,
	)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// Delete removes a paper's metadata and its PDF file. It reports whether a
// paper with the given ID was found.
func (a *PaperArchive) Delete(id int) (bool, error) {
	var filename string
	err := a.db.QueryRow("SELECT filename FROM papers WHERE id = ?", id).Scan(&filename)
	if err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}

	if _, err := a.db.Exec("DELETE FROM citation_occurrences WHERE citation_id IN (SELECT id FROM citations WHERE paper_id = ?)", id); err != nil {
		return false, err
	}
	if _, err := a.db.Exec("DELETE FROM citations WHERE paper_id = ?", id); err != nil {
		return false, err
	}
	if _, err := a.db.Exec("DELETE FROM papers WHERE id = ?", id); err != nil {
		return false, err
	}
	if filename != "" {
		os.Remove(a.PDFPath(filename)) // best-effort; the row is already gone
	}
	return true, nil
}

// ReplaceCitations stores the citations for a paper, replacing any previously
// stored for it. It runs in a single transaction.
func (a *PaperArchive) ReplaceCitations(paperID int, citations []Citation) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM citation_occurrences WHERE citation_id IN (SELECT id FROM citations WHERE paper_id = ?)", paperID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM citations WHERE paper_id = ?", paperID); err != nil {
		return err
	}

	citStmt, err := tx.Prepare("INSERT INTO citations (paper_id, citation_number, citation_title, citation_text, doi) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer citStmt.Close()

	occStmt, err := tx.Prepare("INSERT INTO citation_occurrences (citation_id, occurrence_text, page) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer occStmt.Close()

	for _, c := range citations {
		result, err := citStmt.Exec(paperID, c.Number, c.Title, c.Text, c.DOI)
		if err != nil {
			return err
		}
		citationID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		for _, o := range c.Occurrences {
			if _, err := occStmt.Exec(citationID, o.Text, o.Page); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// GetCitation returns a single citation by its ID (without occurrences). It
// returns sql.ErrNoRows when none exists.
func (a *PaperArchive) GetCitation(citationID int) (Citation, error) {
	var (
		c   Citation
		pid sql.NullInt64
	)
	err := a.db.QueryRow(
		"SELECT id, paper_id, citation_number, citation_title, citation_text, doi FROM citations WHERE id = ?",
		citationID,
	).Scan(&c.ID, &pid, &c.Number, &c.Title, &c.Text, &c.DOI)
	if err != nil {
		return Citation{}, err
	}
	if pid.Valid {
		id := int(pid.Int64)
		c.PaperID = &id
	}
	return c, nil
}

// UpdateCitationDOI sets the DOI of a single citation.
func (a *PaperArchive) UpdateCitationDOI(citationID int, doi string) error {
	_, err := a.db.Exec("UPDATE citations SET doi = ? WHERE id = ?", doi, citationID)
	return err
}

// CitationsForPaper returns the citations stored for a paper, ordered by their
// citation number.
func (a *PaperArchive) CitationsForPaper(paperID int) ([]Citation, error) {
	rows, err := a.db.Query(
		"SELECT id, paper_id, citation_number, citation_title, citation_text, doi FROM citations WHERE paper_id = ? ORDER BY citation_number",
		paperID,
	)
	if err != nil {
		return nil, err
	}

	var citations []Citation
	indexByID := map[int]int{} // citation id -> position in citations
	for rows.Next() {
		var (
			c   Citation
			pid sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &pid, &c.Number, &c.Title, &c.Text, &c.DOI); err != nil {
			rows.Close()
			return nil, err
		}
		if pid.Valid {
			id := int(pid.Int64)
			c.PaperID = &id
		}
		indexByID[c.ID] = len(citations)
		citations = append(citations, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Attach occurrences to their citations.
	occRows, err := a.db.Query(
		`SELECT o.citation_id, o.occurrence_text, o.page
		 FROM citation_occurrences o
		 JOIN citations c ON c.id = o.citation_id
		 WHERE c.paper_id = ?
		 ORDER BY o.id`,
		paperID,
	)
	if err != nil {
		return nil, err
	}
	defer occRows.Close()

	for occRows.Next() {
		var (
			citationID int
			o          Occurrence
		)
		if err := occRows.Scan(&citationID, &o.Text, &o.Page); err != nil {
			return nil, err
		}
		if i, ok := indexByID[citationID]; ok {
			citations[i].Occurrences = append(citations[i].Occurrences, o)
		}
	}
	return citations, occRows.Err()
}

// writePDF copies the contents of src into a file inside the storage directory.
func (a *PaperArchive) writePDF(filename string, src io.Reader) error {
	dest, err := os.Create(a.PDFPath(filename))
	if err != nil {
		return err
	}
	defer dest.Close()

	if _, err := io.Copy(dest, src); err != nil {
		return err
	}
	return nil
}
