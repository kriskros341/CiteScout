package repository

// Paper represents a single scientific paper stored in the archive. The PDF
// itself lives on disk; Filename is the name of that file within the archive's
// storage directory.
type Paper struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Year     int    `json:"year"`
	Abstract string `json:"abstract"`
	// DOI is the paper's own DOI, if GROBID extracted one.
	DOI string `json:"doi"`
	// Hash is the SHA-256 of the PDF contents, used to detect duplicate uploads.
	// It is an internal bookkeeping field and is not exposed over the API.
	Hash     string `json:"-"`
	Filename string `json:"filename"`
	// Tags are the free-form labels attached to this paper. They are populated
	// by the read methods that join paper_tags.
	Tags []string `json:"tags,omitempty"`
}

// Citation is a single bibliographic reference extracted from a paper.
type Citation struct {
	ID int `json:"id"`
	// PaperID links the citation to a paper in this archive. It is optional: a
	// citation may be stored without an associated paper.
	PaperID *int `json:"paper_id,omitempty"`
	// Number is the 1-based position of the citation in the bibliography.
	Number int `json:"number"`
	// Title is the title of the cited work, if one was parsed.
	Title string `json:"title"`
	// Text is the raw citation string as it appears in the paper.
	Text string `json:"text"`
	// DOI is the DOI of the cited work, if one was resolved.
	DOI string `json:"doi"`
	// Occurrences are the sentences in the body where this citation is cited.
	Occurrences []Occurrence `json:"occurrences"`
}

// Occurrence is one place in the body where a citation is referenced: the
// sentence containing the in-text marker and the page it appears on.
type Occurrence struct {
	Text string `json:"text"`
	Page int    `json:"page"`
}
