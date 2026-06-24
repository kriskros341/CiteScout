#set document(title: "CiteScout — raport projektowy", author: "Krzysztof Czuba")
#set page(
  paper: "a4",
  margin: (x: 2.5cm, y: 2.5cm),
  numbering: "1",
)
#set text(lang: "pl", size: 11pt, font: "New Computer Modern")
#set par(justify: true, leading: 0.65em)
#set heading(numbering: "1.1")

#show heading.where(level: 1): it => [
  #v(0.4em)
  #block(text(size: 15pt, weight: "bold", it))
  #v(0.2em)
]

// Styl bloków kodu / tabel
#show raw.where(block: true): it => block(
  width: 100%,
  fill: luma(245),
  inset: 8pt,
  radius: 3pt,
  text(size: 9pt, it),
)

#align(center)[
  #v(3cm)
  #text(size: 26pt, weight: "bold")[CiteScout]
  #v(0.3em)
  #text(size: 14pt)[Discovery i archiwizacja prac naukowych przez cytowania]
  #v(2cm)
  #text(size: 12pt)[Raport projektowy — REST API]
  #v(0.5cm)
  #text(size: 11pt)[Krzysztof Czuba]
  #v(0.3cm)
  #text(size: 11pt)[#datetime.today().display("[day].[month].[year]")]
]

#pagebreak()
#outline(title: "Spis treści", indent: auto)
#pagebreak()

= Opis funkcjonalności

CiteScout to narzędzie do *odnajdywania prac powiązanych* (related works) przez
śledzenie cytowań. Użytkownik dodaje interesujące go prace naukowe (pliki PDF),
a aplikacja automatycznie wydobywa z nich metadane oraz bibliografię i pomaga
odkrywać kolejne prace warte przeczytania.

Geneza: zadanie od promotora — _"znajdź prace powiązane z moją pracą. możesz sprawdzić prace,
które cytujesz w swojej pracy, oraz prace, które cytują twoją"_.

Pozwala to na utworzenie łatwo nawigowalnych drew powiązań. RestAPI pozwala na zautomatyzowanie najbardziej mozolnych czynności (parsowanie PDF, ekstrakcja cytowań, wyszukiwanie DOI), oraz uporządkowanie bazy prac w jednym miejscu (archiwum PDF, metadane, cytowania, autorzy, tagi).

== Główne funkcje

- *Dodawanie prac* — przez upload pliku PDF lub podanie adresu URL. Plik jest
  walidowany (nagłówek `%PDF-`), a pobieranie z URL jest zabezpieczone przed
  SSRF (tylko `http`/`https`, blokada adresów nie-publicznych).
- *Ekstrakcja metadanych i cytowań* — serwer GROBID parsuje PDF i zwraca tytuł,
  autorów, rok, abstrakt, DOI pracy oraz listę pozycji bibliograficznych wraz
  z miejscami (zdaniami) ich wystąpienia w tekście i numerem strony.
- *Nazewnictwo plików wg DOI* — zapisany PDF jest nazywany bezpieczną dla systemu
  plików formą DOI (fallback `<id>.pdf`). Dla istniejących baz dostępny jest skrypt
  migracyjny.
- *Deduplikacja po hashu* — identyczny plik (po SHA-256 treści) nie jest
  archiwizowany dwa razy; upload duplikatu przekierowuje do istniejącej pracy.
- *Rozwiązywanie DOI cytowań* — przycisk "find" zleca w tle wyszukanie DOI danej
  pozycji bibliograficznej (model Gemini z Google Search grounding); status jest
  odpytywany przez HTMX, bez przeładowania strony.
- *Tagowanie prac* — dowolne etykiety przypisywane do pracy.
- *Filtrowanie i listowanie* — lista prac z filtrami po autorze, tagu oraz treści
  (tytuł, abstrakt, tekst cytowań); klikalne listy autorów i tagów.
- *Discovery "cited by"* — dla pracy z DOI: dociągnięcie na życzenie listy prac,
  które ją cytują (OpenAlex). Realizuje wprost polecenie promotora.
