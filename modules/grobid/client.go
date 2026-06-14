// Package grobid is a small client for a GROBID server. It is used to extract
// the bibliographic references (citations) out of a scientific paper's PDF.
package grobid

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client talks to a GROBID HTTP server (see docker-compose.yml, port 8070).
type Client struct {
	baseURL    string
	httpClient *http.Client
	// Consolidate, when true, asks GROBID to look citations up against CrossRef
	// so DOIs are filled in. It is slower and requires network access, so it is
	// off by default.
	Consolidate bool
}

// NewClient creates a client for the GROBID server at baseURL, e.g.
// "http://localhost:8070".
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// PDF parsing can be slow, so allow a generous timeout.
		httpClient: &http.Client{Timeout: 3 * time.Minute},
	}
}

// Header holds the bibliographic metadata extracted from a paper's header.
type Header struct {
	Title    string
	Author   string
	Year     int
	Abstract string
	// DOI is the paper's own DOI, if present in the header.
	DOI string
}

// Occurrence is one place in the body where a reference is cited: the sentence
// containing the in-text marker and the page it appears on.
type Occurrence struct {
	Text string
	Page int
}

// Reference is a single bibliographic reference extracted from a paper.
type Reference struct {
	// Number is the 1-based position of the reference in the bibliography.
	Number int
	// Title is the title of the cited work, if GROBID parsed one.
	Title string
	// Text is the raw citation string as it appears in the paper.
	Text string
	// DOI is the DOI of the cited work, if GROBID could resolve one.
	DOI string
	// Occurrences are the sentences in the body where this reference is cited.
	Occurrences []Occurrence
}

// ProcessHeader sends a PDF to GROBID's /api/processHeaderDocument endpoint and
// returns the extracted title, authors, year and abstract.
func (c *Client) ProcessHeader(ctx context.Context, pdf io.Reader) (Header, error) {
	fields := map[string]string{}
	if c.Consolidate {
		fields["consolidateHeader"] = "1"
	}

	respBody, err := c.postPDF(ctx, "/api/processHeaderDocument", pdf, fields)
	if err != nil {
		return Header{}, err
	}
	defer respBody.Close()

	return parseHeader(respBody)
}

// ProcessReferences sends a PDF to GROBID's /api/processFulltextDocument
// endpoint and returns the parsed references. The full document (not just the
// reference list) is processed so that, for each reference, we also learn the
// sentences in the body where it is cited and on which page. segmentSentences
// gives us sentence boundaries and teiCoordinates=ref annotates each in-text
// marker with its page coordinates.
func (c *Client) ProcessReferences(ctx context.Context, pdf io.Reader) ([]Reference, error) {
	fields := map[string]string{
		"includeRawCitations": "1",
		"segmentSentences":     "1",
		"teiCoordinates":       "ref",
	}
	if c.Consolidate {
		fields["consolidateCitations"] = "1"
	}

	respBody, err := c.postPDF(ctx, "/api/processFulltextDocument", pdf, fields)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	return parseFulltext(respBody)
}

// postPDF uploads a PDF as multipart/form-data to a GROBID endpoint along with
// the given extra form fields, returning the response body on success. The
// caller must close the returned reader.
func (c *Client) postPDF(ctx context.Context, path string, pdf io.Reader, fields map[string]string) (io.ReadCloser, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	part, err := mw.CreateFormFile("input", "paper.pdf")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, pdf); err != nil {
		return nil, err
	}
	for name, value := range fields {
		if err := mw.WriteField(name, value); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/xml")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling grobid: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("grobid returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	return resp.Body, nil
}

// TEI structures for the document header.
type teiPersName struct {
	Forenames []string `xml:"forename"`
	Surname   string   `xml:"surname"`
}

type teiAuthor struct {
	PersName teiPersName `xml:"persName"`
}

type teiDate struct {
	Type string `xml:"type,attr"`
	When string `xml:"when,attr"`
}

type teiHeaderDoc struct {
	Title        string      `xml:"teiHeader>fileDesc>titleStmt>title"`
	Authors      []teiAuthor `xml:"teiHeader>fileDesc>sourceDesc>biblStruct>analytic>author"`
	Dates        []teiDate   `xml:"teiHeader>fileDesc>sourceDesc>biblStruct>monogr>imprint>date"`
	Idnos        []teiIdno   `xml:"teiHeader>fileDesc>sourceDesc>biblStruct>idno"`
	AnalyticIdno []teiIdno   `xml:"teiHeader>fileDesc>sourceDesc>biblStruct>analytic>idno"`
	AbstractDivP []string    `xml:"teiHeader>profileDesc>abstract>div>p"`
	AbstractP    []string    `xml:"teiHeader>profileDesc>abstract>p"`
}

