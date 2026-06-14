package doi

import "testing"

func TestExtractDOI(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"10.1145/3292500.3300988", "10.1145/3292500.3300988"},
		{"The DOI is 10.1038/nature14539.", "10.1038/nature14539"},
		{"Found it: https://doi.org/10.18653/v1/d17-1151", "10.18653/v1/d17-1151"},
		{"DOI: 10.5555/3295222.3295349 (NeurIPS)", "10.5555/3295222.3295349"},
		{"NONE", ""},
		{"I could not find a DOI for this work.", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractDOI(c.in); got != c.want {
			t.Errorf("extractDOI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
