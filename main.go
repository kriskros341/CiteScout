package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/mattn/go-sqlite3" // Import the SQLite driver

	"restapi/modules/grobid"
	"restapi/modules/repository"
)

func main() {
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
	handlers := repository.NewPaperArchiveHandlers(archive, grobidClient)

	mux := http.NewServeMux()
	handlers.Register(mux)

	fmt.Printf("Scientific paper archive starting on port 8080 (storing PDFs in %q)...\n", storageDir)
	fmt.Printf("Extracting metadata and citations via GROBID at %s\n", grobidURL)
	fmt.Println("Open http://localhost:8080/new to upload a paper.")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