- *Uwierzytelnianie* — opcjonalne logowanie jednym hasłem (SHA-256), sesja w cookie.
  Zaimplementowane wyłącznie na bibliotece standardowej.

== Architektura

Aplikacja napisana jest w języku *Go* z użyciem wyłącznie biblioteki standardowej
dla warstwy HTTP (`net/http`, `html/template`). Trwałość zapewnia *SQLite*
(sterownik `mattn/go-sqlite3`), pliki PDF leżą na dysku w konfigurowalnym katalogu
(docelowo zamontowany Google Drive). Front to lekki HTMX wstrzykiwany do szablonów.

#table(
  columns: (auto, 1fr),
  inset: 7pt,
  align: (left, left),
  stroke: 0.5pt + luma(200),
  table.header([*Moduł*], [*Odpowiedzialność*]),
  [`modules/repository`], [Warstwa danych (`PaperArchive`) oraz cała logika HTTP i szablony HTML/HTMX.],
  [`modules/grobid`], [Klient serwera GROBID — ekstrakcja nagłówka i bibliografii z PDF (TEI/XML).],
  [`modules/doi`], [Klient Gemini (REST) z Google Search — wyszukiwanie DOI po tytule.],
  [`modules/openalex`], [Klient OpenAlex — prace cytujące daną pracę ("cited by").],
  [`modules/auth`], [Logowanie hasłem, sesje w cookie, middleware ochrony tras.],
  [`main.go`], [Złożenie zależności z konfiguracji (zmienne środowiskowe) i start serwera.],
)

= Sitemap — ścieżki API

Serwer nasłuchuje na porcie `8080`. Strony zwracają HTML, API zwraca JSON
(o ile nie zaznaczono inaczej).

== Strony (HTML)

#table(
  columns: (auto, auto, 1fr),
  inset: 7pt,
  stroke: 0.5pt + luma(200),
  table.header([*Metoda*], [*Ścieżka*], [*Opis*]),
  [GET], [`/`], [Przekierowanie na listę prac.],
  [GET], [`/papers/`], [Lista prac; filtry `?q=`, `?author=`, `?tag=`.],
  [GET], [`/new`], [Formularz dodania pracy (plik lub URL).],
  [GET], [`/papers/{id}`], [Strona pracy: metadane, tagi, "cited by", tabela cytowań.],
  [GET], [`/login`], [Formularz logowania (gdy auth włączony).],
  [POST], [`/login`], [Weryfikacja hasła, ustawienie cookie sesji.],
  [GET], [`/logout`], [Wylogowanie i kasowanie sesji.],
)

== API (JSON)

#table(
  columns: (auto, auto, 1fr),
  inset: 7pt,
  stroke: 0.5pt + luma(200),
  table.header([*Metoda*], [*Ścieżka*], [*Opis*]),
  [GET], [`/api/papers/`], [Lista prac; przyjmuje filtry `q`/`author`/`tag`.],
  [POST], [`/api/papers/`], [Dodanie pracy (multipart: pole `pdf` lub `url`).],
  [GET], [`/api/papers/{id}`], [Metadane pojedynczej pracy.],
  [PUT], [`/api/papers/{id}`], [Aktualizacja metadanych (JSON).],
  [DELETE], [`/api/papers/{id}`], [Usunięcie pracy wraz z plikiem PDF i cytowaniami.],
  [GET], [`/api/papers/{id}/view`], [Podgląd PDF w przeglądarce (inline).],
  [GET], [`/api/papers/{id}/download`], [Pobranie PDF (attachment).],
  [GET], [`/api/papers/{id}/citations`], [Cytowania wydobyte z pracy.],
  [POST], [`/api/papers/{id}/tags`], [Zastąpienie tagów (pole `tags`, po przecinku).],
  [GET], [`/api/papers/{id}/citing-works`], [Prace cytujące (OpenAlex, na życzenie).],
  [POST], [`/api/citations/{id}/resolve-doi`], [Zlecenie wyszukania DOI cytowania (Gemini).],
  [POST], [`/api/citations/{id}/doi`], [Ręczne ustawienie (`doi`) lub usunięcie DOI.],
  [GET], [`/api/citations/{id}/doi-cell`], [Fragment HTMX komórki DOI (polling statusu).],
)

