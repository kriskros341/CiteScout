package repository

import (
	"sort"
	"strings"
)

// FilterOptions narrows a paper listing. All fields are optional; an empty
// FilterOptions lists every paper. Filters combine with AND.
type FilterOptions struct {
	// Author matches papers whose author string contains this substring
	// (case-insensitive).
	Author string
	// Tag matches papers carrying exactly this tag.
	Tag string
	// Query matches papers whose title, abstract or any reference text contains
	// this substring (case-insensitive).
	Query string
}

// empty reports whether no filter is set.
func (f FilterOptions) empty() bool {
	return strings.TrimSpace(f.Author) == "" &&
		strings.TrimSpace(f.Tag) == "" &&
		strings.TrimSpace(f.Query) == ""
}

// ListFiltered returns the papers matching opts, each with its tags attached,
// ordered by id. With an empty FilterOptions it returns every paper.
func (a *PaperArchive) ListFiltered(opts FilterOptions) ([]Paper, error) {
	var (
		where []string
		args  []any
		joins []string
	)

	if author := strings.TrimSpace(opts.Author); author != "" {
		where = append(where, "p.author LIKE ?")
		args = append(args, "%"+author+"%")
	}
	if tag := strings.TrimSpace(opts.Tag); tag != "" {
		joins = append(joins, "JOIN paper_tags ft ON ft.paper_id = p.id")
		where = append(where, "ft.tag = ?")
		args = append(args, tag)
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		// Search the title and abstract, plus the text of any reference. The
		// LEFT JOIN keeps papers with no citations; the OR over a joined column
		// is why SELECT DISTINCT is needed.
		joins = append(joins, "LEFT JOIN citations fc ON fc.paper_id = p.id")
		where = append(where, "(p.title LIKE ? OR p.abstract LIKE ? OR fc.citation_text LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}

	query := "SELECT DISTINCT p.id, p.title, p.author, p.year, p.doi, p.abstract, p.filename FROM papers p"
	if len(joins) > 0 {
		query += " " + strings.Join(joins, " ")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY p.id"

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var papers []Paper
	for rows.Next() {
		var paper Paper
		if err := rows.Scan(&paper.ID, &paper.Title, &paper.Author, &paper.Year, &paper.DOI, &paper.Abstract, &paper.Filename); err != nil {
			return nil, err
		}
		papers = append(papers, paper)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := a.attachTags(papers); err != nil {
		return nil, err
	}
	return papers, nil
}

// attachTags fills the Tags field of each paper in one query (avoiding an N+1
// over individual papers).
func (a *PaperArchive) attachTags(papers []Paper) error {
	if len(papers) == 0 {
		return nil
	}
	index := make(map[int]int, len(papers)) // paper id -> position
	for i := range papers {
		index[papers[i].ID] = i
	}

	rows, err := a.db.Query("SELECT paper_id, tag FROM paper_tags ORDER BY tag")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			paperID int
			tag     string
		)
		if err := rows.Scan(&paperID, &tag); err != nil {
			return err
		}
		if i, ok := index[paperID]; ok {
			papers[i].Tags = append(papers[i].Tags, tag)
		}
	}
	return rows.Err()
}

// TagsForPaper returns the tags attached to a single paper, ordered alphabetically.
func (a *PaperArchive) TagsForPaper(paperID int) ([]string, error) {
	rows, err := a.db.Query("SELECT tag FROM paper_tags WHERE paper_id = ? ORDER BY tag", paperID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// SetTags replaces all tags on a paper with the given set, in one transaction.
// Tags are trimmed; blanks and duplicates are dropped.
func (a *PaperArchive) SetTags(paperID int, tags []string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM paper_tags WHERE paper_id = ?", paperID); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO paper_tags (paper_id, tag) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		if _, err := stmt.Exec(paperID, tag); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AllTags returns the distinct tags used across the archive, alphabetically.
func (a *PaperArchive) AllTags() ([]string, error) {
	rows, err := a.db.Query("SELECT DISTINCT tag FROM paper_tags ORDER BY tag")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// Authors returns the distinct individual author names across the archive,
// alphabetically. A paper's author field is a comma-separated list, so it is
// split back into individual names here.
func (a *PaperArchive) Authors() ([]string, error) {
	rows, err := a.db.Query("SELECT author FROM papers WHERE author != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var combined string
		if err := rows.Scan(&combined); err != nil {
			return nil, err
		}
		for _, name := range strings.Split(combined, ",") {
			if name = strings.TrimSpace(name); name != "" {
				seen[name] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	authors := make([]string, 0, len(seen))
	for name := range seen {
		authors = append(authors, name)
	}
	sort.Strings(authors)
	return authors, nil
}
