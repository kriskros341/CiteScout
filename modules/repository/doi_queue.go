package repository

import (
	"context"
	"log"
	"sync"
	"time"

	"citescout/modules/doi"
)

// DOI resolution job statuses, as shown on the paper page.
const (
	DOIStatusIdle    = ""
	DOIStatusQueued  = "queued"
	DOIStatusRunning = "running"
	DOIStatusDone    = "done"
	DOIStatusError   = "error"
)

// DOIQueue runs DOI lookups in the background, one job per citation. Clicking
// "find" on a citation enqueues it rather than blocking the request while a web
// search runs.
type DOIQueue struct {
	archive  *PaperArchive
	resolver DOIResolver
	jobs     chan int

	mu     sync.Mutex
	status map[int]string // citationID -> status
}

// NewDOIQueue creates a queue that resolves DOIs for the given archive using
// resolver. Call Start to launch the worker(s).
func NewDOIQueue(archive *PaperArchive, resolver DOIResolver, buffer int) *DOIQueue {
	return &DOIQueue{
		archive:  archive,
		resolver: resolver,
		jobs:     make(chan int, buffer),
		status:   make(map[int]string),
	}
}

// Start launches n background workers that process enqueued citations.
func (q *DOIQueue) Start(workers int) {
	for i := 0; i < workers; i++ {
		go func() {
			for citationID := range q.jobs {
				q.process(citationID)
			}
		}()
	}
}

// Enqueue schedules DOI resolution for a citation. It is a no-op (returns
// false) if the citation is already queued or running.
func (q *DOIQueue) Enqueue(citationID int) bool {
	q.mu.Lock()
	switch q.status[citationID] {
	case DOIStatusQueued, DOIStatusRunning:
		q.mu.Unlock()
		return false
	}
	q.status[citationID] = DOIStatusQueued
	q.mu.Unlock()

	q.jobs <- citationID
	return true
}

// Status returns the current resolution status for a citation.
func (q *DOIQueue) Status(citationID int) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.status[citationID]
}

func (q *DOIQueue) setStatus(citationID int, status string) {
	q.mu.Lock()
	q.status[citationID] = status
	q.mu.Unlock()
}

// process resolves and stores the DOI for a single citation, if it is missing
// one and has a title to search by.
func (q *DOIQueue) process(citationID int) {
	q.setStatus(citationID, DOIStatusRunning)

	c, err := q.archive.GetCitation(citationID)
	if err != nil {
		log.Printf("dois: loading citation %d: %v", citationID, err)
		q.setStatus(citationID, DOIStatusError)
		return
	}
	if c.DOI != "" || c.Title == "" {
		q.setStatus(citationID, DOIStatusDone) // nothing to do
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resolved, err := q.resolver.FindDOI(ctx, doi.Query{Title: c.Title, Hint: c.Text})
	if err != nil {
		log.Printf("dois: resolving citation %d: %v", citationID, err)
		q.setStatus(citationID, DOIStatusError)
		return
	}
	if resolved != "" {
		if err := q.archive.UpdateCitationDOI(citationID, resolved); err != nil {
			log.Printf("dois: updating citation %d: %v", citationID, err)
			q.setStatus(citationID, DOIStatusError)
			return
		}
		log.Printf("dois: citation %d resolved to %s", citationID, resolved)
	}

	q.setStatus(citationID, DOIStatusDone)
}