= Schemat bazy danych

Baza SQLite (`archive.db`) zawiera cztery tabele. Schemat tworzony jest
idempotentnie przy starcie (`CREATE TABLE IF NOT EXISTS`), a brakujące kolumny
(np. `hash`) są dodawane migracją `ALTER TABLE`.

```sql
CREATE TABLE papers (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    title     TEXT,
    author    TEXT,      -- autorzy złączeni przecinkami
    year      INTEGER,
    abstract  TEXT,
    doi       TEXT,      -- DOI samej pracy (jeśli wykryte)
    hash      TEXT,      -- SHA-256 treści PDF (deduplikacja)
    filename  TEXT       -- nazwa pliku PDF na dysku
);

CREATE TABLE citations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    paper_id        INTEGER,   -- FK -> papers(id)
    citation_number INTEGER,   -- pozycja w bibliografii (1-based)
    citation_title  TEXT,
    citation_text   TEXT,      -- surowy tekst pozycji bibliograficznej
    doi             TEXT,      -- DOI cytowanej pracy (jeśli rozwiązane)
    FOREIGN KEY(paper_id) REFERENCES papers(id)
);

CREATE TABLE citation_occurrences (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    citation_id     INTEGER,   -- FK -> citations(id)
    occurrence_text TEXT,      -- zdanie, w którym cytowanie wystąpiło
    page            INTEGER,   -- numer strony
    FOREIGN KEY(citation_id) REFERENCES citations(id)
);

CREATE TABLE paper_tags (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    paper_id INTEGER NOT NULL,  -- FK -> papers(id)
    tag      TEXT NOT NULL,
    FOREIGN KEY(paper_id) REFERENCES papers(id)
);
CREATE UNIQUE INDEX idx_paper_tags_unique ON paper_tags(paper_id, tag);
```

== Relacje

- `papers` 1 — N `citations` (praca ma wiele cytowań),
- `citations` 1 — N `citation_occurrences` (cytowanie ma wiele wystąpień w tekście),
- `papers` 1 — N `paper_tags` (praca ma wiele tagów; para `(paper_id, tag)` jest unikalna).

= Opis testów

Testy uruchamia się przez `make test` (czyli `go test ./...`). Każdy pakiet
ma testy jednostkowe lub funkcjonalne; te ostatnie korzystają z `net/http/httptest`
oraz baz SQLite w katalogu tymczasowym (`t.TempDir()`) i atrap (fakes) dla GROBID
oraz OpenAlex.

#table(
  columns: (auto, 1fr),
  inset: 7pt,
  stroke: 0.5pt + luma(200),
  table.header([*Test*], [*Sprawdzany wymóg*]),
  [`TestUploadAndListPaper`], [Upload PDF tworzy pracę z metadanami i pojawia się na liście.],
  [`TestUploadDeduplicatesByHash`], [Dwukrotny upload tego samego pliku daje jedną pracę (dedup).],
  [`TestTagsAndFiltering`], [Ustawienie tagów oraz filtrowanie po tagu, autorze i treści.],
  [`TestCitingWorksFragment`], [Discovery "cited by" renderuje listę prac cytujących.],
  [`TestValidateFetchURLRejectsNonPublic`], [Odrzucenie URL-i SSRF (loopback, prywatne, zły schemat).],
  [`TestFindByHashDetectsDuplicate`], [Wyszukiwanie pracy po hashu; pusty hash nie pasuje.],
  [`TestEnsureColumnIsIdempotentAndMigrates`], [Migracja kolumny `hash` na starej bazie.],
  [`TestPDFFilename`], [Generowanie bezpiecznej nazwy pliku z DOI.],
  [`TestDOIQueueFillsMissingDOIs`], [Kolejka w tle uzupełnia brakujące DOI cytowań.],
  [`TestDisabledAuthPassesThrough`], [Brak hasła = brak ochrony (przejście).],
  [`TestMiddlewareBlocksUnauthenticated`], [Bez sesji: API → 401, strony → redirect na `/login`.],
  [`TestLoginFlowGrantsAccess`], [Pełny cykl: złe hasło, logowanie, dostęp, wylogowanie.],
  [`TestCitingWorks`], [Klient OpenAlex: rozwiązanie pracy i lista cytujących (mock).],
  [`TestNormalizeDOI`], [Normalizacja DOI (usunięcie prefiksu URL, lowercase).],
  [`TestExtractDOI`], [Wyciągnięcie DOI z odpowiedzi modelu.],
  [`TestParseHeader` / `TestParseFulltext*`], [Parsowanie TEI/XML z GROBID (nagłówek, cytowania, wystąpienia).],
)

