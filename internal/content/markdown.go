package content

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const maxTableColumns = 8

type Document struct {
	Blocks []Block
	TOC    []TOCItem
}

// TOCItem describes one rendered second- to fourth-level heading. ID is the
// sanitized, duplicate-free anchor injected into the published HTML; Level
// keeps the Markdown heading level (2..4).
type TOCItem struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level int    `json:"level"`
}

type Block struct {
	Kind     string
	Text     string
	Language string
	Fallback string
}

type MarkdownError struct {
	Message string
}

func (e MarkdownError) Error() string { return e.Message }

func ParseMarkdown(source string) (Document, error) {
	if len(source) > 200000 {
		return Document{}, MarkdownError{"Markdown exceeds the 200000 character limit"}
	}
	if strings.Contains(strings.ToLower(source), "<script") || strings.Contains(strings.ToLower(source), "javascript:") {
		return Document{}, MarkdownError{"Markdown contains unsafe HTML or URL content"}
	}

	lines := strings.Split(source, "\n")
	document := Document{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "```mermaid") {
			language := strings.TrimSpace(strings.TrimPrefix(line, "```"))
			var code []string
			i++
			for ; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
				code = append(code, lines[i])
			}
			if i == len(lines) {
				return Document{}, MarkdownError{"Code block is not closed"}
			}
			document.Blocks = append(document.Blocks, Block{Kind: "code", Language: language, Text: strings.Join(code, "\n")})
			continue
		}
		if strings.HasPrefix(line, "```mermaid") {
			var diagram []string
			i++
			for ; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
				diagram = append(diagram, lines[i])
			}
			if i == len(lines) {
				return Document{}, MarkdownError{"Mermaid block is not closed"}
			}
			fallback, next, err := mermaidFallback(lines, i+1)
			if err != nil {
				return Document{}, err
			}
			document.Blocks = append(document.Blocks, Block{Kind: "mermaid", Text: strings.Join(diagram, "\n"), Fallback: fallback})
			i = next - 1
			continue
		}
		if strings.HasPrefix(line, "|") && strings.Count(line, "|")-1 > maxTableColumns {
			return Document{}, MarkdownError{"Tables may not contain more than 8 columns"}
		}
	}
	return document, nil
}

func mermaidFallback(lines []string, start int) (string, int, error) {
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[start]), "Mermaid fallback:") {
		return "", start, MarkdownError{"Mermaid blocks require a following Mermaid fallback: description"}
	}
	fallback := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[start]), "Mermaid fallback:"))
	if fallback == "" {
		return "", start, MarkdownError{"Mermaid fallback must not be empty"}
	}
	return fallback, start + 1, nil
}

func RenderHTML(source string) (Document, string, error) {
	document, err := ParseMarkdown(source)
	if err != nil {
		return Document{}, "", err
	}
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.Table),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(&headingTOCTransformer{document: &document}, 100))),
	)
	var rendered bytes.Buffer
	if err := markdown.Convert([]byte(sourceWithoutMermaid(source)), &rendered); err != nil {
		return Document{}, "", fmt.Errorf("render Markdown: %w", err)
	}
	output := sanitizeHTML(rendered.String())
	output = injectHeadingIDs(output, document.TOC)
	if document.TOC == nil {
		document.TOC = []TOCItem{}
	}
	for _, block := range document.Blocks {
		output += "<figure class=\"mermaid-fallback\"><figcaption>" + html.EscapeString(block.Fallback) + "</figcaption><pre><code>" + html.EscapeString(block.Text) + "</code></pre></figure>\n"
	}
	return document, output, nil
}

// headingTOCTransformer records every second- to fourth-level heading of the
// parsed document: its plain text, its Markdown level, and the stable,
// duplicate-free anchor id that later gets injected into the sanitized HTML.
type headingTOCTransformer struct {
	document *Document
}

func (h *headingTOCTransformer) Transform(node *ast.Document, reader text.Reader, _ parser.Context) {
	used := map[string]bool{}
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		heading, ok := n.(*ast.Heading)
		if !ok || heading.Level < 2 || heading.Level > 4 {
			return ast.WalkContinue, nil
		}
		textContent := strings.TrimSpace(string(n.Text(reader.Source())))
		if textContent == "" {
			return ast.WalkContinue, nil
		}
		id := headingAnchorID(textContent, used)
		used[id] = true
		h.document.TOC = append(h.document.TOC, TOCItem{ID: id, Text: textContent, Level: heading.Level})
		return ast.WalkContinue, nil
	})
}

// headingAnchorID slugifies the heading text to [a-z0-9-] and, when needed,
// appends the smallest free numeric suffix so ids stay unique even when the
// text itself looks like a suffixed duplicate.
func headingAnchorID(value string, used map[string]bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = true
			continue
		}
		// ASCII punctuation and whitespace become a single word separator;
		// non-ASCII runes (e.g. diacritics) are dropped entirely so
		// "Diseño & más" collapses to "diseo-ms".
		if r < 128 && dash {
			b.WriteByte('-')
			dash = false
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "section"
	}
	if !used[base] {
		used[base] = true
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

// headingOpenTag matches the attribute-free heading open tags the sanitizer
// produces; ids are injected after sanitization so the whitelist stays
// untouched and every id carries the controlled [a-z0-9-] charset.
var headingOpenTag = regexp.MustCompile(`<(h[2-4])>`)

func injectHeadingIDs(value string, items []TOCItem) string {
	queues := map[int][]string{}
	for _, item := range items {
		queues[item.Level] = append(queues[item.Level], item.ID)
	}
	next := map[int]int{}
	var b strings.Builder
	last := 0
	for _, match := range headingOpenTag.FindAllStringSubmatchIndex(value, -1) {
		level := int(value[match[2]+1] - '0')
		if ids := queues[level]; next[level] < len(ids) {
			b.WriteString(value[last:match[0]])
			fmt.Fprintf(&b, `<h%d id="%s">`, level, ids[next[level]])
			next[level]++
		} else {
			b.WriteString(value[last:match[1]])
		}
		last = match[1]
	}
	b.WriteString(value[last:])
	return b.String()
}

func sourceWithoutMermaid(source string) string {
	lines := strings.Split(source, "\n")
	var clean []string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "```mermaid") {
			clean = append(clean, lines[i])
			continue
		}
		// Skip the mermaid body through its closing fence.
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
			i++
		}
		// i now points at the closing fence. Skip the blank lines and the
		// mandatory "Mermaid fallback:" description that follow it.
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
			i++
		}
		if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "Mermaid fallback:") {
			i++
		}
	}
	return strings.Join(clean, "\n")
}

func sanitizeHTML(value string) string {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("h1", "h2", "h3", "h4", "h5", "h6", "p", "br", "strong", "em", "del", "ul", "ol", "li", "blockquote", "pre", "code", "table", "thead", "tbody", "tr", "th", "td", "hr", "a")
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowURLSchemes("http", "https")
	return policy.Sanitize(strings.ReplaceAll(value, "<!-- raw HTML omitted -->", ""))
}

func ValidURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
