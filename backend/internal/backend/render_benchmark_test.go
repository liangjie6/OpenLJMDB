package backend

import (
	"strings"
	"testing"
)

func BenchmarkRenderMarkdown100KB(b *testing.B) {
	paragraph := "中文知识库用于记录研究过程和实验结果。Markdown keeps source text, code, formulas and URLs.\n"
	body := strings.Repeat(paragraph, (100<<10)/len(paragraph))
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rendered := RenderMarkdown(body)
		if rendered.HTML == "" || rendered.SearchText == "" {
			b.Fatal("empty render")
		}
	}
}
