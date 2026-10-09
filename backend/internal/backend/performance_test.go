package backend

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// Opt-in capacity evidence, kept separate from ordinary correctness tests.
func TestBackendPerformance(t *testing.T) {
	if os.Getenv("LJMDB_RUN_PERF") != "1" {
		t.Skip("set LJMDB_RUN_PERF=1 to run the 10,000-document capacity scenario")
	}
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	kb, doc := NewID(), NewID()
	paragraph := "中文知识库用于记录研究过程和实验结果。Markdown keeps source text, code, formulas and URLs.\n"
	body := strings.Repeat(paragraph, (20<<10)/len(paragraph)) + "\n中文短词 % literal_code\n"
	a.WriteMu.Lock()
	tx, err := a.DB.Begin()
	if err != nil {
		a.WriteMu.Unlock()
		t.Fatal(err)
	}
	now := Now()
	if _, err = tx.Exec("INSERT INTO knowledge_bases(id,name,created_at,updated_at) VALUES(?,'性能样本',?,?)", kb, now, now); err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO documents(id,knowledge_base_id,title,markdown,search_text,render_version,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?, ?,?)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		id := NewID()
		if i == 0 {
			id = doc
		}
		if _, err = stmt.Exec(id, kb, fmt.Sprintf("样本 %05d", i), body, body, RenderVersion, i, now, now); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.WriteMu.Unlock()
	t.Logf("platform=%s/%s CPUs=%d documents=10000 markdown_bytes=%d", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), len(body))
	for _, q := range []string{"Markdown", "中文短词", "%"} {
		var elapsed []time.Duration
		for i := 0; i < 6; i++ {
			start := time.Now()
			w := securityRequest(a, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil, nil)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			elapsed = append(elapsed, time.Since(start))
		}
		sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
		t.Logf("search=%q all_documents_match=true warmed_max=%s best=%s", q, elapsed[len(elapsed)-1], elapsed[0])
	}
	start := time.Now()
	tree := securityRequest(a, "GET", "/api/v1/knowledge-bases/"+kb+"/tree", nil, nil)
	if tree.Code != 200 {
		t.Fatal(tree.Body.String())
	}
	t.Logf("tree_nodes=10000 duration=%s response_bytes=%d", time.Since(start), tree.Body.Len())
	saveBody := strings.Repeat(paragraph, (100<<10)/len(paragraph))
	var saves []time.Duration
	for revision := 1; revision <= 20; revision++ {
		start := time.Now()
		w := securityRequest(a, "PUT", "/api/v1/documents/"+doc, map[string]any{"title": "样本保存", "markdown": saveBody + fmt.Sprintf("\n实际编辑 %d\n", revision), "expected_revision": revision}, nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		saves = append(saves, time.Since(start))
	}
	sort.Slice(saves, func(i, j int) bool { return saves[i] < saves[j] })
	t.Logf("save_markdown_bytes=%d samples=20 P95=%s max=%s", len(saveBody), saves[18], saves[19])
}
