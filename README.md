The goal:

I want to be able to add papers of interest into the database and have them saved as files on my gdrive

Papers -> Citations

The RestAPI encodes the workflow

I had the task from my supervisor.
`
find related works. you can check papers that i cite in my work, and papers that cite my work.

workflow is:
- I check two of his papers most related to my current work,
- I find references that interest me. I read one paper a day.

This tool helps me find, download and save notes on the papers easier.
Notes are saved to a linked google drive mount folder 

## Site map

Pages (HTML):

- `GET /` — redirect to the paper list
- `GET /papers/` — list of archived papers (with the **add** button)
- `GET /new` — upload form (add a paper by file or by URL)
- `GET /papers/{id}` — paper page: metadata + citations table (DOI / appears-on / title)

API (JSON unless noted):

- `GET /api/papers/` — list papers
- `POST /api/papers/` — add a paper (multipart: `pdf` file **or** `url`)
- `GET /api/papers/{id}` — paper metadata
- `PUT /api/papers/{id}` — update metadata
- `DELETE /api/papers/{id}` — remove the paper and its PDF
- `GET /api/papers/{id}/view` — open the PDF inline (in the browser)
- `GET /api/papers/{id}/download` — download the PDF
- `GET /api/papers/{id}/citations` — citations extracted from the paper
- `POST /api/citations/{id}/resolve-doi` — look up this citation's DOI (Gemini web search)
- `POST /api/citations/{id}/doi` — set (`doi` field) or remove (blank) the DOI manually
- `GET /api/citations/{id}/doi-cell` — HTMX fragment for the DOI cell (status polling)

## TODO

- [ ] Migracja nazw istniejących plików na schemat z DOI (jednorazowy skrypt; nowe uploady już używają DOI w nazwie)
- [ ] Dociąganie DOI samej pracy po tytule (CrossRef/OpenAlex), gdy GROBID go nie znajdzie (typowe dla arXiv) — poprawia nazewnictwo i napędza discovery
- [ ] Walidacja DOI: sprawdzenie, czy DOI faktycznie się rozwiązuje (oznaczanie/odrzucanie „bezsensownych")
- [ ] Deduplikacja przy dodawaniu (po DOI / podobieństwie tytułu / hashu pliku)
- [ ] Lepsze rozwiązywanie DOI: `mailto` do CrossRef („polite pool") lub biblio-glutton zamiast throttlowanego zapytania
- [ ] Status kolejki DOI jest w pamięci — ginie po restarcie (ewentualnie utrwalić)

## Realnie ważne TODO

- [ ] Testy funkcjonalne handlerów HTTP (`httptest`) + plan testów (wymaganie → przypadek)
- [ ] Deduplikacja przy dodawaniu (po DOI / podobieństwie tytułu / hashu pliku)

## Kierunki rozwoju (discovery)

Główny cel narzędzia: **odnajdywanie related works pod własną pracę** — nie notatki
(te robione są poza aplikacją). Stąd rozwój idzie w stronę odkrywania:

- **„Luki w bibliotece"** — agregacja cytowań ze wszystkich prac i ranking najczęściej
  cytowanych dzieł, których jeszcze nie ma w archiwum → lista „warto dodać" z dodawaniem po DOI.
- **Powiązane prace (bibliographic coupling / co-citation)** — podobieństwo prac liczone po
  wspólnych cytowaniach; widok „powiązane w mojej bibliotece".
- **Odkrywanie „w przód"** — przy cytowaniach z DOI jeden klik „dodaj cytowaną pracę"
  (pobranie po DOI: Unpaywall / arXiv / OpenAlex).
- **Graf cytowań między pracami w archiwum** — „praca A cytuje pracę B" (obie u mnie).
- **(Opcjonalnie) pełnotekstowe wyszukiwanie** w treści PDF (SQLite FTS5).

Część „akademicka" pod efekty przedmiotu (gdy będzie potrzebna): dokument projektowy z analizą
i wyborem technologii, model obiektowy + diagram UML, plan testów funkcjonalnych.

