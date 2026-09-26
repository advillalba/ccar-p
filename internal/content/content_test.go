package content

import (
	"context"
	"strings"
	"testing"
)

func TestNoteInputValidationAndReadingTime(t *testing.T) {
	input := NoteInput{DomainID: "domain", Title: "Title", Slug: "valid-slug", Markdown: "one two", Tags: []TagInput{{Name: "cloud"}}, References: []ReferenceInput{{Title: "AWS", URL: "https://example.com", Position: 1}}}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := EstimateReadingTime(input.Markdown); got != 1 {
		t.Fatalf("reading time = %d", got)
	}
	input.Slug = "Invalid Slug"
	if err := input.Validate(); err == nil {
		t.Fatal("expected slug error")
	}
}

func TestMarkdownRejectsUnsafeAndExtractsMermaidFallback(t *testing.T) {
	_, _, err := RenderHTML("<script>alert(1)</script>")
	if err == nil {
		t.Fatal("expected unsafe content rejection")
	}
	doc, output, err := RenderHTML("# Heading\n\n```mermaid\ngraph TD\n```\nMermaid fallback: Request flow from client to server.")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].Fallback == "" {
		t.Fatalf("unexpected document: %#v", doc)
	}
	if !strings.Contains(output, "Request flow") || strings.Contains(output, "<script") {
		t.Fatalf("unsafe or missing fallback output: %s", output)
	}
}

func TestMarkdownRejectsMissingMermaidFallbackAndWideTables(t *testing.T) {
	if _, err := ParseMarkdown("```mermaid\ngraph TD\n```"); err == nil {
		t.Fatal("expected missing fallback error")
	}
	if _, err := ParseMarkdown("|a|b|c|d|e|f|g|h|i|\n|-|-|-|-|-|-|-|-|-|"); err == nil {
		t.Fatal("expected table limit error")
	}
}

type links struct {
	internal bool
	status   int
	err      error
}

func (l links) InternalExists(context.Context, string) (bool, error) { return l.internal, l.err }
func (l links) ExternalStatus(context.Context, string) (int, error)  { return l.status, l.err }

func TestPublicationValidationAndTransitions(t *testing.T) {
	note := Note{DomainID: "domain", Title: "Title", Slug: "note", Markdown: "[bad](/missing) [remote](https://example.com)"}
	result := ValidateForPublication(context.Background(), note, links{internal: false, status: 503}, ValidationOptions{CheckExternal: true})
	if result.Valid() || len(result.Errors) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if !CanTransition(StatusDraft, StatusPublished) || CanTransition(StatusArchived, StatusPublished) {
		t.Fatal("unexpected transition rules")
	}
}

func TestRenderHTMLInjectsHeadingAnchorsAndTOC(t *testing.T) {
	source := `# Top

## Getting *Started*

Intro.

## Getting Started

Again.

### Deep Dive

Body.

##### TooDeep

Deep.

` + "```mermaid" + `
graph LR
  A --> B
` + "```" + `
Mermaid fallback: flow diagram

## After Diagram
`
	document, output, err := RenderHTML(source)
	if err != nil {
		t.Fatalf("RenderHTML returned error: %v", err)
	}
	want := []TOCItem{
		{ID: "getting-started", Text: "Getting Started", Level: 2},
		{ID: "getting-started-2", Text: "Getting Started", Level: 2},
		{ID: "deep-dive", Text: "Deep Dive", Level: 3},
		{ID: "after-diagram", Text: "After Diagram", Level: 2},
	}
	if len(document.TOC) != len(want) {
		t.Fatalf("expected %d TOC items, got %#v", len(want), document.TOC)
	}
	for i, item := range want {
		if document.TOC[i] != item {
			t.Fatalf("TOC item %d: expected %#v, got %#v", i, item, document.TOC[i])
		}
	}
	for _, anchor := range []string{`<h2 id="getting-started">`, `<h2 id="getting-started-2">`, `<h3 id="deep-dive">`, `<h2 id="after-diagram">`} {
		if !strings.Contains(output, anchor) {
			t.Fatalf("expected %q in output: %s", anchor, output)
		}
	}
	if strings.Contains(output, "<h1 id=") || strings.Contains(output, "<h5 id=") {
		t.Fatalf("h1/h5 must not carry anchors: %s", output)
	}
	if document.TOC[0].Text != "Getting Started" || strings.Contains(output, "<strong>Started</strong>") && !strings.Contains(output, `<h2 id="getting-started">Getting <strong>Started</strong></h2>`) {
		t.Fatalf("TOC text must be plain while the heading keeps its formatting: %s | %s", document.TOC[0].Text, output)
	}
	if !strings.Contains(output, "mermaid-fallback") {
		t.Fatalf("mermaid fallback must remain after the heading anchors: %s", output)
	}
}

func TestRenderHeadingAnchorIDSlugifiesAndDeDuplicates(t *testing.T) {
	used := map[string]bool{}
	if id := headingAnchorID("Diseño & más", used); id != "diseo-ms" {
		t.Fatalf("unexpected slug: %q", id)
	}
	first := headingAnchorID("Overview", used)
	if first != "overview" {
		t.Fatalf("unexpected first: %q", first)
	}
	if second := headingAnchorID("Overview", used); second != "overview-2" {
		t.Fatalf("unexpected duplicate: %q", second)
	}
	if third := headingAnchorID("Overview-2", used); third != "overview-2-2" {
		t.Fatalf("literal suffix collision not resolved: %q", third)
	}
}
