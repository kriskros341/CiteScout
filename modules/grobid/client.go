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

// ProcessReferences sends a PDF to GROBID's /api/processReferences endpoint and
// returns the parsed references. includeRawCitations is requested so each
// reference carries its original text.
func (c *Client) ProcessReferences(ctx context.Context, pdf io.Reader) ([]Reference, error) {
	fields := map[string]string{"includeRawCitations": "1"}
	if c.Consolidate {
		fields["consolidateCitations"] = "1"
	}

	respBody, err := c.postPDF(ctx, "/api/processReferences", pdf, fields)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	return parseReferences(respBody)
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

// parseReferences streams the TEI XML response and decodes each <biblStruct>
// into a Reference. Nested biblStruct elements are consumed by DecodeElement,
// so they are not counted twice.
func parseReferences(r io.Reader) ([]Reference, error) {
	dec := xml.NewDecoder(r)

	var refs []Reference
	number := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "biblStruct" {
			continue
		}

		var bs teiBiblStruct
		if err := dec.DecodeElement(&bs, &start); err != nil {
			return nil, err
		}

		number++
		ref := Reference{Number: number}

		ref.Title = firstNonEmpty(append(bs.AnalyticTitle, bs.MonogrTitle...))

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
	}

	return refs, nil
}
