package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"citescout/modules/doi"
	"citescout/modules/grobid"
)

// maxUploadMemory is how much of a multipart upload is buffered in memory; the
// rest spills to temporary files. The PDF payload itself can be larger.
const maxUploadMemory = 32 << 20 // 32 MiB

// paperAnalyzer extracts a paper's header metadata and its bibliographic
// references from a PDF. It is satisfied by *grobid.Client.
type paperAnalyzer interface {
	ProcessHeader(ctx context.Context, pdf io.Reader) (grobid.Header, error)
	ProcessReferences(ctx context.Context, pdf io.Reader) ([]grobid.Reference, error)
}

// DOIResolver finds the DOI for a work given its title. It is satisfied by
// *doi.Client.
type DOIResolver interface {
	FindDOI(ctx context.Context, query doi.Query) (string, error)
}

// PaperArchiveHandlers exposes the scientific paper archive over HTTP. It holds
// only request/response logic and delegates persistence to the PaperArchive,
// metadata/citation extraction to GROBID, and background DOI lookups to a queue.
type PaperArchiveHandlers struct {
	archive  *PaperArchive
	grobid   paperAnalyzer
	doiQueue *DOIQueue
}

// NewPaperArchiveHandlers wires HTTP handlers to the given archive. grobid may
// be nil (no metadata/citation extraction on upload) and doiQueue may be nil
// (DOI lookup disabled).
func NewPaperArchiveHandlers(archive *PaperArchive, grobid paperAnalyzer, doiQueue *DOIQueue) *PaperArchiveHandlers {
	return &PaperArchiveHandlers{archive: archive, grobid: grobid, doiQueue: doiQueue}
}

// Register wires the archive's routes onto the given mux:
//
//	GET    /                             redirect to the paper list
//	GET    /new                          HTML upload form
//	GET    /papers/                      HTML list of papers (with add button)
//	GET    /papers/{id}                  HTML page: metadata + citations
//
//	GET    /api/papers/                  list papers (JSON)
//	POST   /api/papers/                  upload a paper (multipart/form-data)
//	GET    /api/papers/{id}              paper metadata (JSON)
//	GET    /api/papers/{id}/download     download the PDF (attachment)
//	GET    /api/papers/{id}/view         view the PDF inline (in the browser)
//	GET    /api/papers/{id}/citations    citations extracted from the paper (JSON)
//	PUT    /api/papers/{id}              update metadata (JSON)
//	DELETE /api/papers/{id}              remove the paper and its PDF
//
//	POST   /api/citations/{id}/resolve-doi  look up this citation's DOI (web search)
//	POST   /api/citations/{id}/doi          set (field "doi") or remove (blank) the DOI manually
func (h *PaperArchiveHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.home)
	mux.HandleFunc("/new", h.uploadForm)
	mux.HandleFunc("/papers/", h.paperPage)
	mux.HandleFunc("/api/papers/", h.api)
	mux.HandleFunc("/api/citations/", h.citationAPI)
}

// home redirects the site root to the paper list and 404s anything else that
// falls through to the catch-all pattern.
func (h *PaperArchiveHandlers) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/papers/", http.StatusSeeOther)
}

