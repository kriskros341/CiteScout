package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"restapi/modules/grobid"
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

// PaperArchiveHandlers exposes the scientific paper archive over HTTP. It holds
// only request/response logic and delegates persistence to the PaperArchive and
// metadata/citation extraction to GROBID.
type PaperArchiveHandlers struct {
	archive *PaperArchive
	grobid  paperAnalyzer
}

// NewPaperArchiveHandlers wires HTTP handlers to the given archive. grobid may
// be nil, in which case metadata and citations are not extracted on upload.
func NewPaperArchiveHandlers(archive *PaperArchive, grobid paperAnalyzer) *PaperArchiveHandlers {
	return &PaperArchiveHandlers{archive: archive, grobid: grobid}
}

// Register wires the archive's routes onto the given mux:
//
//	GET    /new                          HTML upload form
//	GET    /papers/{id}                  HTML page: metadata + citations
//
//	GET    /api/papers/                  list papers (JSON)
//	POST   /api/papers/                  upload a paper (multipart/form-data)
//	GET    /api/papers/{id}              paper metadata (JSON)
//	GET    /api/papers/{id}/download     download the PDF
//	GET    /api/papers/{id}/citations    citations extracted from the paper (JSON)
//	PUT    /api/papers/{id}              update metadata (JSON)
//	DELETE /api/papers/{id}              remove the paper and its PDF
func (h *PaperArchiveHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/new", h.uploadForm)
	mux.HandleFunc("/papers/", h.paperPage)
	mux.HandleFunc("/api/papers/", h.api)
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
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch parts[1] {
		case "download":
			h.downloadPaper(w, r, id)
		case "citations":
			h.listCitations(w, id)
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
		h.deletePaper(w, id)
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

// paperPage renders an HTML page for a single paper: its metadata and the list
// of citations extracted from it.
func (h *PaperArchiveHandlers) paperPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/papers/")
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paperPageTemplate.Execute(w, struct {
		Paper     Paper
		Citations []Citation
	}{paper, citations}); err != nil {
		log.Printf("rendering paper page %d: %v", id, err)
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

// downloadPaper streams the stored PDF for a paper.
func (h *PaperArchiveHandlers) downloadPaper(w http.ResponseWriter, r *http.Request, id int) {
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
	http.ServeFile(w, r, h.archive.PDFPath(paper.Filename))
}

// createPaper accepts a multipart/form-data submission carrying only a PDF
// file. Before anything is written to disk, GROBID analyses the PDF to fill in
// the paper's metadata; the PDF and metadata are then archived together and the
// citations extracted.
func (h *PaperArchiveHandlers) createPaper(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		http.Error(w, "Could not parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("pdf")
	if err != nil {
		http.Error(w, "A PDF file is required (form field \"pdf\")", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if !strings.EqualFold(filepath.Ext(header.Filename), ".pdf") {
		http.Error(w, "Only PDF files are accepted", http.StatusBadRequest)
		return
	}

	// Analyse the uploaded PDF for its metadata before persisting anything.
	paper := h.analyzeHeader(file)

	// Rewind so the same upload can be written to disk by the archive.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	created, err := h.archive.Create(paper, file)
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
		citations = append(citations, Citation{
			PaperID: &paperID,
			Number:  ref.Number,
			Title:   ref.Title,
			Text:    ref.Text,
			DOI:     ref.DOI,
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

func (h *PaperArchiveHandlers) deletePaper(w http.ResponseWriter, id int) {
	found, err := h.archive.Delete(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Paper not found", http.StatusNotFound)
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
<h1>Archive a scientific paper</h1>
<p>Upload a PDF. Its title, author, year and abstract are filled in
automatically from a GROBID analysis of the document.</p>
<form action="/api/papers/" method="post" enctype="multipart/form-data">
	<p><label>PDF file<br><input type="file" name="pdf" accept="application/pdf" required></label></p>
	<p><button type="submit">Upload &amp; analyze</button></p>
</form>
</body>
</html>
`

// paperPageTemplate renders a single paper's metadata and its citation list.
var paperPageTemplate = template.Must(template.New("paper").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{if .Paper.Title}}{{.Paper.Title}}{{else}}Paper #{{.Paper.ID}}{{end}}</title>
</head>
<body>
<p><a href="/new">&larr; Upload another paper</a></p>
<h1>{{if .Paper.Title}}{{.Paper.Title}}{{else}}(untitled){{end}}</h1>
<dl>
	<dt>Author</dt><dd>{{if .Paper.Author}}{{.Paper.Author}}{{else}}&mdash;{{end}}</dd>
	<dt>Year</dt><dd>{{if .Paper.Year}}{{.Paper.Year}}{{else}}&mdash;{{end}}</dd>
	<dt>DOI</dt><dd>{{if .Paper.DOI}}<a href="https://doi.org/{{.Paper.DOI}}">{{.Paper.DOI}}</a>{{else}}&mdash;{{end}}</dd>
	<dt>PDF</dt><dd><a href="/api/papers/{{.Paper.ID}}/download">download</a></dd>
</dl>
{{if .Paper.Abstract}}<h2>Abstract</h2>
<p>{{.Paper.Abstract}}</p>{{end}}
<h2>Citations ({{len .Citations}})</h2>
{{if .Citations}}<table border="1" cellpadding="4" cellspacing="0">
<thead><tr><th>No</th><th>DOI</th><th>Title</th><th>Text</th></tr></thead>
<tbody>
{{range .Citations}}	<tr>
		<td>{{.Number}}</td>
		<td>{{if .DOI}}<a href="https://doi.org/{{.DOI}}">{{.DOI}}</a>{{else}}&mdash;{{end}}</td>
		<td>{{if .Title}}{{.Title}}{{else}}&mdash;{{end}}</td>
		<td>{{if .Text}}{{.Text}}{{else}}&mdash;{{end}}</td>
	</tr>
{{end}}</tbody>
</table>{{else}}<p>No citations extracted.</p>{{end}}
</body>
</html>
`))
