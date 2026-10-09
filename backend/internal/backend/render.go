package backend

import (
	"context"
	"database/sql"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/88250/lute"
	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	"github.com/88250/lute/render"
	"github.com/microcosm-cc/bluemonday"
	xhtml "golang.org/x/net/html"
)

// RenderVersion identifies both the parser and the security/configuration contract.
const RenderVersion = "lute-1.7.6-ljmdb-2"

type Rendered struct {
	HTML          string
	SearchText    string
	Version       string
	AttachmentIDs []string
}

func markdownEngine() *lute.Lute {
	e := lute.New()
	e.SetGFMTable(true)
	e.SetGFMTaskListItem(true)
	e.SetGFMStrikethrough(true)
	e.SetGFMAutoLink(true)
	e.SetFootnotes(true)
	e.SetHeadingID(true)
	e.SetHeadingAnchor(false)
	e.SetAutoSpace(false)
	e.SetFixTermTypo(false)
	e.SetEmoji(false)
	e.SetCodeSyntaxHighlight(true)
	e.SetCodeSyntaxHighlightInlineStyle(false)
	e.SetLinkRef(true)
	e.SetSanitize(false) // The final, common sanitizer is applied below.
	return e
}

// Lute 1.7.6 attempts table parsing at every soft line and repeatedly copies
// the remainder of a paragraph. Only skip that expensive attempt when the
// source has no possible delimiter row. Keep single-column tables (which may
// omit pipes) and be conservative about blockquote/list container prefixes.
func canContainTable(markdown string) bool {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 2 || !strings.ContainsAny(line, ":-|") {
			continue
		}
		possible := true
		for _, r := range line {
			if !strings.ContainsRune(" \t\r:-|>+*.0123456789)", r) {
				possible = false
				break
			}
		}
		if possible {
			return true
		}
	}
	return false
}

func parseMarkdown(markdown string) (*lute.Lute, *parse.Tree) {
	e := markdownEngine()
	e.SetGFMTable(canContainTable(markdown))
	return e, parse.Parse("document", []byte(markdown), e.ParseOptions)
}

// Policies are immutable after construction; Sanitize keeps request-local
// tokenizer state and is safe to share across concurrent renders.
var markdownPolicy = buildRenderPolicy()