Testy uruchamiane są również z detektorem wyścigów (`go test -race ./...`) ze
względu na współbieżną kolejkę DOI i magazyn sesji.

= Instrukcja uruchomienia

== Wymagania

- Go (wersja zgodna z `go.mod`),
- działający serwer GROBID (np. przez Docker Compose),
- opcjonalnie: klucz API Gemini (DOI cytowań), `sqlite3` CLI (skrypt migracji).

== Konfiguracja (zmienne środowiskowe)

Zmienne czytane są z pliku `.env` (nie nadpisują istniejących zmiennych powłoki).

#table(
  columns: (auto, 1fr),
  inset: 7pt,
  stroke: 0.5pt + luma(200),
  table.header([*Zmienna*], [*Znaczenie*]),
  [`PAPERS_DIR`], [Katalog na pliki PDF (domyślnie `data/papers`).],
  [`GROBID_URL`], [Adres serwera GROBID (domyślnie `http://localhost:8070`).],
  [`GROBID_CONSOLIDATE`], [`1` = rozwiązuj DOI cytowań w GROBID przez CrossRef (wolniej).],
  [`GEMINI_API_KEY`], [Włącza wyszukiwanie DOI cytowań (web search).],
  [`GEMINI_MODEL`], [Model Gemini (domyślnie `gemini-2.5-flash`).],
  [`OPENALEX_MAILTO`], [E-mail kontaktowy do "polite pool" OpenAlex.],
  [`AUTH_PASSWORD_HASH`], [SHA-256 hex hasła logowania; ustawione = wymóg logowania.],
)

== Uruchomienie GROBID

```sh
docker compose up -d grobid
```

== Uruchomienie aplikacji

```sh
# kompilacja i start
go run main.go
# lub: make compile && ./dist/cite-scout

# aplikacja wystartuje na http://localhost:8080
# formularz dodawania:   http://localhost:8080/new
```

== Włączenie logowania (opcjonalnie)

```sh
# wygeneruj hash hasła i wstaw do .env jako AUTH_PASSWORD_HASH
make hash-password PASSWORD=tajnehaslo
```

== Migracja nazw plików (jednorazowo)

```sh
# podgląd zmian bez modyfikacji
DRY_RUN=1 make migrate-filenames
# właściwa migracja
make migrate-filenames
```

== Uruchomienie testów

```sh
make test
# lub z detektorem wyścigów:
go test -race ./...
```

= Zrzuty ekranu

#figure(
  image("screens/add_paper.png"),
  caption: "Dodawanie nowego pliku PDF lub URL pracy naukowej.",
)
#v(0.5cm)
#figure(
  image("screens/filter.png"),
  caption: "Lista prac z paskiem filtrów (autor / tag / treść).",
)
#v(0.5cm)
#figure(
  image("screens/paper_details.png"),
  caption: "Strona pojedynczej pracy: metadane, tagi i tabela cytowań.",
)
