package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func coreApp(t *testing.T) *App {
	t.Helper()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}
func coreStatus(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
	}
	return responseData(t, w)
}
func coreCreateKB(t *testing.T, a *App) string {
	t.Helper()
	data := coreStatus(t, securityRequest(a, "POST", "/api/v1/knowledge-bases", map[string]any{"name": " 知识库 ", "description": "testing"}, nil), 201)
	if data["name"] != "知识库" {
		t.Fatal("name not trimmed", data)
	}
	return data["id"].(string)
}
func coreTreeRevision(t *testing.T, a *App, kb string) int {
	t.Helper()
	var revision int
	if err := a.DB.QueryRow("SELECT tree_revision FROM knowledge_bases WHERE id=?", kb).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}
func coreCreateDoc(t *testing.T, a *App, kb string, parent *string, title, markdown string) Document {
	t.Helper()
	w := securityRequest(a, "POST", "/api/v1/knowledge-bases/"+kb+"/documents", map[string]any{"parent_id": parent, "title": title, "markdown": markdown, "expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	coreStatus(t, w, 201)
	var envelope struct {
		Data Document `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func coreDeleteDoc(t *testing.T, a *App, d Document) string {
	t.Helper()
	data := coreStatus(t, securityRequest(a, "DELETE", "/api/v1/documents/"+d.ID, map[string]any{"expected_tree_revision": coreTreeRevision(t, a, d.KnowledgeBaseID)}, nil), 200)
	return data["id"].(string)
}
func coreTrashToken(t *testing.T, a *App, batch string) (string, map[string]any) {
	t.Helper()
	data := coreStatus(t, securityRequest(a, "GET", "/api/v1/trash/"+batch, nil, nil), 200)
	return data["confirmation_token"].(string), data["delete_preview"].(map[string]any)
}
func coreFTSCount(t *testing.T, a *App, query string, want int) {
	t.Helper()
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM documents_fts WHERE documents_fts MATCH ?", query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("FTS %q count=%d want=%d", query, count, want)
	}
	if _, err := a.DB.Exec("INSERT INTO documents_fts(documents_fts,rank) VALUES('integrity-check',1)"); err != nil {
		t.Fatal("FTS integrity", err)
	}
}

func TestCoreConcurrentSaveConflictAndDeletedSave(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "原始", "initialtoken")
	initialTree := coreTreeRevision(t, a, kb)
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, text := range []string{"writer alpha", "writer beta"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			responses <- securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": "新标题", "markdown": text, "expected_revision": 1}, nil)
		}(text)
	}
	wg.Wait()
	close(responses)
	statuses := map[int]int{}
	for w := range responses {
		statuses[w.Code]++
		if w.Code != 200 && w.Code != 409 {
			t.Fatal(w.Body.String())
		}
		if w.Code == 409 && !strings.Contains(w.Body.String(), "REVISION_CONFLICT") {
			t.Fatal(w.Body.String())
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal("both editors overwrote", statuses)
	}
	current, err := LoadDocument(context.Background(), a.DB, d.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != 2 || current.Title != "新标题" {
		t.Fatal(current)
	}
	if got := coreTreeRevision(t, a, kb); got != initialTree+1 {
		t.Fatal("title tree revision", got)
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM document_revisions WHERE document_id=?", d.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	coreDeleteDoc(t, a, d)
	w := securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": "误复活", "markdown": "overwrite", "expected_revision": 2}, nil)
	coreStatus(t, w, 404)
	if _, err := LoadDocument(context.Background(), a.DB, d.ID, true); err == nil {
		t.Fatal("deleted document revived")
	}
}

func TestCoreHistoryRestoreAndFTSLifecycle(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "Alpha", "alphaword")
	coreFTSCount(t, a, "alphaword", 1)
	coreStatus(t, securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": "Beta", "markdown": "betaword", "expected_revision": 1, "snapshot": true}, nil), 200)
	coreFTSCount(t, a, "alphaword", 0)
	coreFTSCount(t, a, "betaword", 1)
	var historyID string
	if err := a.DB.QueryRow("SELECT id FROM document_revisions WHERE document_id=? AND revision=1", d.ID).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	data := coreStatus(t, securityRequest(a, "POST", "/api/v1/documents/"+d.ID+"/history/"+historyID+"/restore", map[string]any{"expected_revision": 2}, nil), 200)
	if data["revision"] != float64(3) || data["title"] != "Alpha" || data["markdown"] != "alphaword" {
		t.Fatal(data)
	}
	coreFTSCount(t, a, "alphaword", 1)
	coreFTSCount(t, a, "betaword", 0)
	var reason string
	if err := a.DB.QueryRow("SELECT reason FROM document_revisions WHERE document_id=? AND revision=2", d.ID).Scan(&reason); err != nil || reason != "before_restore" {
		t.Fatal(reason, err)
	}
	wrong := coreCreateDoc(t, a, kb, nil, "别的文档", "")
	coreStatus(t, securityRequest(a, "POST", "/api/v1/documents/"+wrong.ID+"/history/"+historyID+"/restore", map[string]any{"expected_revision": 1}, nil), 404)
	batch := coreDeleteDoc(t, a, d)
	coreFTSCount(t, a, "alphaword", 0)
	coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+batch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	coreFTSCount(t, a, "alphaword", 1)
	if _, err := a.DB.Exec("INSERT INTO documents_fts(documents_fts) VALUES('rebuild')"); err != nil {
		t.Fatal(err)
	}
	coreFTSCount(t, a, "alphaword", 1)
}

func TestCoreEmptyHistoryDetailKeepsMarkdownField(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "空正文", "")
	coreStatus(t, securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": d.Title, "markdown": "new", "expected_revision": 1, "snapshot": true}, nil), 200)
	var id string
	if err := a.DB.QueryRow("SELECT id FROM document_revisions WHERE document_id=? AND revision=1", d.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	detail := coreStatus(t, securityRequest(a, "GET", "/api/v1/documents/"+d.ID+"/history/"+id, nil, nil), 200)
	markdown, present := detail["markdown"]
	if !present || markdown != "" {
		t.Fatal("empty historical markdown omitted", detail)
	}
	w := securityRequest(a, "GET", "/api/v1/documents/"+d.ID+"/history", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 {
		t.Fatal(envelope)
	}
	if _, present := envelope.Data[0]["markdown"]; present {
		t.Fatal("history list leaked body", envelope)
	}
}

func TestCoreIndependentTrashBatchesAndLibraryPurge(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	root := coreCreateDoc(t, a, kb, nil, "父", "")
	child := coreCreateDoc(t, a, kb, &root.ID, "子", "childtoken")
	childBatch := coreDeleteDoc(t, a, child)
	rootBatch := coreDeleteDoc(t, a, root)
	data := coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+rootBatch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	if data["restored_count"] != float64(1) {
		t.Fatal("restored independent child", data)
	}
	coreStatus(t, securityRequest(a, "GET", "/api/v1/documents/"+child.ID, nil, nil), 404)
	rootBatch = coreDeleteDoc(t, a, root)
	token, preview := coreTrashToken(t, a, rootBatch)
	if preview["document_count"] != float64(1) {
		t.Fatal(preview)
	}
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash/"+rootBatch, map[string]any{"confirmation_token": token}, nil), 200)
	retained, err := LoadDocument(context.Background(), a.DB, child.ID, false)
	if err != nil || retained.ParentID != nil || retained.DeleteBatchID == nil || *retained.DeleteBatchID != childBatch {
		t.Fatal(retained, err)
	}
	data = coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+childBatch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	if len(data["parent_adjustments"].([]any)) != 1 {
		t.Fatal("missing removed-parent notice", data)
	}
	coreFTSCount(t, a, "childtoken", 1)
	grandchild := coreCreateDoc(t, a, kb, &child.ID, "独立回收", "")
	grandchildBatch := coreDeleteDoc(t, a, grandchild)
	data = coreStatus(t, securityRequest(a, "DELETE", "/api/v1/knowledge-bases/"+kb, map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	libraryBatch := data["id"].(string)
	coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+grandchildBatch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 409)
	token, preview = coreTrashToken(t, a, libraryBatch)
	if preview["document_count"] != float64(2) || preview["independent_batch_count"] != float64(1) {
		t.Fatal("library preview omitted trash", preview)
	}
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash/"+libraryBatch, map[string]any{"confirmation_token": token}, nil), 200)
	for _, table := range []string{"knowledge_bases", "documents", "document_revisions", "document_attachments", "revision_attachments"} {
		var count int
		if err := a.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s=%d %v", table, count, err)
		}
	}
	var failures int
	if err := a.DB.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&failures); err != nil || failures != 0 {
		t.Fatal(failures, err)
	}
	var purged *int64
	if err := a.DB.QueryRow("SELECT purged_at FROM trash_batches WHERE id=?", grandchildBatch).Scan(&purged); err != nil || purged == nil {
		t.Fatal("independent batch not audited", err)
	}
}

func TestCoreRestoreLibraryKeepsEarlierDeletedSubtree(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	root := coreCreateDoc(t, a, kb, nil, "父", "")
	child := coreCreateDoc(t, a, kb, &root.ID, "子", "")
	childBatch := coreDeleteDoc(t, a, child)
	data := coreStatus(t, securityRequest(a, "DELETE", "/api/v1/knowledge-bases/"+kb, map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	libraryBatch := data["id"].(string)
	data = coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+libraryBatch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	if data["restored_count"] != float64(1) {
		t.Fatal(data)
	}
	d, err := LoadDocument(context.Background(), a.DB, child.ID, false)
	if err != nil || d.DeleteBatchID == nil || *d.DeleteBatchID != childBatch {
		t.Fatal(d, err)
	}
	coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+childBatch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	d, err = LoadDocument(context.Background(), a.DB, child.ID, true)
	if err != nil || d.ParentID == nil || *d.ParentID != root.ID {
		t.Fatal(d, err)
	}
}

func TestCorePurgedParentRestoresAtRootWithSurvivingAncestor(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	grandparent := coreCreateDoc(t, a, kb, nil, "存活祖先", "")
	parent := coreCreateDoc(t, a, kb, &grandparent.ID, "永久删除父", "")
	child := coreCreateDoc(t, a, kb, &parent.ID, "独立批次", "")
	childBatch := coreDeleteDoc(t, a, child)
	parentBatch := coreDeleteDoc(t, a, parent)
	token, _ := coreTrashToken(t, a, parentBatch)
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash/"+parentBatch, map[string]any{"confirmation_token": token}, nil), 200)
	data := coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+childBatch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
	if len(data["parent_adjustments"].([]any)) != 1 {
		t.Fatal(data)
	}
	restored, err := LoadDocument(context.Background(), a.DB, child.ID, true)
	if err != nil || restored.ParentID != nil {
		t.Fatal("original unavailable parent must restore at root", restored, err)
	}
}

func TestCoreActiveTreeNodeLimitRejectsCreateAndRestoreAtomically(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	now := Now()
	if _, err := a.DB.Exec("WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?) INSERT INTO documents(id,knowledge_base_id,title,render_version,sort_order,created_at,updated_at) SELECT printf('%08x-0000-4000-8000-000000000000',n),?,'节点',?,n,?,? FROM seq", MaxTreeNodes, kb, RenderVersion, now, now); err != nil {
		t.Fatal(err)
	}
	w := securityRequest(a, "GET", "/api/v1/knowledge-bases/"+kb+"/tree", nil, nil)
	data := coreStatus(t, w, 200)
	if len(data["nodes"].([]any)) != MaxTreeNodes {
		t.Fatal("complete permitted tree response missing nodes")
	}
	w = securityRequest(a, "POST", "/api/v1/knowledge-bases/"+kb+"/documents", map[string]any{"parent_id": nil, "title": "超限", "expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	if w.Code != 413 || !strings.Contains(w.Body.String(), "TREE_LIMIT_EXCEEDED") {
		t.Fatal(w.Body.String())
	}
	batchID, docID := NewID(), NewID()
	tx, err := a.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO trash_batches(id,kind,root_document_id,knowledge_base_id,created_at) VALUES(?,'document_subtree',?,?,?)", batchID, docID, kb, now); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO documents(id,knowledge_base_id,title,deleted_at,delete_batch_id,created_at,updated_at) VALUES(?,?,'待恢复',?,?,?,?)", docID, kb, now, batchID, now, now); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	revision := coreTreeRevision(t, a, kb)
	w = securityRequest(a, "POST", "/api/v1/trash/"+batchID+"/restore", map[string]any{"expected_tree_revision": revision}, nil)
	if w.Code != 413 || !strings.Contains(w.Body.String(), "TREE_LIMIT_EXCEEDED") {
		t.Fatal(w.Body.String())
	}
	d, err := LoadDocument(context.Background(), a.DB, docID, false)
	if err != nil || d.DeletedAt == nil || coreTreeRevision(t, a, kb) != revision {
		t.Fatal("over-limit restore partially committed", d, err)
	}
}

func TestCoreTreeDepthValidationAndAtomicRestore(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	other := coreCreateKB(t, a)
	var parent *string
	for depth := 1; depth <= 32; depth++ {
		d := coreCreateDoc(t, a, kb, parent, fmt.Sprintf("层%d", depth), "")
		parent = &d.ID
	}
	path := "/api/v1/knowledge-bases/" + kb + "/documents"
	w := securityRequest(a, "POST", path, map[string]any{"parent_id": parent, "title": "第33层", "expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "TREE_DEPTH_EXCEEDED") {
		t.Fatal(w.Body.String())
	}
	w = securityRequest(a, "POST", "/api/v1/knowledge-bases/"+other+"/documents", map[string]any{"parent_id": parent, "title": "跨库", "expected_tree_revision": coreTreeRevision(t, a, other)}, nil)
	coreStatus(t, w, 400)
	w = securityRequest(a, "POST", path, map[string]any{"parent_id": nil, "title": "旧树版本", "expected_tree_revision": 1}, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "TREE_REVISION_CONFLICT") {
		t.Fatal(w.Body.String())
	}
	w = securityRequest(a, "POST", path, map[string]any{"title": "缺省根", "expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	coreStatus(t, w, 400)
	deleted := coreCreateDoc(t, a, kb, nil, "待恢复", "")
	batch := coreDeleteDoc(t, a, deleted)
	if _, err := a.DB.Exec("UPDATE documents SET parent_id=? WHERE id=?", parent, deleted.ID); err != nil {
		t.Fatal(err)
	}
	w = securityRequest(a, "POST", "/api/v1/trash/"+batch+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "TREE_DEPTH_EXCEEDED") {
		t.Fatal(w.Body.String())
	}
	retained, err := LoadDocument(context.Background(), a.DB, deleted.ID, false)
	if err != nil || retained.DeletedAt == nil {
		t.Fatal("restore partially committed", retained, err)
	}
	var firstID string
	if err := a.DB.QueryRow("SELECT id FROM documents WHERE knowledge_base_id=? AND parent_id IS NULL AND deleted_at IS NULL", kb).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec("UPDATE documents SET parent_id=? WHERE id=?", parent, firstID); err != nil {
		t.Fatal(err)
	}
	w = securityRequest(a, "GET", "/api/v1/knowledge-bases/"+kb+"/tree", nil, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "TREE_INTEGRITY_ERROR") {
		t.Fatal("cycle not detected", w.Body.String())
	}
	w = securityRequest(a, "POST", path, map[string]any{"parent_id": nil, "title": "损坏树", "expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	coreStatus(t, w, 409)
}

func TestCoreTrashConfirmationBindsContentAndCacheRefresh(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "源", "cachetoken")
	if _, err := a.DB.Exec("UPDATE documents SET html='stale',search_text='stale',render_version='old' WHERE id=?", d.ID); err != nil {
		t.Fatal(err)
	}
	data := coreStatus(t, securityRequest(a, "GET", "/api/v1/documents/"+d.ID, nil, nil), 200)
	if data["html"] == "stale" || data["revision"] != float64(1) {
		t.Fatal(data)
	}
	var historyCount int
	if err := a.DB.QueryRow("SELECT count(*) FROM document_revisions WHERE document_id=?", d.ID).Scan(&historyCount); err != nil || historyCount != 0 {
		t.Fatal(historyCount, err)
	}
	coreFTSCount(t, a, "cachetoken", 1)
	batch := coreDeleteDoc(t, a, d)
	token, _ := coreTrashToken(t, a, batch)
	if _, err := a.DB.Exec("UPDATE documents SET markdown='changed without revision' WHERE id=?", d.ID); err != nil {
		t.Fatal(err)
	}
	w := securityRequest(a, "DELETE", "/api/v1/trash/"+batch, map[string]any{"confirmation_token": token}, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "CONFIRMATION_EXPIRED") {
		t.Fatal(w.Body.String())
	}
	if _, err := LoadDocument(context.Background(), a.DB, d.ID, false); err != nil {
		t.Fatal("stale confirmation deleted", err)
	}
}

func TestCoreHistoryWindowRetentionAndUnavailableAttachmentRollback(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "历史", "first")
	for revision := 1; revision <= 3; revision++ {
		coreStatus(t, securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": "历史", "markdown": fmt.Sprintf("version%d", revision), "expected_revision": revision}, nil), 200)
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM document_revisions WHERE document_id=?", d.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("automatic window not coalesced", count, err)
	}
	if _, err := a.DB.Exec("UPDATE settings SET value_json='2' WHERE key='history_limit'"); err != nil {
		t.Fatal(err)
	}
	for revision := 4; revision <= 6; revision++ {
		coreStatus(t, securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": "历史", "markdown": fmt.Sprintf("manual%d", revision), "expected_revision": revision, "snapshot": true}, nil), 200)
	}
	if err := a.DB.QueryRow("SELECT count(*) FROM document_revisions WHERE document_id=?", d.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("history retention", count, err)
	}
	unavailableID := NewID()
	w := securityRequest(a, "PUT", "/api/v1/documents/"+d.ID, map[string]any{"title": "坏保存", "markdown": "![bad](/api/v1/attachments/" + unavailableID + "/content)", "expected_revision": 7, "snapshot": true}, nil)
	if w.Code != 409 && w.Code != 400 {
		t.Fatal("unavailable reference accepted", w.Code, w.Body.String())
	}
	current, err := LoadDocument(context.Background(), a.DB, d.ID, true)
	if err != nil || current.Revision != 7 || current.Title != "历史" {
		t.Fatal("failed save partially committed", current, err)
	}
	if err := a.DB.QueryRow("SELECT count(*) FROM document_revisions WHERE document_id=? AND revision=7", d.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("snapshot leaked from rollback", count, err)
	}
}

func TestCoreSchemaDriverCapabilitiesUpgradeAndRefusal(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	for pragma, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "busy_timeout": 5000, "user_version": SchemaVersion} {
		var value int
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&value); err != nil || value != want {
			t.Fatal(pragma, value, err)
		}
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal(mode, err)
	}
	var valid int
	if err := db.QueryRow("SELECT json_valid(?)", `{"test":true}`).Scan(&valid); err != nil || valid != 1 {
		t.Fatal("JSON driver capability", valid, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dir, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if newer, err := OpenDB(dir); err == nil {
		newer.Close()
		t.Fatal("older binary wrote newer schema")
	}
	legacy := t.TempDir()
	raw, err = sql.Open("sqlite", filepath.Join(legacy, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	upgraded, err := OpenDB(legacy)
	if err != nil {
		t.Fatal(err)
	}
	upgraded.Close()
	snapshots, err := filepath.Glob(filepath.Join(legacy, "backups", "pre-upgrade-*", "knowledge.db"))
	if err != nil || len(snapshots) != 1 {
		t.Fatal("missing upgrade-before snapshot", snapshots, err)
	}
	before, err := sql.Open("sqlite", snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	var tables int
	if err = before.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='documents'").Scan(&tables); err != nil || tables != 0 {
		t.Fatal("snapshot taken after migration", tables, err)
	}
	before.Close()
	// A corrupt checksum must halt startup rather than silently accepting a
	// modified migration, and opening an unrelated DB must not overwrite it.
	unrelated := t.TempDir()
	raw, err = sql.Open("sqlite", filepath.Join(unrelated, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.Exec("CREATE TABLE other_data(value TEXT)")
	raw.Close()
	if unexpected, err := OpenDB(unrelated); err == nil {
		unexpected.Close()
		t.Fatal("unrelated database overwritten")
	}
	if _, err = os.Stat(filepath.Join(unrelated, "knowledge.db")); err != nil {
		t.Fatal(err)
	}
	checksumDir := t.TempDir()
	checksummed, err := OpenDB(checksumDir)
	if err != nil {
		t.Fatal(err)
	}
	checksummed.Exec("UPDATE schema_migrations SET checksum='changed'")
	checksummed.Close()
	if untrusted, err := OpenDB(checksumDir); err == nil {
		untrusted.Close()
		t.Fatal("migration checksum drift accepted")
	}
	missingDir := t.TempDir()
	missing, err := OpenDB(missingDir)
	if err != nil {
		t.Fatal(err)
	}
	missing.Exec("DROP TRIGGER documents_fts_au")
	missing.Close()
	if damaged, err := OpenDB(missingDir); err == nil {
		damaged.Close()
		t.Fatal("missing FTS trigger accepted")
	}
}

func TestCoreMigrationFailureRollsBackAndReportsSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knowledge.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// The conflicting documents table fails the migration after it has created
	// earlier tables, so this verifies transaction rollback rather than just
	// refusal before any migration SQL runs.
	if _, err = raw.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL);
CREATE TABLE documents(marker TEXT NOT NULL);
INSERT INTO documents(marker) VALUES('preserved');`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, migrationErr := OpenDB(dir)
	if db != nil || migrationErr == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("failed migration opened writable database", migrationErr)
	}
	snapshots, err := filepath.Glob(filepath.Join(dir, "backups", "pre-upgrade-*", "knowledge.db"))
	if err != nil || len(snapshots) != 1 {
		t.Fatal("missing recovery snapshot", snapshots, err)
	}
	if !strings.Contains(migrationErr.Error(), snapshots[0]) || !strings.Contains(migrationErr.Error(), "恢复快照") {
		t.Fatal("migration failure omitted recovery path", migrationErr)
	}
	for _, candidate := range []string{path, snapshots[0]} {
		check, err := sql.Open("sqlite", candidate)
		if err != nil {
			t.Fatal(err)
		}
		var marker string
		var version, migrationCount, partialTables int
		if err = check.QueryRow("SELECT marker FROM documents").Scan(&marker); err != nil || marker != "preserved" {
			check.Close()
			t.Fatal("original data changed", candidate, marker, err)
		}
		if err = check.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
			check.Close()
			t.Fatal("schema version changed", candidate, version, err)
		}
		if err = check.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil || migrationCount != 0 {
			check.Close()
			t.Fatal("migration record leaked", candidate, migrationCount, err)
		}
		if err = check.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('trash_batches','knowledge_bases')").Scan(&partialTables); err != nil || partialTables != 0 {
			check.Close()
			t.Fatal("partial migration tables leaked", candidate, partialTables, err)
		}
		if err = check.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
