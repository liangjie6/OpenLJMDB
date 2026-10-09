package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestImportClassificationFullContentArgmaxAndGuards(t *testing.T) {
	a := coreApp(t)
	one := coreCreateKB(t, a)
	two := coreStatus(t, securityRequest(a, "POST", "/api/v1/knowledge-bases", map[string]any{
		"name": "数据库", "description": "SQL、事务、索引",
	}, nil), 201)["id"].(string)
	content := "# 数据库\n" + strings.Repeat("SQL事务与索引。\n", 1200) + "最后一段也必须传给模型。"
	p := coreStatus(t, multipartImport(t, a, []string{"数据库.md"}, [][]byte{[]byte(content)}), 200)
	source := p["documents"].([]any)[0].(map[string]any)["source_id"].(string)
	body := map[string]any{"preview_id": p["preview_id"], "source_id": source}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("incorrect endpoint or authentication")
		}
		var request struct {
			Model     string            `json:"model"`
			State     map[string]string `json:"state"`
			Questions map[string]struct {
				Type     string            `json:"type"`
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != decisionModel || request.State["markdown"] != content || request.State["title"] != "数据库" || request.Questions["category"].Type != "choice" || !strings.Contains(request.Questions["category"].Criteria[two], "SQL、事务、索引") {
			t.Error("classification did not receive full note and category descriptions")
		}
		// Even when choice disagrees and confidence is low, use the largest probability.
		json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"category": map[string]any{
			"type": "choice", "choice": one, "confidence": 0.1,
			"probabilities": map[string]float64{one: 0.4, two: 0.6},
		}}})
	}))
	defer server.Close()
	t.Setenv("LJMDB_DECISION_API_KEY", "test-secret")
	t.Setenv("LJMDB_DECISION_BASE_URL", server.URL+"/")
	coreStatus(t, securityRequest(a, "POST", "/api/v1/imports/classify", body, nil), 409)
	coreStatus(t, securityRequest(a, "PUT", "/api/v1/settings", map[string]any{"import_auto_classify": true}, nil), 200)
	answer := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports/classify", body, nil), 200)
	if answer["target_knowledge_base_id"] != two || answer["probability"] != 0.6 || answer["confidence"] != 0.1 || answer["target_parent_id"] != nil {
		t.Fatal("highest-probability selection failed", answer)
	}
	zip := coreStatus(t, multipartImport(t, a, []string{"notes.zip"}, [][]byte{transferZIP(t, map[string]string{"a.md": "正文"})}), 200)
	if zip["source_kind"] != "zip" || p["source_kind"] != "markdown" {
		t.Fatal("source kinds missing")
	}
	coreStatus(t, securityRequest(a, "POST", "/api/v1/imports/classify", map[string]any{
		"preview_id": zip["preview_id"], "source_id": zip["documents"].([]any)[0].(map[string]any)["source_id"],
	}, nil), 400)
	coreStatus(t, securityRequest(a, "POST", "/api/v1/imports/classify", map[string]any{"preview_id": p["preview_id"], "source_id": NewID()}, nil), 400)
	t.Setenv("LJMDB_DECISION_API_KEY", "")
	coreStatus(t, securityRequest(a, "POST", "/api/v1/imports/classify", body, nil), 503)
	if calls.Load() != 1 {
		t.Fatal("disabled, ZIP, invalid source or missing key reached upstream", calls.Load())
	}
	var count int
	a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count)
	if count != 0 {
		t.Fatal("classification created documents")
	}
}

func TestDecisionRejectsInvalidResultsAndRedactsUpstreamErrors(t *testing.T) {
	for _, test := range []struct {
		name, response string
		status         int
	}{
		{"unknown category", `{"answers":{"category":{"type":"choice","choice":"unknown","confidence":0.9,"probabilities":{"a":0.9,"unknown":0.1}}}}`, 200},
		{"missing probabilities", `{"answers":{"category":{"type":"choice","choice":"a","confidence":0.9}}}`, 200},
		{"out of range", `{"answers":{"category":{"type":"choice","choice":"a","confidence":0.9,"probabilities":{"a":1.5,"b":0.1}}}}`, 200},
		{"invalid confidence", `{"answers":{"category":{"type":"choice","choice":"a","confidence":4,"probabilities":{"a":0.9,"b":0.1}}}}`, 200},
		{"upstream failure", `test-secret private-note`, 401},
		{"malformed", `<html>test-secret private-note</html>`, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				w.Write([]byte(test.response))
			}))
			defer server.Close()
			_, err := classifyMarkdown(context.Background(), server.URL, "test-secret", "title", "private-note", map[string]string{"a": "A", "b": "B"}, []string{"a", "b"})
			if err == nil || strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "private-note") {
				t.Fatal("invalid response accepted or private data exposed", err)
			}
		})
	}
}