// parseHeader decodes the TEI header document into a Header.
func parseHeader(r io.Reader) (Header, error) {
	var doc teiHeaderDoc
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return Header{}, err
	}

	header := Header{
		Title: strings.TrimSpace(doc.Title),
		Year:  headerYear(doc.Dates),
	}

	for _, idno := range append(doc.Idnos, doc.AnalyticIdno...) {
		if strings.EqualFold(idno.Type, "DOI") {
			header.DOI = strings.TrimSpace(idno.Value)
			break
		}
	}

	var authors []string
	for _, a := range doc.Authors {
		name := strings.TrimSpace(strings.Join(append(a.PersName.Forenames, a.PersName.Surname), " "))
		if name != "" {
			authors = append(authors, name)
		}
	}
	header.Author = strings.Join(authors, ", ")

	paragraphs := doc.AbstractDivP
	if len(paragraphs) == 0 {
		paragraphs = doc.AbstractP
	}
	var cleaned []string
	for _, p := range paragraphs {
		if p = strings.TrimSpace(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	header.Abstract = strings.Join(cleaned, "\n\n")

	return header, nil
}

// firstNonEmpty returns the first trimmed non-empty string in values.
func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// headerYear picks a publication year from the available TEI dates, preferring
// one explicitly marked as published.
func headerYear(dates []teiDate) int {
	when := ""
	for _, d := range dates {
		if d.When == "" {
			continue
		}
		if strings.EqualFold(d.Type, "published") {
			when = d.When
			break
		}
		if when == "" {
			when = d.When
		}
	}
	if len(when) >= 4 {
		if year, err := strconv.Atoi(when[:4]); err == nil {
			return year
		}
	}
	return 0
}

// TEI structures we care about within each <biblStruct>.
type teiIdno struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type teiNote struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type teiBiblStruct struct {
	AnalyticTitle []string  `xml:"analytic>title"`
	MonogrTitle   []string  `xml:"monogr>title"`
	AnalyticIdno  []teiIdno `xml:"analytic>idno"`
	MonogrIdno    []teiIdno `xml:"monogr>idno"`
	Notes         []teiNote `xml:"note"`
}

// teiRef is an in-text reference marker, e.g. <ref type="bibr" target="#b2">.
type teiRef struct {
	Type   string `xml:"type,attr"`
	Target string `xml:"target,attr"`
	Coords string `xml:"coords,attr"`
}

// teiSentence is a segmented sentence in the body. InnerXML preserves its full
// text (including the marker text inside child elements), and Refs gives the
// in-text reference markers it contains.
type teiSentence struct {
	Coords   string   `xml:"coords,attr"`
	InnerXML string   `xml:",innerxml"`
	Refs     []teiRef `xml:"ref"`
}

// parseFulltext streams a TEI full-text document. It collects the bibliography
// entries (<biblStruct> with an xml:id) and, from the body sentences (<s>), the
// in-text citation occurrences, which it attaches to the matching reference by
// the marker's target id.
func parseFulltext(r io.Reader) ([]Reference, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false

	var (
		refs    []*Reference
		refIDs  []string                 // parallel to refs, the biblStruct xml:id
		occByID = map[string][]Occurrence{} // target id -> occurrences
		number  int
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "s":
			var s teiSentence
			if err := dec.DecodeElement(&s, &start); err != nil {
				return nil, err
			}
			sentence := plainText(s.InnerXML)
			for _, rf := range s.Refs {
				if !strings.EqualFold(rf.Type, "bibr") {
					continue
				}
				id := strings.TrimPrefix(rf.Target, "#")
				if id == "" {
					continue
				}
				page := coordPage(rf.Coords)
				if page == 0 {
					page = coordPage(s.Coords)
				}
				occByID[id] = append(occByID[id], Occurrence{Text: sentence, Page: page})
			}

		case "biblStruct":
			// Only bibliography entries carry an xml:id; the document's own
			// biblStruct (in the header) does not, so skip it.
			id := attrValue(start, "id")
			if id == "" {
				dec.Skip()
				continue
			}

			var bs teiBiblStruct
			if err := dec.DecodeElement(&bs, &start); err != nil {
				return nil, err
			}

			number++
			ref := &Reference{
				Number: number,
				Title:  firstNonEmpty(append(bs.AnalyticTitle, bs.MonogrTitle...)),
			}
			for _, idno := range append(bs.AnalyticIdno, bs.MonogrIdno...) {
				if strings.EqualFold(idno.Type, "DOI") {
					ref.DOI = strings.TrimSpace(idno.Value)
					break
				}
			}
			for _, note := range bs.Notes {
				if note.Type == "raw_reference" {
					ref.Text = strings.TrimSpace(note.Value)
					break
				}
			}
			refs = append(refs, ref)
			refIDs = append(refIDs, id)
		}
	}

	// The body precedes the bibliography, so occurrences are attached only now
	// that every reference is known.
	result := make([]Reference, len(refs))
	for i, ref := range refs {
		ref.Occurrences = occByID[refIDs[i]]
		result[i] = *ref
	}
	return result, nil
}

// attrValue returns the value of the named attribute (ignoring namespace), or
// "" if absent.
func attrValue(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// plainText extracts the character data from a fragment of TEI XML, collapsing
// runs of whitespace into single spaces.
func plainText(innerXML string) string {
	dec := xml.NewDecoder(strings.NewReader("<x>" + innerXML + "</x>"))
	dec.Strict = false

	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if cd, ok := tok.(xml.CharData); ok {
			b.Write(cd)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// coordPage returns the page number from a TEI coords attribute, whose first
// field is the page (e.g. "3,72.0,107.4,200.6,12.0"). Returns 0 if unavailable.
func coordPage(coords string) int {
	coords = strings.TrimSpace(coords)
	if coords == "" {
		return 0
	}
	if i := strings.IndexByte(coords, ';'); i >= 0 {
		coords = coords[:i] // first bounding box only
	}
	fields := strings.Split(coords, ",")
	page, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return 0
	}
	return page
}
