package openalex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCitingWorks(t *testing.T) {
	var citedByPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/works/doi:"):
			// Resolve the work; point cited_by at this same server.
			citedByPath = "/works"
			w.Write([]byte(`{"id":"W1","cited_by_api_url":"` + serverURL(r) + `/works"}`))
		case r.URL.Path == citedByPath:
			w.Write([]byte(`{"results":[
				{"title":"A citing paper","doi":"https://doi.org/10.1/ABC","publication_year":2023},
				{"title":"Another","doi":"10.2/def","publication_year":2020}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient("test@example.com")
	c.baseURL = srv.URL

	works, err := c.CitingWorks(context.Background(), "10.9/target")
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("got %d works, want 2", len(works))
	}
	if works[0].Title != "A citing paper" || works[0].DOI != "10.1/abc" {
		t.Errorf("work[0] = %+v, want normalized DOI 10.1/abc", works[0])
	}
	if works[1].DOI != "10.2/def" {
		t.Errorf("work[1].DOI = %q, want 10.2/def", works[1].DOI)
	}
}

func TestNormalizeDOI(t *testing.T) {
	cases := map[string]string{
		"https://doi.org/10.1/ABC": "10.1/abc",
		"10.2/DEF":                 "10.2/def",
		"  10.3/x  ":               "10.3/x",
	}
	for in, want := range cases {
		if got := normalizeDOI(in); got != want {
			t.Errorf("normalizeDOI(%q) = %q, want %q", in, got, want)
		}
	}
}

// serverURL reconstructs the test server's base URL from a request.
func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
