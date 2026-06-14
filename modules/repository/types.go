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
	DOI      string `json:"doi"`
	Filename string `json:"filename"`
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
}
