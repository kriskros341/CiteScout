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

func TestParseFulltext(t *testing.T) {
	refs, err := parseFulltext(strings.NewReader(sampleTEI))
	if err != nil {
		t.Fatalf("parseFulltext: %v", err)
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

const sampleFulltextTEI = `<?xml version="1.0" encoding="UTF-8"?>
<TEI xmlns="http://www.tei-c.org/ns/1.0">
 <text>
  <body>
   <div>
    <p>
     <s coords="3,72.0,107.4,200.6,12.0">As shown by prior work <ref type="bibr" target="#b0" coords="3,150.0,107.4,8.0,12.0">[1]</ref>, attention helps.</s>
     <s coords="6,72.0,300.0,200.6,12.0">Following <ref type="bibr" target="#b0" coords="6,90.0,300.0,8.0,12.0">[1]</ref> we adopt the same setup.</s>
    </p>
   </div>
  </body>
  <back>
   <div type="references">
    <listBibl>
     <biblStruct xml:id="b0">
      <analytic><title level="a">Attention is all you need</title></analytic>
      <note type="raw_reference">Vaswani et al. Attention is all you need. 2017.</note>
     </biblStruct>
     <biblStruct xml:id="b1">
      <analytic><title level="a">Deep Learning</title></analytic>
      <note type="raw_reference">Goodfellow et al. Deep Learning. 2016.</note>
     </biblStruct>
    </listBibl>
   </div>
  </back>
 </text>
</TEI>`

func TestParseFulltextOccurrences(t *testing.T) {
	refs, err := parseFulltext(strings.NewReader(sampleFulltextTEI))
	if err != nil {
		t.Fatalf("parseFulltext: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d references, want 2", len(refs))
	}

	occ := refs[0].Occurrences
	if len(occ) != 2 {
		t.Fatalf("ref[0] got %d occurrences, want 2", len(occ))
	}
	if occ[0].Page != 3 {
		t.Errorf("occ[0].Page = %d, want 3", occ[0].Page)
	}
	if !strings.Contains(occ[0].Text, "attention helps") || !strings.Contains(occ[0].Text, "[1]") {
		t.Errorf("occ[0].Text = %q (should be the full sentence incl. marker)", occ[0].Text)
	}
	if occ[1].Page != 6 {
		t.Errorf("occ[1].Page = %d, want 6", occ[1].Page)
	}

	if len(refs[1].Occurrences) != 0 {
		t.Errorf("ref[1] got %d occurrences, want 0", len(refs[1].Occurrences))
	}
}