// api routes the JSON API under /api/papers/.
func (h *PaperArchiveHandlers) api(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/papers/")

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			h.listPapers(w)
		case http.MethodPost:
			h.createPaper(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Remaining routes are /api/papers/{id} and /api/papers/{id}/{action}.
	parts := strings.Split(rest, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "Invalid paper ID", http.StatusBadRequest)
		return
	}

	if len(parts) == 2 {
		switch parts[1] {
		case "download":
			h.requireGet(w, r, func() { h.servePDF(w, r, id, "attachment") })
		case "view":
			h.requireGet(w, r, func() { h.servePDF(w, r, id, "inline") })
		case "citations":
			h.requireGet(w, r, func() { h.listCitations(w, id) })
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getPaper(w, id)
	case http.MethodPut:
		h.updatePaper(w, r, id)
	case http.MethodDelete:
		h.deletePaper(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// uploadForm serves the HTML form for submitting a paper's PDF.
func (h *PaperArchiveHandlers) uploadForm(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/new" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(uploadFormHTML))
}

// listPage renders the HTML index of all papers, with a link to add one.
func (h *PaperArchiveHandlers) listPage(w http.ResponseWriter) {
	papers, err := h.archive.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paperListTemplate.Execute(w, papers); err != nil {
		log.Printf("rendering paper list: %v", err)
	}
}

// paperPage renders an HTML page for a single paper: its metadata and the list
// of citations extracted from it.
func (h *PaperArchiveHandlers) paperPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/papers/"), "/")
	if idStr == "" {
		h.listPage(w)
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid paper ID", http.StatusBadRequest)
		return
	}

	paper, err := h.archive.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Paper not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	citations, err := h.archive.CitationsForPaper(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build per-citation view models carrying the live DOI-lookup status.
	views := make([]citationView, len(citations))
	for i, c := range citations {
		views[i] = h.newCitationView(c)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paperPageTemplate.Execute(w, struct {
		Paper     Paper
		Citations []citationView
	}{
		Paper:     paper,
		Citations: views,
	}); err != nil {
		log.Printf("rendering paper page %d: %v", id, err)
	}
}

// citationView decorates a Citation with its live DOI-lookup status for
// rendering. It is self-contained so the DOI cell can be rendered both inline
// and as a standalone HTMX fragment.
type citationView struct {
	Citation
	DOIStatus    string
	DOIPending   bool
	DOIAvailable bool
}

// newCitationView builds a view for a citation using the queue's current status.
func (h *PaperArchiveHandlers) newCitationView(c Citation) citationView {
	status := DOIStatusIdle
	if h.doiQueue != nil {
		status = h.doiQueue.Status(c.ID)
	}
	return citationView{
		Citation:     c,
		DOIStatus:    status,
		DOIPending:   status == DOIStatusQueued || status == DOIStatusRunning,
		DOIAvailable: h.doiQueue != nil,
	}
}

// renderDOICell writes the HTMX fragment for a single citation's DOI cell.
func (h *PaperArchiveHandlers) renderDOICell(w http.ResponseWriter, citationID int) {
	c, err := h.archive.GetCitation(citationID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Citation not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paperPageTemplate.ExecuteTemplate(w, "doiCell", h.newCitationView(c)); err != nil {
		log.Printf("rendering doi cell %d: %v", citationID, err)
	}
}

func (h *PaperArchiveHandlers) listPapers(w http.ResponseWriter) {
	papers, err := h.archive.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, papers)
}

func (h *PaperArchiveHandlers) getPaper(w http.ResponseWriter, id int) {
	paper, err := h.archive.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Paper not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, paper)
}

// servePDF streams the stored PDF for a paper. disposition is "inline" to view
// it in the browser or "attachment" to download it.
func (h *PaperArchiveHandlers) servePDF(w http.ResponseWriter, r *http.Request, id int, disposition string) {
	paper, err := h.archive.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Paper not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if paper.Filename == "" {
		http.Error(w, "Paper has no PDF on file", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, paper.Filename))
	http.ServeFile(w, r, h.archive.PDFPath(paper.Filename))
}

// createPaper accepts a multipart/form-data submission carrying either a PDF
// file (field "pdf") or a link to one (field "url"). Before anything is written
// to disk, GROBID analyses the PDF to fill in the paper's metadata; the PDF and
// metadata are then archived together and the citations extracted.
func (h *PaperArchiveHandlers) createPaper(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		http.Error(w, "Could not parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// The PDF comes from an uploaded file or a URL we fetch server-side.
	var pdf io.ReadSeeker
	if file, _, err := r.FormFile("pdf"); err == nil {
		defer file.Close()
		pdf = file
	} else if rawURL := strings.TrimSpace(r.FormValue("url")); rawURL != "" {
		data, err := downloadPDF(rawURL)
		if err != nil {
			http.Error(w, "Could not fetch PDF: "+err.Error(), http.StatusBadRequest)
			return
		}
		pdf = bytes.NewReader(data)
	} else {
		http.Error(w, "Provide a PDF file (field \"pdf\") or a URL (field \"url\")", http.StatusBadRequest)
		return
	}

	if !isPDF(pdf) {
		http.Error(w, "The provided data is not a PDF", http.StatusBadRequest)
		return
	}

	// Analyse the PDF for its metadata before persisting anything.
	paper := h.analyzeHeader(pdf)

	// Rewind so the same PDF can be written to disk by the archive.
	if _, err := pdf.Seek(0, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	created, err := h.archive.Create(paper, pdf)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Extract and store citations from the PDF (best-effort: a GROBID outage
	// should not fail the upload — the paper is already archived).
	h.extractCitations(created)

	// Send the browser to the freshly saved paper so the extracted metadata is
	// visible; API clients can use the same Location header.
	w.Header().Set("Location", "/papers/"+strconv.Itoa(created.ID))
	http.Redirect(w, r, "/papers/"+strconv.Itoa(created.ID), http.StatusSeeOther)
}

// maxPDFBytes caps how much we download from a URL.
const maxPDFBytes = 64 << 20 // 64 MiB

// isPDF reports whether the stream starts with the PDF magic bytes, rewinding
// it afterwards so the content can be read again.
func isPDF(rs io.ReadSeeker) bool {
	head := make([]byte, 5)
	n, _ := io.ReadFull(rs, head)
	rs.Seek(0, io.SeekStart)
	return n == 5 && string(head) == "%PDF-"
}

// downloadPDF fetches a PDF from a URL (size-limited). Note: this performs a
// server-side request to a user-supplied URL — fine for local use, but exposes
// SSRF surface if the server is ever public.
func downloadPDF(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPDFBytes))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// analyzeHeader asks GROBID for a paper's metadata. Extraction failures are
// logged and yield an empty Paper rather than aborting the upload.
func (h *PaperArchiveHandlers) analyzeHeader(pdf io.Reader) Paper {
	if h.grobid == nil {
		return Paper{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	header, err := h.grobid.ProcessHeader(ctx, pdf)
	if err != nil {
		log.Printf("metadata: extracting header: %v", err)
		return Paper{}
	}
	return Paper{
		Title:    header.Title,
		Author:   header.Author,
		Year:     header.Year,
		Abstract: header.Abstract,
		DOI:      header.DOI,
	}
}

// extractCitations runs GROBID over a paper's PDF and stores the resulting
// citations. Failures are logged, not returned to the client.
func (h *PaperArchiveHandlers) extractCitations(paper Paper) {
	if h.grobid == nil {
		return
	}

	pdf, err := h.archive.OpenPDF(paper.Filename)
	if err != nil {
		log.Printf("citations: opening PDF for paper %d: %v", paper.ID, err)
		return
	}
	defer pdf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	refs, err := h.grobid.ProcessReferences(ctx, pdf)
	if err != nil {
		log.Printf("citations: extracting for paper %d: %v", paper.ID, err)
		return
	}

	citations := make([]Citation, 0, len(refs))
	for _, ref := range refs {
		paperID := paper.ID
		occurrences := make([]Occurrence, 0, len(ref.Occurrences))
		for _, o := range ref.Occurrences {
			occurrences = append(occurrences, Occurrence{Text: o.Text, Page: o.Page})
		}
		citations = append(citations, Citation{
			PaperID:     &paperID,
			Number:      ref.Number,
			Title:       ref.Title,
			Text:        ref.Text,
			DOI:         ref.DOI,
			Occurrences: occurrences,
		})
	}

	if err := h.archive.ReplaceCitations(paper.ID, citations); err != nil {
		log.Printf("citations: saving for paper %d: %v", paper.ID, err)
		return
	}
	log.Printf("citations: saved %d for paper %d", len(citations), paper.ID)
}

// listCitations returns the citations extracted from a paper.
func (h *PaperArchiveHandlers) listCitations(w http.ResponseWriter, id int) {
	citations, err := h.archive.CitationsForPaper(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, citations)
}

// requireGet runs handle only for GET requests, otherwise replies 405.
func (h *PaperArchiveHandlers) requireGet(w http.ResponseWriter, r *http.Request, handle func()) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	handle()
}

// citationAPI routes the citation endpoints under /api/citations/.
func (h *PaperArchiveHandlers) citationAPI(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/citations/")
	parts := strings.Split(rest, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "Invalid citation ID", http.StatusBadRequest)
		return
	}

	if len(parts) == 2 {
		switch parts[1] {
		case "resolve-doi":
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.resolveCitationDOI(w, r, id)
		case "doi":
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.setCitationDOI(w, r, id)
		case "doi-cell":
			h.requireGet(w, r, func() { h.renderDOICell(w, id) })
		default:
			http.NotFound(w, r)
		}
		return
	}
	http.NotFound(w, r)
}

// resolveCitationDOI enqueues a background DOI lookup for a single citation,
// then redirects back to its paper page where the status is shown.
func (h *PaperArchiveHandlers) resolveCitationDOI(w http.ResponseWriter, r *http.Request, citationID int) {
	if h.doiQueue == nil {
		http.Error(w, "DOI lookup is not configured (set GEMINI_API_KEY)", http.StatusServiceUnavailable)
		return
	}

	c, err := h.archive.GetCitation(citationID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Citation not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.doiQueue.Enqueue(citationID)

	// HTMX requests get the updated cell back to swap in place; plain form
	// submissions (no JS) fall back to a redirect to the paper page.
	if r.Header.Get("HX-Request") != "" {
		h.renderDOICell(w, citationID)
		return
	}

	paperID := 0
	if c.PaperID != nil {
		paperID = *c.PaperID
	}
	http.Redirect(w, r, "/papers/"+strconv.Itoa(paperID), http.StatusSeeOther)
}

// setCitationDOI manually sets (or, when blank, removes) a citation's DOI. This
// does not require a resolver, so it works even when DOI lookup is disabled.
func (h *PaperArchiveHandlers) setCitationDOI(w http.ResponseWriter, r *http.Request, citationID int) {
	c, err := h.archive.GetCitation(citationID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Citation not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	value := strings.TrimSpace(r.FormValue("doi"))
	if value != "" && !looksLikeDOI(value) {
		http.Error(w, "Invalid DOI (expected something like 10.1145/3292500)", http.StatusBadRequest)
		return
	}

	if err := h.archive.UpdateCitationDOI(citationID, value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		h.renderDOICell(w, citationID)
		return
	}

	paperID := 0
	if c.PaperID != nil {
		paperID = *c.PaperID
	}
	http.Redirect(w, r, "/papers/"+strconv.Itoa(paperID), http.StatusSeeOther)
}

// looksLikeDOI does a light sanity check on a DOI string: "10." prefix and a
// slash. It guards against obviously bogus manual entries without being strict.
func looksLikeDOI(s string) bool {
	return strings.HasPrefix(s, "10.") && strings.Contains(s, "/")
}

func (h *PaperArchiveHandlers) updatePaper(w http.ResponseWriter, r *http.Request, id int) {
	var updatedPaper Paper
	if err := json.NewDecoder(r.Body).Decode(&updatedPaper); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	found, err := h.archive.Update(id, updatedPaper)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Paper not found", http.StatusNotFound)
		return
	}

	updated, err := h.archive.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *PaperArchiveHandlers) deletePaper(w http.ResponseWriter, r *http.Request, id int) {
	found, err := h.archive.Delete(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Paper not found", http.StatusNotFound)
		return
	}
	// HTMX expects a 2xx with a body to swap (a 204 would be treated as "no
	// swap"); returning empty 200 lets the list row be removed. API clients get
	// the conventional 204.
	if r.Header.Get("HX-Request") != "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON encodes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

const uploadFormHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Archive a scientific paper</title>
</head>
<body>
<p><a href="/papers/">&larr; All papers</a></p>
<h1>Archive a scientific paper</h1>
<p>Add a PDF by uploading a file or giving a link to one. Its title, author,
year and abstract are filled in automatically from a GROBID analysis of the
document.</p>
<form action="/api/papers/" method="post" enctype="multipart/form-data">
	<p><label>PDF file<br><input type="file" name="pdf" accept="application/pdf"></label></p>
	<p>&mdash; or &mdash;</p>
	<p><label>PDF URL<br><input type="url" name="url" size="50" placeholder="https://example.org/paper.pdf"></label></p>
	<p><button type="submit">Add &amp; analyze</button></p>
</form>
</body>
</html>
`

// paperListTemplate renders the index of archived papers. Data is []Paper.
var paperListTemplate = template.Must(template.New("list").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
<title>Scientific paper archive</title>
</head>
<body>
<h1>Scientific paper archive</h1>
<p><a href="/new">+ Add paper</a></p>
{{if .}}<table border="1" cellpadding="4" cellspacing="0">
<thead><tr><th>Title</th><th>Author</th><th>Year</th><th>DOI</th><th></th><th></th></tr></thead>
<tbody>
{{range .}}	<tr>
		<td><a href="/papers/{{.ID}}">{{if .Title}}{{.Title}}{{else}}(untitled #{{.ID}}){{end}}</a></td>
		<td>{{if .Author}}{{.Author}}{{else}}&mdash;{{end}}</td>
		<td>{{if .Year}}{{.Year}}{{else}}&mdash;{{end}}</td>
		<td>{{if .DOI}}<a href="https://doi.org/{{.DOI}}">{{.DOI}}</a>{{else}}&mdash;{{end}}</td>
		<td><a href="/api/papers/{{.ID}}/view" target="_blank">view</a></td>
		<td><button hx-delete="/api/papers/{{.ID}}" hx-confirm="Delete this paper and its PDF?" hx-target="closest tr" hx-swap="outerHTML">delete</button></td>
	</tr>
{{end}}</tbody>
</table>{{else}}<p>No papers yet. <a href="/new">Add one</a>.</p>{{end}}
</body>
</html>
`))

// paperPageTemplate renders a single paper's metadata and its citation list.
var paperPageTemplate = template.Must(template.New("paper").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
<title>{{if .Paper.Title}}{{.Paper.Title}}{{else}}Paper #{{.Paper.ID}}{{end}}</title>
</head>
<body>
<p><a href="/papers/">&larr; All papers</a></p>
<h1>{{if .Paper.Title}}{{.Paper.Title}}{{else}}(untitled){{end}}</h1>
<dl>
	<dt>Author</dt><dd>{{if .Paper.Author}}{{.Paper.Author}}{{else}}&mdash;{{end}}</dd>
	<dt>Year</dt><dd>{{if .Paper.Year}}{{.Paper.Year}}{{else}}&mdash;{{end}}</dd>
	<dt>DOI</dt><dd>{{if .Paper.DOI}}<a href="https://doi.org/{{.Paper.DOI}}">{{.Paper.DOI}}</a>{{else}}&mdash;{{end}}</dd>
	<dt>PDF</dt><dd><a href="/api/papers/{{.Paper.ID}}/view" target="_blank">view</a> &middot; <a href="/api/papers/{{.Paper.ID}}/download">download</a></dd>
</dl>
{{if .Paper.Abstract}}<h2>Abstract</h2>
<p>{{.Paper.Abstract}}</p>{{end}}
<h2>Citations ({{len .Citations}})</h2>
{{if .Citations}}<table border="1" cellpadding="4" cellspacing="0">
<thead><tr><th>No</th><th>DOI</th><th>Appears on</th><th>Title</th></tr></thead>
<tbody>
{{range .Citations}}	<tr>
		<td>{{.Number}}</td>
		{{template "doiCell" .}}
		<td>{{if .Occurrences}}<ul>
{{range .Occurrences}}			<li>{{.Text}}{{if .Page}} (p.{{.Page}}){{end}}</li>
{{end}}		</ul>{{else}}&mdash;{{end}}</td>
		<td>{{if .Text}}{{.Text}}{{else}}&mdash;{{end}}</td>
	</tr>
{{end}}</tbody>
</table>{{else}}<p>No citations extracted.</p>{{end}}
</body>
</html>
`))

// doiCell is the DOI table cell, rendered both inline and as a standalone HTMX
// fragment. While a lookup is pending it polls itself every 2s (swapping its own
// outerHTML) until the queue finishes, so only this cell updates — no full-page
// refresh. The "find" button runs the web-search lookup; a DOI can also be set
// manually or removed (e.g. when the model returns a nonsensical one).
// The {{- -}} markers trim template whitespace so the fragment stays tidy.
var _ = template.Must(paperPageTemplate.Parse(`
{{- define "doiCell" -}}
<td id="doi-cell-{{.ID}}"{{if .DOIPending}} hx-get="/api/citations/{{.ID}}/doi-cell" hx-trigger="load delay:2s" hx-swap="outerHTML"{{end}}>
	{{- if .DOIPending}}
	{{.DOIStatus}}&hellip;
	{{- else if .DOI}}
	<a href="https://doi.org/{{.DOI}}">{{.DOI}}</a>
	<form hx-post="/api/citations/{{.ID}}/doi" hx-target="#doi-cell-{{.ID}}" hx-swap="outerHTML" style="display:inline">
		<input type="text" name="doi" value="{{.DOI}}" size="18">
		<button type="submit">save</button>
	</form>
	<form hx-post="/api/citations/{{.ID}}/doi" hx-target="#doi-cell-{{.ID}}" hx-swap="outerHTML" style="display:inline">
		<input type="hidden" name="doi" value="">
		<button type="submit">remove</button>
	</form>
	{{- else}}
		{{- if and .DOIAvailable .Title}}
	<button hx-post="/api/citations/{{.ID}}/resolve-doi" hx-target="#doi-cell-{{.ID}}" hx-swap="outerHTML">find</button>
			{{- if eq .DOIStatus "done"}} <small>not found</small>
			{{- else if eq .DOIStatus "error"}} <small>error</small>
			{{- end}}
		{{- end}}
	<form hx-post="/api/citations/{{.ID}}/doi" hx-target="#doi-cell-{{.ID}}" hx-swap="outerHTML" style="display:inline">
		<input type="text" name="doi" placeholder="10.xxxx/..." size="18">
		<button type="submit">set</button>
	</form>
	{{- end}}
</td>
{{- end}}`))
