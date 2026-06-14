// Package doi uses a Gemini model with Google Search grounding to find
// the DOI of a scientific work given its title (plus an optional disambiguating
// hint such as authors or year).
package doi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const geminiURL = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

// defaultModel is a light, search-grounded model. Override via Client.Model.
const defaultModel = "gemini-2.5-flash"

// maxConcurrency bounds how many lookups run at once to respect rate limits.
const maxConcurrency = 5

const promptTemplate = `You are a bibliographic assistant. Use web search to find the DOI of the scientific work described below.
Respond with ONLY the DOI (for example: 10.1145/3292500.3300988) and nothing else.
If you cannot confidently identify the DOI, respond with exactly: NONE

Title: %s%s`

// doiPattern matches a DOI; the character class stops at whitespace, quotes and
// closing brackets so trailing prose is not captured.
var doiPattern = regexp.MustCompile(`10\.\d{4,9}/[^\s"'<>)\]}]+`)

// Client calls the Gemini generateContent REST API with the Google Search tool.
type Client struct {
	apiKey     string
	httpClient *http.Client
	// Model is the Gemini model id used for lookups (default "gemini-2.5-flash").
	Model string
}

// NewClient returns a client authenticating with the given Gemini API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		Model:      defaultModel,
	}
}

// Query describes one work whose DOI we want to find.
type Query struct {
	Title string
	// Hint is optional extra context (e.g. the raw citation string) used to
	// disambiguate works that share a title.
	Hint string
}

// FindDOI returns the DOI for a single query, or "" if none was found.
func (c *Client) FindDOI(ctx context.Context, q Query) (string, error) {
	if strings.TrimSpace(q.Title) == "" {
		return "", nil
	}

	hint := ""
	if strings.TrimSpace(q.Hint) != "" {
		hint = "\nAdditional details: " + strings.TrimSpace(q.Hint)
	}
	prompt := fmt.Sprintf(promptTemplate, strings.TrimSpace(q.Title), hint)

	text, err := c.generate(ctx, prompt)
	if err != nil {
		return "", err
	}
	return extractDOI(text), nil
}

// FindDOIs resolves a batch of queries concurrently. The returned slice is
// aligned with queries; entries are "" where no DOI was found or a lookup
// failed. The error is non-nil only if the overall context is cancelled.
func (c *Client) FindDOIs(ctx context.Context, queries []Query) []string {
	results := make([]string, len(queries))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for i, q := range queries {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, q Query) {
			defer wg.Done()
			defer func() { <-sem }()
			doi, err := c.FindDOI(ctx, q)
			if err != nil {
				return // leave results[i] == ""
			}
			results[i] = doi
		}(i, q)
	}

	wg.Wait()
	return results
}

// --- Gemini REST plumbing ---

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

// geminiTool enables the built-in Google Search grounding tool.
type geminiTool struct {
	GoogleSearch *struct{} `json:"google_search,omitempty"`
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
	Tools    []geminiTool    `json:"tools,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// generate sends one prompt (with the search tool enabled) and returns the
// concatenated text of the first candidate.
func (c *Client) generate(ctx context.Context, prompt string) (string, error) {
	reqBody := geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
		Tools:    []geminiTool{{GoogleSearch: &struct{}{}}},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf(geminiURL, c.Model) + "?" + url.Values{"key": {c.apiKey}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result geminiResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("gemini error: %s", result.Error.Message)
	}
	if len(result.Candidates) == 0 {
		return "", fmt.Errorf("empty response from gemini")
	}

	var b strings.Builder
	for _, p := range result.Candidates[0].Content.Parts {
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

// extractDOI pulls the first DOI out of model output, trimming trailing
// punctuation. Returns "" when the model answered NONE or no DOI is present.
func extractDOI(text string) string {
	doi := doiPattern.FindString(text)
	return strings.TrimRight(doi, ".,;:")
}