func renderPolicy() *bluemonday.Policy { return markdownPolicy }
func buildRenderPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[\pL\pN_:.\-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6", "a", "li", "sup")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^[a-zA-Z0-9_\- ]{1,200}$`)).OnElements("span", "div", "code", "pre", "ul", "ol", "li", "input", "p", "section")
	p.AllowElements("input")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("disabled", "checked").OnElements("input")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|right|center)$`)).OnElements("th", "td")
	p.AllowRelativeURLs(true)
	p.RequireNoFollowOnLinks(true)
	return p
}

func RenderMarkdown(markdown string) Rendered {
	e, tree := parseMarkdown(markdown)
	var search strings.Builder
	ids := map[string]bool{}
	ast.Walk(tree.Root, func(n *ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			switch n.Type {
			case ast.NodeParagraph, ast.NodeHeading, ast.NodeCodeBlock, ast.NodeMathBlock, ast.NodeTableCell, ast.NodeListItem:
				search.WriteByte('\n')
			}
			return ast.WalkContinue
		}
		switch n.Type {
		case ast.NodeSoftBreak, ast.NodeHardBreak:
			search.WriteByte('\n')
		case ast.NodeText, ast.NodeLinkText, ast.NodeCodeSpanContent, ast.NodeCodeBlockCode,
			ast.NodeInlineMathContent, ast.NodeMathBlockContent, ast.NodeBackslashContent,
			ast.NodeEmojiUnicode, ast.NodeYamlFrontMatterContent:
			search.Write(n.Tokens)
		case ast.NodeHTMLEntity:
			search.WriteString(html.UnescapeString(string(n.Tokens)))
		case ast.NodeLinkDest:
			link := normalizeLink(string(n.Tokens))
			if id := AttachmentID(link); id != "" {
				ids[id] = true
			} else {
				search.WriteString(" " + link + " ")
			}
		case ast.NodeInlineHTML, ast.NodeHTMLBlock:
			links, texts := htmlContent(string(n.Tokens))
			for _, text := range texts {
				search.WriteString(text)
			}
			for _, link := range links {
				if id := AttachmentID(link); id != "" {
					ids[id] = true
				} else {
					search.WriteString(" " + link + " ")
				}
			}
		}
		return ast.WalkContinue
	})
	attachmentIDs := make([]string, 0, len(ids))
	for id := range ids {
		attachmentIDs = append(attachmentIDs, id)
	}
	sort.Strings(attachmentIDs)
	return Rendered{HTML: renderPolicy().Sanitize(string(render.NewHtmlRenderer(tree, e.RenderOptions).Render())), SearchText: strings.Join(strings.Fields(search.String()), " "), Version: RenderVersion, AttachmentIDs: attachmentIDs}
}

func normalizeLink(raw string) string {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	// CommonMark backslash escaping applies to punctuation inside destinations.
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) && strings.ContainsRune(`!"#$%&'()*+,-./:;<=>?@[\]^_`+"`"+`{|}~`, rune(raw[i+1])) {
			i++
		}
		b.WriteByte(raw[i])
	}
	return b.String()
}

// AttachmentID accepts only local root-relative addresses; an external host using
// the same path must never become a reference to a local file.
func AttachmentID(raw string) string {
	u, err := url.Parse(normalizeLink(raw))
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	clean := path.Clean(u.Path)
	const prefix = "/api/v1/attachments/"
	if !strings.HasPrefix(clean, prefix) || !strings.HasSuffix(clean, "/content") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(clean, prefix), "/content")
	if !ValidID(strings.ToLower(id)) {
		return ""
	}
	return strings.ToLower(id)
}

func htmlContent(source string) (links, texts []string) {
	// Use the same allowlist as rendering: unsafe tags and attributes do not
	// create hidden references which the user cannot inspect in the document.
	z := xhtml.NewTokenizer(strings.NewReader(renderPolicy().Sanitize(source)))
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return
		case xhtml.TextToken:
			texts = append(texts, string(z.Text()))
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			t := z.Token()
			for _, a := range t.Attr {
				if isHTMLLinkAttribute(t.Data, a.Key) {
					links = append(links, normalizeLink(a.Val))
				}
			}
		}
	}
}

func isHTMLLinkAttribute(tag, key string) bool {
	switch key {
	case "href":
		return tag == "a" || tag == "area"
	case "src":
		return tag == "img"
	case "cite":
		return tag == "blockquote" || tag == "q" || tag == "del" || tag == "ins"
	}
	return false
}

func ExtractLinks(markdown string) []string {
	_, t := parseMarkdown(markdown)
	var links []string
	seen := map[string]bool{}
	add := func(link string) {
		link = normalizeLink(link)
		if link != "" && !seen[link] {
			links = append(links, link)
			seen[link] = true
		}
	}
	ast.Walk(t.Root, func(n *ast.Node, entering bool) ast.WalkStatus {
		if entering {
			switch n.Type {
			case ast.NodeLinkDest:
				add(string(n.Tokens))
			case ast.NodeInlineHTML, ast.NodeHTMLBlock:
				ls, _ := htmlContent(string(n.Tokens))
				for _, l := range ls {
					add(l)
				}
			}
		}
		return ast.WalkContinue
	})
	return links
}

type linkSpan struct {
	start, end int
	html       bool
}

// candidateLinkSpans is a token scanner, not a Markdown parser. Candidates are
// subsequently validated by the Lute AST, so code examples, escaped syntax and
// ordinary text cannot be rewritten by accident.
func candidateLinkSpans(s string) []linkSpan {
	var spans []linkSpan
	addDestination := func(start int) {
		for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
			start++
		}
		if start >= len(s) {
			return
		}
		if s[start] == '<' {
			end := start + 1
			for end < len(s) && s[end] != '>' && s[end] != '\n' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			if end < len(s) && s[end] == '>' {
				spans = append(spans, linkSpan{start + 1, end, false})
			}
			return
		}
		end, depth := start, 0
		for end < len(s) {
			c := s[end]
			if c == '\\' && end+1 < len(s) {
				end += 2
				continue
			}
			if c == '(' {
				depth++
			}
			if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '<' {
				break
			}
			end++
		}
		if end > start {
			spans = append(spans, linkSpan{start, end, false})
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ']' && i+1 < len(s) && s[i+1] == '(' {
			addDestination(i + 2)
		}
		if s[i] == ']' && i+1 < len(s) && s[i+1] == ':' {
			addDestination(i + 2)
		}
		if s[i] == '<' {
			j := i + 1
			for j < len(s) && s[j] != '>' && s[j] != '\n' {
				j++
			}
			if j < len(s) && s[j] == '>' {
				inner := s[i+1 : j]
				if strings.HasPrefix(inner, "http://") || strings.HasPrefix(inner, "https://") || strings.HasPrefix(inner, "mailto:") {
					spans = append(spans, linkSpan{i + 1, j, false})
				}
			}
		}
	}
	// The HTML tokenizer exposes raw token lengths so attributes can be edited
	// without serializing and reformatting the surrounding HTML.
	z, offset := xhtml.NewTokenizer(strings.NewReader(s)), 0
	for {
		tt := z.Next()
		raw := string(z.Raw())
		base := offset
		offset += len(raw)
		if tt == xhtml.ErrorToken {
			break
		}
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		if !isHTMLLinkAttribute(t.Data, "href") && !isHTMLLinkAttribute(t.Data, "src") && !isHTMLLinkAttribute(t.Data, "cite") {
			continue
		}
		// Scan the raw attribute grammar, retaining quotes and spacing.
		for p := 1; p < len(raw); {
			for p < len(raw) && !isASCIIWhitespace(raw[p]) && raw[p] != '>' {
				p++
			}
			for p < len(raw) && isASCIIWhitespace(raw[p]) {
				p++
			}
			nameStart := p
			for p < len(raw) && !isASCIIWhitespace(raw[p]) && raw[p] != '=' && raw[p] != '>' && raw[p] != '/' {
				p++
			}
			if p == nameStart {
				break
			}
			name := strings.ToLower(raw[nameStart:p])
			for p < len(raw) && isASCIIWhitespace(raw[p]) {
				p++
			}
			if p >= len(raw) || raw[p] != '=' {
				continue
			}
			p++
			for p < len(raw) && isASCIIWhitespace(raw[p]) {
				p++
			}
			if p >= len(raw) {
				break
			}
			quote := byte(0)
			if raw[p] == '\'' || raw[p] == '"' {
				quote = raw[p]
				p++
			}
			valueStart := p
			for p < len(raw) && (quote != 0 && raw[p] != quote || quote == 0 && !isASCIIWhitespace(raw[p]) && raw[p] != '>') {
				p++
			}
			if isHTMLLinkAttribute(t.Data, name) {
				spans = append(spans, linkSpan{base + valueStart, base + p, true})
			}
			if quote != 0 && p < len(raw) {
				p++
			}
		}
	}
	// GFM automatic URLs are candidates too. The AST rejects copies in code.
	for i := 0; i < len(s); i++ {
		if !strings.HasPrefix(s[i:], "https://") && !strings.HasPrefix(s[i:], "http://") {
			continue
		}
		j := i
		for j < len(s) && !isASCIIWhitespace(s[j]) && !strings.ContainsRune("<>\"'", rune(s[j])) {
			j++
		}
		for j > i && strings.ContainsRune(".,;:!?)]}", rune(s[j-1])) {
			j--
		}
		if j > i {
			spans = append(spans, linkSpan{i, j, false})
			i = j - 1
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var unique []linkSpan
	for _, span := range spans {
		if len(unique) == 0 || span.start >= unique[len(unique)-1].end {
			unique = append(unique, span)
		}
	}
	return unique
}

func isASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

func escapedDestination(destination string) string {
	const hexadecimal = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(destination); i++ {
		c := destination[i]
		if c <= 0x20 || c == 0x7f || strings.ContainsRune(`<>"'()`+"`"+`\`, rune(c)) {
			b.WriteByte('%')
			b.WriteByte(hexadecimal[c>>4])
			b.WriteByte(hexadecimal[c&15])
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func RewriteLinks(markdown string, rewrite func(string) (string, error)) (string, error) {
	spans := candidateLinkSpans(markdown)
	if len(spans) == 0 {
		return markdown, nil
	}
	const marker = "https://ljmdb-link-rewrite.invalid/link-"
	var marked strings.Builder
	previous := 0
	for i, span := range spans {
		marked.WriteString(markdown[previous:span.start])
		marked.WriteString(marker + strconv.Itoa(i))
		previous = span.end
	}
	marked.WriteString(markdown[previous:])
	valid := map[string]bool{}
	for _, link := range ExtractLinks(marked.String()) {
		valid[link] = true
	}
	var output strings.Builder
	previous = 0
	for i, span := range spans {
		output.WriteString(markdown[previous:span.start])
		original := markdown[span.start:span.end]
		if valid[marker+strconv.Itoa(i)] {
			next, err := rewrite(normalizeLink(original))
			if err != nil {
				return "", err
			}
			if next == normalizeLink(original) {
				output.WriteString(original)
			} else {
				next = escapedDestination(next)
				if span.html {
					next = html.EscapeString(next)
				}
				output.WriteString(next)
			}
		} else {
			output.WriteString(original)
		}
		previous = span.end
	}
	output.WriteString(markdown[previous:])
	return output.String(), nil
}

func SyncDocumentAttachments(ctx context.Context, tx *sql.Tx, documentID string, ids []string) error {
	unique := map[string]bool{}
	for _, id := range ids {
		if !ValidID(id) {
			return Err(400, "INVALID_ARGUMENT", "附件引用 ID 无效", nil)
		}
		if unique[id] {
			continue
		}
		unique[id] = true
		var state string
		err := tx.QueryRowContext(ctx, "SELECT state FROM attachments WHERE id=?", id).Scan(&state)
		if err == sql.ErrNoRows || err == nil && state != "ready" {
			return Err(409, "ATTACHMENT_UNAVAILABLE", "正文引用的附件不存在或当前不可用", map[string]any{"attachment_id": id, "state": state})
		}
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM document_attachments WHERE document_id=?", documentID); err != nil {
		return err
	}
	for id := range unique {
		if _, err := tx.ExecContext(ctx, "INSERT INTO document_attachments(document_id,attachment_id) VALUES(?,?)", documentID, id); err != nil {
			return err
		}
	}
	return nil
}

func CopyRevisionAttachments(tx *sql.Tx, revisionID, documentID string) error {
	_, err := tx.Exec("INSERT INTO revision_attachments(revision_id,attachment_id) SELECT ?,attachment_id FROM document_attachments WHERE document_id=?", revisionID, documentID)
	return err
}
