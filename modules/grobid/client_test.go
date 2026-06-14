package grobid

import (
	"strings"
	"testing"
)

const sampleTEI = `<?xml version="1.0" encoding="UTF-8"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
 <text>
  <back>
   <div type="references">
    <listBibl>
     <biblStruct xml:id="b0">
      <analytic>
       <title level="a">Attention is all you need</title>
       <idno type="DOI">10.5555/3295222.3295349</idno>
      </analytic>
      <note type="raw_reference">Vaswani et al. Attention is all you need. NeurIPS 2017.</note>
     </biblStruct>
     <biblStruct xml:id="b1">
      <monogr>
       <title level="m">Deep Learning</title>
      </monogr>
      <note type="raw_reference">Goodfellow et al. Deep Learning. MIT Press 2016.</note>
     </biblStruct>
    </listBibl>
   </div>
  </back>
 </text>
</TEI>`

const sampleHeaderTEI = `<?xml version="1.0" encoding="UTF-8"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
 <teiHeader>
  <fileDesc>
   <titleStmt>
    <title level="a" type="main">Attention Is All You Need</title>
   </titleStmt>
   <sourceDesc>
    <biblStruct>
     <analytic>
      <author><persName><forename type="first">Ashish</forename><surname>Vaswani</surname></persName></author>
      <author><persName><forename type="first">Noam</forename><surname>Shazeer</surname></persName></author>
     </analytic>
     <monogr>
      <imprint><date type="published" when="2017-06-12">2017</date></imprint>
     </monogr>
     <idno type="DOI">10.5555/3295222.3295349</idno>
    </biblStruct>
   </sourceDesc>
  </fileDesc>
  <profileDesc>
   <abstract>
    <div><p>The dominant sequence transduction models are based on recurrent networks.</p></div>
   </abstract>
  </profileDesc>
 </teiHeader>
</TEI>`

func TestParseHeader(t *testing.T) {
	h, err := parseHeader(strings.NewReader(sampleHeaderTEI))
	if err != nil {
		t.Fatalf("parseHeader: %v", err)
	}
	if h.Title != "Attention Is All You Need" {
		t.Errorf("Title = %q", h.Title)
	}
	if h.Author != "Ashish Vaswani, Noam Shazeer" {
		t.Errorf("Author = %q", h.Author)
	}
	if h.Year != 2017 {
		t.Errorf("Year = %d, want 2017", h.Year)
	}
	if !strings.Contains(h.Abstract, "dominant sequence transduction") {
		t.Errorf("Abstract = %q", h.Abstract)
	}
	if h.DOI != "10.5555/3295222.3295349" {
		t.Errorf("DOI = %q", h.DOI)
	}
}

func TestParseReferences(t *testing.T) {
	refs, err := parseReferences(strings.NewReader(sampleTEI))
	if err != nil {
		t.Fatalf("parseReferences: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d references, want 2", len(refs))
	}

	if refs[0].Number != 1 {
		t.Errorf("ref[0].Number = %d, want 1", refs[0].Number)
	}
	if refs[0].DOI != "10.5555/3295222.3295349" {
		t.Errorf("ref[0].DOI = %q", refs[0].DOI)
	}
	if !strings.Contains(refs[0].Text, "Attention is all you need") {
		t.Errorf("ref[0].Text = %q", refs[0].Text)
	}
	if refs[0].Title != "Attention is all you need" {
		t.Errorf("ref[0].Title = %q", refs[0].Title)
	}
	if refs[1].Title != "Deep Learning" {
		t.Errorf("ref[1].Title = %q", refs[1].Title)
	}

	if refs[1].Number != 2 {
		t.Errorf("ref[1].Number = %d, want 2", refs[1].Number)
	}
	if refs[1].DOI != "" {
		t.Errorf("ref[1].DOI = %q, want empty", refs[1].DOI)
	}
	if !strings.Contains(refs[1].Text, "Deep Learning") {
		t.Errorf("ref[1].Text = %q", refs[1].Text)
	}
}