func TestPerDocumentImportDestinationsLinksAndIdempotency(t *testing.T) {
	a := coreApp(t)
	one, two := coreCreateKB(t, a), coreCreateKB(t, a)
	parent := coreCreateDoc(t, a, two, nil, "父文档", "")
	p := coreStatus(t, multipartImport(t, a, []string{"a.md", "b.md"}, [][]byte{[]byte("[B](b.md#section)"), []byte("[A](a.md)")}), 200)
	targets := []importTarget{}
	for _, v := range p["documents"].([]any) {
		d := v.(map[string]any)
		target := importTarget{SourceID: d["source_id"].(string), TargetKnowledgeBaseID: one, ExpectedTreeRevision: coreTreeRevision(t, a, one)}
		if d["path"] == "b.md" {
			target.TargetKnowledgeBaseID = two
			target.TargetParentID = &parent.ID
			target.ExpectedTreeRevision = coreTreeRevision(t, a, two)
		}
		targets = append(targets, target)
	}
	body := map[string]any{"preview_id": p["preview_id"], "targets": targets}
	headers := map[string]string{"Idempotency-Key": "separate-destinations"}
	id := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", body, headers), 202)["job_id"].(string)
	job := transferWait(t, a, id)
	if coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", body, headers), 202)["job_id"] != id {
		t.Fatal("retry created a second batch")
	}
	var first, second Document
	for _, v := range job["result"].(map[string]any)["documents"].([]any) {
		d, err := LoadDocument(context.Background(), a.DB, v.(map[string]any)["id"].(string), true)
		if err != nil {
			t.Fatal(err)
		}
		if d.Title == "a" {
			first = d
		} else {
			second = d
		}
	}
	if first.KnowledgeBaseID != one || first.ParentID != nil || second.KnowledgeBaseID != two || second.ParentID == nil || *second.ParentID != parent.ID || !strings.Contains(first.Markdown, "/documents/"+second.ID+"#section") || !strings.Contains(second.Markdown, "/documents/"+first.ID) {
		t.Fatal("destinations, parents or cross-library links were lost", first, second)
	}
	if coreTreeRevision(t, a, one) != 2 || coreTreeRevision(t, a, two) != 3 {
		t.Fatal("tree revisions not incremented once per touched library")
	}
	var count int
	a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count)
	if count != 3 {
		t.Fatal("duplicate import", count)
	}
}

func TestPerDocumentImportValidationAndAtomicRollback(t *testing.T) {
	a := coreApp(t)
	one, two := coreCreateKB(t, a), coreCreateKB(t, a)
	parent := coreCreateDoc(t, a, two, nil, "父文档", "")
	p := coreStatus(t, multipartImport(t, a, []string{"a.md", "b.md"}, [][]byte{[]byte("first"), []byte("second")}), 200)
	docs := p["documents"].([]any)
	targets := []importTarget{
		{SourceID: docs[0].(map[string]any)["source_id"].(string), TargetKnowledgeBaseID: one, ExpectedTreeRevision: 1},
		{SourceID: docs[1].(map[string]any)["source_id"].(string), TargetKnowledgeBaseID: two, TargetParentID: &parent.ID, ExpectedTreeRevision: 2},
	}
	for _, test := range []struct {
		name    string
		targets []importTarget
		status  int
	}{
		{"missing assignment", targets[:1], 400},
		{"duplicate source", []importTarget{targets[0], targets[0]}, 400},
		{"unknown source", []importTarget{targets[0], {SourceID: NewID(), TargetKnowledgeBaseID: two, ExpectedTreeRevision: 2}}, 400},
		{"wrong library parent", []importTarget{{SourceID: targets[0].SourceID, TargetKnowledgeBaseID: one, TargetParentID: &parent.ID, ExpectedTreeRevision: 1}, targets[1]}, 400},
		{"stale second library", []importTarget{targets[0], {SourceID: targets[1].SourceID, TargetKnowledgeBaseID: two, ExpectedTreeRevision: 1}}, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{"preview_id": p["preview_id"], "targets": test.targets}, nil), test.status)
		})
	}
	// Corrupt the later file so the task fails after starting the transaction.
	var preview ImportPreview
	if err := readJSONFile(filepath.Join(a.DataDir, "tmp", "imports", p["preview_id"].(string), "preview.json"), &preview); err != nil {
		t.Fatal(err)
	}
	last := preview.Documents[0]
	for _, d := range preview.Documents {
		if d.SortOrder > last.SortOrder {
			last = d
		}
	}
	if err := os.WriteFile(filepath.Join(a.DataDir, "tmp", "imports", preview.ID, "files", last.Path), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	id := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{"preview_id": p["preview_id"], "targets": targets}, nil), 202)["job_id"].(string)
	deadline := time.Now().Add(10 * time.Second)
	failed := false
	for time.Now().Before(deadline) {
		job := coreStatus(t, securityRequest(a, "GET", "/api/v1/jobs/"+id, nil, nil), 200)
		if job["state"] == "failed" {
			failed = true
			break
		}
		if job["state"] == "succeeded" {
			t.Fatal("changed content was imported")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var count int
	a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count)
	if !failed || count != 1 || coreTreeRevision(t, a, one) != 1 || coreTreeRevision(t, a, two) != 2 {
		t.Fatal("failed multi-library batch was not completely rolled back", count)
	}
	zip := coreStatus(t, multipartImport(t, a, []string{"tree.zip"}, [][]byte{transferZIP(t, map[string]string{"folder/index.md": "parent", "folder/child.md": "child"})}), 200)
	zipDocs := zip["documents"].([]any)
	zipTargets := []importTarget{}
	for _, v := range zipDocs {
		zipTargets = append(zipTargets, importTarget{SourceID: v.(map[string]any)["source_id"].(string), TargetKnowledgeBaseID: one, ExpectedTreeRevision: 1})
	}
	coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{"preview_id": zip["preview_id"], "targets": zipTargets}, nil), 400)
}
