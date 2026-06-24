# CiteScout

Discover and aggregate related works by following the citations of papers you care about.

The goal:

I want to be able to add papers of interest into the database and have them saved as files on my mounted gdrive

The RestAPI encodes the workflow.

I had the task from my supervisor. "find related works. you can check papers that i cite in my work, and papers that cite my work."

workflow is:
- I check two of his papers most related to my current work,
- I find references that interest me.

## Site map

Pages (HTML):

- `GET /` — redirect to the paper list
- `GET /papers/` — list of archived papers (with the **add** button); filter with
  `?q=` (title/abstract/reference text), `?author=` and `?tag=`
- `GET /new` — upload form (add a paper by file or by URL)
- `GET /papers/{id}` — paper page: metadata, tags, "cited by", citations table
- `GET /login`, `GET /logout` — sign in / out (only when auth is enabled)

API (JSON unless noted):

- `GET /api/papers/` — list papers; accepts the `q`/`author`/`tag` filter params
- `POST /api/papers/` — add a paper (multipart: `pdf` file **or** `url`)
- `GET /api/papers/{id}` — paper metadata
- `PUT /api/papers/{id}` — update metadata
- `DELETE /api/papers/{id}` — remove the paper and its PDF
- `GET /api/papers/{id}/view` — open the PDF inline (in the browser)
- `GET /api/papers/{id}/download` — download the PDF
- `GET /api/papers/{id}/citations` — citations extracted from the paper
- `POST /api/papers/{id}/tags` — replace a paper's tags (comma-separated `tags` field)
- `GET /api/papers/{id}/citing-works` — works that cite this paper (OpenAlex, on demand)
- `POST /api/citations/{id}/resolve-doi` — look up this citation's DOI (Gemini web search)
- `POST /api/citations/{id}/doi` — set (`doi` field) or remove (blank) the DOI manually
- `GET /api/citations/{id}/doi-cell` — HTMX fragment for the DOI cell (status polling)

## Configuration (env)

- `PAPERS_DIR`, `GROBID_URL`, `GROBID_CONSOLIDATE` — storage dir and GROBID server
- `GEMINI_API_KEY`, `GEMINI_MODEL` — enable citation DOI lookup (web search)
- `OPENALEX_MAILTO` — contact email for OpenAlex's polite pool ("cited by" discovery)
- `AUTH_PASSWORD_HASH` — SHA-256 hex of the login password; when set, the site
  requires sign-in. Generate it with `make hash-password PASSWORD=secret`.

## Scripts

- `make migrate-filenames` — rename existing stored PDFs to the DOI-based scheme
  (preview with `DRY_RUN=1`, override `DB=`/`PAPERS_DIR=`). Requires the `sqlite3` CLI.
- `make test` — run the test suite.

## TODO

- [x] Migrate existing filenames to the DOI-based scheme (`make migrate-filenames`)
- [ ] Better DOI resolution: `mailto` to CrossRef ("polite pool") or biblio-glutton instead of a throttled query
- [ ] DOI queue status is in memory — lost on restart (optionally persist it)
- [x] Deduplicate uploads by PDF content hash (SHA-256) — an identical file redirects to the existing paper instead of creating a duplicate
- [x] Paper tagging + list filtering by author / tag / content (title, abstract, reference text)
- [x] "Cited by" (who cites a given paper) — OpenAlex, fetched on demand
- [x] Simple auth: password login (SHA-256 in `AUTH_PASSWORD_HASH`), session cookie, STDLIB only
- [x] Harden `downloadPDF` against SSRF (http/https only, block non-public addresses, validate redirects)

## Really important TODO

- [x] Functional HTTP handler tests (`httptest`) — upload, dedup, tags/filtering, "cited by", auth, SSRF
- [ ] Deduplicate uploads (by DOI / title similarity) — file hash already done

## Roadmap (discovery)

The tool's main goal: **finding related works for your own paper** — not note-taking
(that happens outside the app). So development leans toward discovery:

- **"Gaps in the library"** — aggregate citations across all papers and rank the most
  frequently cited works not yet in the archive → a "worth adding" list with add-by-DOI.
- **Related papers (bibliographic coupling / co-citation)** — paper similarity computed from
  shared citations; a "related in my library" view.
- **Forward discovery** — for citations with a DOI, one-click "add the cited work"
  (fetch by DOI: Unpaywall / arXiv / OpenAlex).
- **"Cited by" (forward citations)** — for a paper with a DOI, show the works that cite it, with a
  one-click "add". This directly fulfils the supervisor's task ("papers that cite mine").
  API: **OpenAlex** (no key, `mailto` for the polite pool; a work record gives `cited_by_api_url`
  and `referenced_works`, so one source covers both directions), with a fallback to the
  **Semantic Scholar Graph API** (`/paper/DOI:.../citations`) or **OpenCitations** (DOI→DOI).
  CrossRef does not openly expose the list of citing works, and Google Scholar has no API.
- **OpenAlex client (`modules/openalex`)** as a shared backbone — could replace/supplement the
  throttled title-based DOI lookup and power both "cited by" and forward discovery.
- **Citation graph between archived papers** — "paper A cites paper B" (both in my library).
- **(Optional) full-text search** over PDF content (SQLite FTS5).

The "academic" part for the course deliverables (when needed): a design document with analysis
and technology choice, an object model + UML diagram, and a functional test plan.

