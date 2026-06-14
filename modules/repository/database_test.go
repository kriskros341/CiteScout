package repository

import "testing"

func TestPDFFilename(t *testing.T) {
	cases := []struct {
		doi  string
		id   int
		want string
	}{
		{"10.1145/3292500", 7, "10.1145_3292500.pdf"},
		{"10.18653/v1/d17-1151", 7, "10.18653_v1_d17-1151.pdf"},
		{"", 7, "7.pdf"},
		{"  ", 7, "7.pdf"},
		{"10.1/../x", 7, "10.1_.._x.pdf"}, // no path separators survive
	}
	for _, c := range cases {
		if got := pdfFilename(c.doi, c.id); got != c.want {
			t.Errorf("pdfFilename(%q, %d) = %q, want %q", c.doi, c.id, got, c.want)
		}
	}
}
