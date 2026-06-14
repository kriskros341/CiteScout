package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3" // Import the SQLite driver

	doi "restapi/modules/doi"
	"restapi/modules/grobid"
	"restapi/modules/repository"
)

// loadDotEnv reads simple KEY=VALUE lines from path into the environment. Lines
// may be blank, comments (#...), or optionally prefixed with "export". Existing
// environment variables are not overridden. A missing file is not an error.
func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

func main() {
	loadDotEnv(".env")

	// Open the SQLite database. It will be created if it doesn't exist.
	db, err := sql.Open("sqlite3", "./archive.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Directory where uploaded PDFs are stored. Override with PAPERS_DIR; this
	// can point at a mounted Google Drive folder (e.g. linked to data/papers).
	storageDir := os.Getenv("PAPERS_DIR")
	if storageDir == "" {
		storageDir = "data/papers"
	}

	// GROBID server used to extract citations from uploaded PDFs. Override with
	// GROBID_URL; the docker-compose service listens on port 8070.
	grobidURL := os.Getenv("GROBID_URL")
	if grobidURL == "" {
		grobidURL = "http://localhost:8070"
	}

	// Set up the scientific paper archive repository and its HTTP handlers.
	archive := repository.NewPaperArchive(db, storageDir)
	if err := archive.EnsureReady(); err != nil {
		log.Fatalf("Error preparing paper archive: %q", err)
	}
	grobidClient := grobid.NewClient(grobidURL)
	// Set GROBID_CONSOLIDATE=1 to resolve citation DOIs against CrossRef
	// (slower, needs network access).
	grobidClient.Consolidate = os.Getenv("GROBID_CONSOLIDATE") == "1"

	// Optional Gemini-backed DOI lookup (web search), run via a background task
	// queue. Enabled when GEMINI_API_KEY is set; override the model with
	// GEMINI_MODEL.
	var doiQueue *repository.DOIQueue
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		gemini := doi.NewClient(key)
		if model := os.Getenv("GEMINI_MODEL"); model != "" {
			gemini.Model = model
		}
		doiQueue = repository.NewDOIQueue(archive, gemini, 64)
		doiQueue.Start(2) // worker goroutines
		fmt.Printf("DOI lookup enabled via Gemini model %s\n", gemini.Model)
	} else {
		fmt.Println("DOI lookup disabled (set GEMINI_API_KEY to enable)")
	}

	handlers := repository.NewPaperArchiveHandlers(archive, grobidClient, doiQueue)

	mux := http.NewServeMux()
	handlers.Register(mux)

	fmt.Printf("Scientific paper archive starting on port 8080 (storing PDFs in %q)...\n", storageDir)
	fmt.Printf("Extracting metadata and citations via GROBID at %s\n", grobidURL)
	fmt.Println("Open http://localhost:8080/new to upload a paper.")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
