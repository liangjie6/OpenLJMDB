package backend

import (
	xhtml "golang.org/x/net/html"
	"strings"
	"testing"
)

func TestRenderContractAndSafeReferences(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	url := "/api/v1/attachments/" + id + "/content"
	md := "# 重复\n\n# 重复\n\n中国**文学** [外部](https://example.test/a)\n\n![引用][图片]\n\n[图片]: <" + url + "?download=1>\n\n<img src=\"/api/v1/attachments/%31" + id[1:] + "/content\">\n\n`![代码](" + url + ")`\n\n$x+y$\n\n$$\na^2+b^2=c^2\n$$\n\n```mermaid\ngraph TD; A-->B\n```\n\n<script>alert(1)</script><a href=\"javascript:alert(1)\">坏链接</a><img src=\"x\" onerror=\"alert(1)\">\n\n- [x] 完成\n"
	r := RenderMarkdown(md)
	if len(r.AttachmentIDs) != 1 || r.AttachmentIDs[0] != id {
		t.Fatal(r.AttachmentIDs)
	}
	for _, want := range []string{"中国文学", "https://example.test/a", "a^2+b^2=c^2", "graph TD; A-->B"} {
		if !strings.Contains(r.SearchText, want) {
			t.Fatalf("search missing %q: %s", want, r.SearchText)
		}
	}
	if strings.Contains(r.HTML, "<script") || strings.Contains(r.HTML, "javascript:") || strings.Contains(r.HTML, "onerror=") {
		t.Fatal(r.HTML)
	}
	for _, want := range []string{`class="language-math"`, `class="language-mermaid"`, `disabled`} {
		if !strings.Contains(r.HTML, want) {
			t.Fatalf("render missing %s: %s", want, r.HTML)
		}
	}
	z := xhtml.NewTokenizer(strings.NewReader(r.HTML))
	ids := map[string]bool{}
	headings := 0
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		if tt == xhtml.StartTagToken {
			tag := z.Token()
			if tag.Data == "h1" {
				headings++
				found := ""
				for _, attr := range tag.Attr {
					if attr.Key == "id" {
						found = attr.Val
					}
				}
				if found == "" || ids[found] {
					t.Fatalf("duplicate or missing heading ID: %s", r.HTML)
				}
				ids[found] = true
			}
		}
	}
	if headings != 2 {
		t.Fatal(r.HTML)
	}
	if AttachmentID("https://evil.test"+url) != "" || AttachmentID("//evil.test"+url) != "" {
		t.Fatal("external links bind local files")
	}
}

func TestRewriteLinksPreservesSourceAndCode(t *testing.T) {
	md := "  # 标题\r\n\r\n![a](images/a.png \"原始标题\")\r\n[x][REF]\r\n\r\n[REF]: <images/a.png> 'title'\r\n\r\n<img  alt='x' src = 'images/a.png' data-ignore='images/a.png'>\r\n\r\n`![a](images/a.png)`\r\n\r\n```markdown\r\n![a](images/a.png)\r\n```\r\n\r\n普通 images/a.png\r\n"
	unchanged, err := RewriteLinks(md, func(s string) (string, error) { return s, nil })
	if err != nil || unchanged != md {
		t.Fatalf("no-op altered markdown: %v\n%s", err, unchanged)
	}
	got, err := RewriteLinks(md, func(s string) (string, error) {
		if s == "images/a.png" {
			return "/api/v1/attachments/id/content", nil
		}
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "/api/v1/attachments/id/content") != 3 {
		t.Fatalf("incorrect substitutions: %s", got)
	}
	for _, want := range []string{"`![a](images/a.png)`", "```markdown\r\n![a](images/a.png)\r\n```", "普通 images/a.png", "data-ignore='images/a.png'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("changed non-link %s: %s", want, got)
		}
	}
	if !strings.Contains(got, "src = '/api/v1/attachments/id/content'") {
		t.Fatal(got)
	}
}

func TestRewriteLinksEscapedAndNestedDestinations(t *testing.T) {
	md := `[nested](assets/a(b).png "t")
[escaped](assets/a\(b\).png)
[angle](<assets/my file.png>)
[ref][r]

[r]: assets/a(b).png

<img src="assets/a&amp;b.png"><a href='assets/my%20file.png'>文件</a>

\[escaped](assets/no.png)

https://example.test/path?q=1&x=2
`
	links := ExtractLinks(md)
	if len(links) == 0 {
		t.Fatal("no parsed links")
	}
	got, err := RewriteLinks(md, func(s string) (string, error) {
		if strings.HasPrefix(s, "assets/") {
			return "output/资源 (新).png?x=1&y=2", nil
		}
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `\[escaped](assets/no.png)`) {
		t.Fatal("escaped syntax changed", got)
	}
	for _, want := range []string{"https://example.test/path?q=1&x=2", "output/资源%20%28新%29.png?x=1&y=2", `src="output/资源%20%28新%29.png?x=1&amp;y=2"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "assets/a(b).png") || strings.Contains(got, `assets/a\(b\).png`) {
		t.Fatal("parsed destination not rewritten", got)
	}
}

func TestRendererOptimizationPreservesTableAndMarkdownContract(t *testing.T) {
	for _, markdown := range []string{
		"plain line\n更多行\nnext line\n",
		strings.Repeat("ordinary prose\n", 100) + "escaped\\| pipe\n\n```sh\nprintf data | cat\n```\n",
		"head\n:---:\nbody\n",
		"head\n::\nbody\n",
		"> head\n> :---:\n> body\n",
		"- head\n  :---:\n  body\n",
		"|a|b|\n|---|:---:|\n|**x**|`a\\|b`|\n",
		"> 1. | a | b |\n>    | :--- | ---: |\n>    | x | y |\n",
		"## Heading\n\nfootnote[^1]\n\n[^1]: footnote text\n\n$x$\n\n```go\nfmt.Println(\"hi\")\n```\n",
	} {
		engine := markdownEngine()
		expected := renderPolicy().Sanitize(engine.MarkdownStr("document", markdown))
		actual := RenderMarkdown(markdown).HTML
		if actual != expected {
			t.Fatalf("renderer changed syntax for %q\nactual=%s\nexpected=%s", markdown, actual, expected)
		}
	}
}

func TestAllowedHTMLURLAttributesBecomeReferencesAndRewrite(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	target := "/api/v1/attachments/" + id + "/content"
	markdown := `<blockquote cite="` + target + `">正文</blockquote><q cite='` + target + `'>引用</q><map name="m"><area href="` + target + `" shape="default"></map>`
	rendered := RenderMarkdown(markdown)
	if len(rendered.AttachmentIDs) != 1 || rendered.AttachmentIDs[0] != id {
		t.Fatal(rendered.AttachmentIDs, rendered.HTML)
	}
	got, err := RewriteLinks(markdown, func(link string) (string, error) {
		if link == target {
			return "assets/file.bin", nil
		}
		return link, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "assets/file.bin") != 3 || strings.Contains(got, target) {
		t.Fatal("allowed URL attribute missed", got)
	}
}

func TestRenderSearchPreservesWordBoundariesAcrossBreaks(t *testing.T) {
	r := RenderMarkdown("foo\nbar\n\n中国**文学**\n\nfirst  \nsecond\n")
	if !strings.Contains(r.SearchText, "foo bar") || !strings.Contains(r.SearchText, "first second") || !strings.Contains(r.SearchText, "中国文学") {
		t.Fatal(r.SearchText)
	}
}
