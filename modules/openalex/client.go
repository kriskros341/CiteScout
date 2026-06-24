// Package openalex is a tiny client for the OpenAlex API. It is used to
// discover works that cite a given paper ("cited by" / reverse discovery),
// fetched on demand. OpenAlex needs no API key; passing a mailto address opts
// into its faster "polite pool".
package openalex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openalex.org"

// Client talks to the OpenAlex HTTP API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	// Mailto, when set, is sent as the mailto query parameter (polite pool).
	Mailto string
}

// NewClient returns a client. mailto may be empty; if set (a contact email) it
// opts into OpenAlex's polite pool.
func NewClient(mailto string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		Mailto:     strings.TrimSpace(mailto),
	}
}

// Work is a minimal view of an OpenAlex work record.
type Work struct {
	Title string `json:"title"`
	DOI   string `json:"doi"`
	Year  int    `json:"year"`
}

// maxCitingWorks caps how many citing works we return for one lookup.
const maxCitingWorks = 50

// CitingWorks returns works that cite the paper identified by doi, most recent
// first. It returns an empty slice (not an error) when OpenAlex knows of no
// citations. The doi may be bare ("10.1145/x") or a URL.
func (c *Client) CitingWorks(ctx context.Context, doi string) ([]Work, error) {
	doi = normalizeDOI(doi)
	if doi == "" {
		return nil, fmt.Errorf("no DOI to look up")
	}

	// OpenAlex's "cites" filter takes an OpenAlex id, not a DOI, so resolve the
	// work first; its record carries cited_by_api_url, the ready-made listing of
	// everything that cites it.
	work, err := c.fetchWork(ctx, doi)
	if err != nil {
		return nil, err
	}
	if work.CitedByURL == "" {
		return []Work{}, nil
	}
	return c.fetchCiting(ctx, work.CitedByURL)
}

// rawWork is the subset of an OpenAlex work record we decode.
type rawWork struct {
	ID              string `json:"id"`
	DOI             string `json:"doi"`
	Title           string `json:"title"`
	PublicationYear int    `json:"publication_year"`
	CitedByURL      string `json:"cited_by_api_url"`
}

// fetchWork resolves a single work by DOI.
func (c *Client) fetchWork(ctx context.Context, doi string) (rawWork, error) {
	endpoint := c.baseURL + "/works/doi:" + url.PathEscape(doi)
	var work rawWork
	if err := c.getJSON(ctx, endpoint, &work); err != nil {
		return rawWork{}, err
	}
	return work, nil
}

// fetchCiting reads the first page of the cited_by listing.
func (c *Client) fetchCiting(ctx context.Context, citedByURL string) ([]Work, error) {
	u, err := url.Parse(citedByURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("per-page", fmt.Sprintf("%d", maxCitingWorks))
	q.Set("sort", "publication_year:desc")
	u.RawQuery = q.Encode()

	var page struct {
		Results []rawWork `json:"results"`
	}
	if err := c.getJSON(ctx, u.String(), &page); err != nil {
		return nil, err
	}

	works := make([]Work, 0, len(page.Results))
	for _, r := range page.Results {
		works = append(works, Work{
			Title: r.Title,
			DOI:   normalizeDOI(r.DOI),
			Year:  r.PublicationYear,
		})
	}
	return works, nil
}

// getJSON performs a GET (adding the mailto polite-pool param) and decodes the
// JSON body into v.
func (c *Client) getJSON(ctx context.Context, endpoint string, v any) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	if c.Mailto != "" {
		q := u.Query()
		q.Set("mailto", c.Mailto)
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling openalex: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("openalex returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// normalizeDOI strips a leading DOI URL prefix and lowercases the result, so
// "https://doi.org/10.1/X" and "10.1/x" compare equal.
func normalizeDOI(doi string) string {
	doi = strings.TrimSpace(doi)
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "doi.org/"} {
		doi = strings.TrimPrefix(doi, prefix)
	}
	return strings.ToLower(strings.TrimSpace(doi))
}
