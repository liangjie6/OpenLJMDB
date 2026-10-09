package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assetsUpload(t *testing.T, a *App, name string, content []byte) Attachment {
	w := assetsUploadRequest(t, a, name, content, "")
	if w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data Attachment `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func assetsUploadRequest(t *testing.T, a *App, name string, content []byte, key string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	mw.Close()
	r := httptest.NewRequest("POST", "http://localhost/api/v1/attachments", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-Data-Epoch", a.identity.Load().(identity).DataEpoch)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestAttachmentIdempotencyAndPendingUploadRetry(t *testing.T) {
	a := coreApp(t)
	first := assetsUploadRequest(t, a, "same.txt", []byte("bytes"), "upload-1")
	second := assetsUploadRequest(t, a, "same.txt", []byte("bytes"), "upload-1")
	if first.Code != 201 || second.Code != 201 || responseData(t, first)["id"] != responseData(t, second)["id"] {
		t.Fatal("retry duplicate", first.Body.String(), second.Body.String())
	}
	for _, change := range []struct{ Name, Body string }{{"renamed.txt", "bytes"}, {"same.txt", "different"}} {
		w := assetsUploadRequest(t, a, change.Name, []byte(change.Body), "upload-1")
		if w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_KEY_REUSED") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Simulate a durable pending ID with a missing file after an interrupted
	// rename. A new multipart boundary and stream must finish that same ID.
	a.WriteMu.Lock()
	a.FileMu.Lock()
	pending, tmp, err := a.stageAttachment(strings.NewReader("retry bytes"), "retry.txt", 100)
	a.FileMu.Unlock()
	if err != nil {
		a.WriteMu.Unlock()
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(struct {
		Name, SHA256 string
		Size         int64
	}{pending.OriginalName, pending.SHA256, pending.SizeBytes})
	hash := sha256.Sum256(canonical)
	record, _ := attachmentResponseRecord(202, pending)
	tx, err := a.DB.Begin()
	if err == nil {
		_, err = tx.Exec("INSERT INTO attachments(id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',?,?)", pending.ID, pending.OriginalName, pending.StoragePath, pending.MediaType, pending.SizeBytes, pending.SHA256, pending.CreatedAt, pending.UpdatedAt)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO idempotency_records(scope,idempotency_key,request_hash,response_json,created_at) VALUES('POST /api/v1/attachments','upload-interrupted',?,?,?)", hex.EncodeToString(hash[:]), string(record), Now())
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		tx.Rollback()
	}
	os.Remove(tmp)
	a.WriteMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	w := assetsUploadRequest(t, a, "retry.txt", []byte("retry bytes"), "upload-interrupted")
	if w.Code != 201 || responseData(t, w)["id"] != pending.ID {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if err = a.DB.QueryRow("SELECT count(*) FROM attachments").Scan(&count); err != nil || count != 2 {
		t.Fatal("duplicate pending attachment", count, err)
	}
	content := securityRequest(a, "GET", "/api/v1/attachments/"+pending.ID+"/content", nil, nil)
	if content.Code != 200 || content.Body.String() != "retry bytes" {
		t.Fatal(content.Code, content.Body.String())
	}
}

type blockingAttachmentWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	once    bool
}

func (w *blockingAttachmentWriter) Write(b []byte) (int, error) {
	if !w.once {
		w.once = true
		close(w.started)
		<-w.release
	}
	return w.ResponseRecorder.Write(b)
}

func TestAttachmentCleanupWaitsForActiveDownload(t *testing.T) {
	a := coreApp(t)
	at := assetsUpload(t, a, "download.bin", bytes.Repeat([]byte("data"), 10000))
	recorder := &blockingAttachmentWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
	downloadDone := make(chan struct{})
	go func() {
		r := httptest.NewRequest("GET", "http://localhost"+at.URL, nil)
		a.Handler().ServeHTTP(recorder, r)
		close(downloadDone)
	}()
	select {
	case <-recorder.started:
	case <-time.After(2 * time.Second):
		t.Fatal("download did not start")
	}
	preview := coreStatus(t, securityRequest(a, "POST", "/api/v1/attachments/cleanup-preview", map[string]any{"attachment_ids": []string{at.ID}}, nil), 200)
	coreStatus(t, securityRequest(a, "POST", "/api/v1/attachments/cleanup", map[string]any{"attachment_ids": []string{at.ID}, "confirmation_token": preview["confirmation_token"]}, nil), 202)
	drainDone := make(chan error, 1)
	go func() {
		a.Mu.RLock()
		a.WriteMu.Lock()
		err := a.drainCleanup(context.Background())
		a.WriteMu.Unlock()
		a.Mu.RUnlock()
		drainDone <- err
	}()
	select {
	case err := <-drainDone:
		close(recorder.release)
		t.Fatalf("cleanup completed while download active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	full, _ := a.SafeAttachmentPath(at.ID, attachmentRelativePath(at.ID))
	if _, err := os.Stat(full); err != nil {
		close(recorder.release)
		t.Fatal("file removed during download", err)
	}
	close(recorder.release)
	select {
	case <-downloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("download blocked")
	}
	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not resume")
	}
	if recorder.Code != 200 || recorder.Body.Len() != int(at.SizeBytes) {
		t.Fatal("download truncated", recorder.Code, recorder.Body.Len())
	}
}

func TestAttachmentCleanupCompletesAfterFileWasAlreadyRemoved(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := assetsUpload(t, a, "interrupted.bin", []byte("delete task"))
	a.Mu.RLock()
	a.WriteMu.Lock()
	_, err = a.DB.Exec("UPDATE attachments SET state='pending_delete' WHERE id=?", at.ID)
	if err == nil {
		_, err = a.DB.Exec("INSERT INTO cleanup_tasks(id,attachment_id,state,attempts,created_at,updated_at) VALUES(?,?,'running',1,?,?)", NewID(), at.ID, Now(), Now())
	}
	full, _ := a.SafeAttachmentPath(at.ID, attachmentRelativePath(at.ID))
	if err == nil {
		err = os.Remove(full)
	}
	a.WriteMu.Unlock()
	a.Mu.RUnlock()
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var state, taskState string
	if err = a.DB.QueryRow("SELECT a.state,t.state FROM attachments a JOIN cleanup_tasks t ON t.attachment_id=a.id WHERE a.id=?", at.ID).Scan(&state, &taskState); err != nil || state != "deleted" || taskState != "done" {
		t.Fatal(state, taskState, err)
	}
}

func TestAttachmentCleanupCancelsWhenReferencesUnexpectedlyExist(t *testing.T) {
	a := coreApp(t)
	at := assetsUpload(t, a, "kept.bin", []byte("still referenced"))
	kb := coreCreateKB(t, a)
	coreCreateDoc(t, a, kb, nil, "保留", "[引用]("+at.URL+")")
	a.Mu.RLock()
	a.WriteMu.Lock()
	taskID := NewID()
	_, err := a.DB.Exec("UPDATE attachments SET state='pending_delete' WHERE id=?", at.ID)
	if err == nil {
		_, err = a.DB.Exec("INSERT INTO cleanup_tasks(id,attachment_id,state,created_at,updated_at) VALUES(?,?,'queued',?,?)", taskID, at.ID, Now(), Now())
	}
	if err == nil {
		err = a.drainCleanup(context.Background())
	}
	a.WriteMu.Unlock()
	a.Mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	var state, taskState string
	if err = a.DB.QueryRow("SELECT a.state,t.state FROM attachments a JOIN cleanup_tasks t ON t.attachment_id=a.id WHERE t.id=?", taskID).Scan(&state, &taskState); err != nil || state != "ready" || taskState != "cancelled" {
		t.Fatal(state, taskState, err)
	}
	w := securityRequest(a, "GET", at.URL, nil, nil)
	if w.Code != 200 || w.Body.String() != "still referenced" {
		t.Fatal("worker deleted referenced file", w.Code, w.Body.String())
	}
}

func TestAttachmentTypeLimitsAndPathSafety(t *testing.T) {
	a := coreApp(t)
	html := assetsUpload(t, a, "危险.html", []byte("<html><script>alert(1)</script></html>"))
	w := securityRequest(a, "GET", html.URL, nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	head := securityRequest(a, "HEAD", html.URL, nil, nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatal(head.Code, head.Header(), head.Body.String())
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	pngFile := assetsUpload(t, a, "像素.png", picture.Bytes())
	w = securityRequest(a, "GET", pngFile.URL, nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline;") {
		t.Fatal(w.Code, w.Header())
	}
	if _, err := a.PrepareAttachment(strings.NewReader("12345678901"), "too-large.bin", 10); err == nil {
		t.Fatal("capacity ignored")
	}
	if _, err := a.PrepareAttachment(strings.NewReader("x"), "bad\nname", 10); err == nil {
		t.Fatal("control character accepted")
	}
	full, err := a.SafeAttachmentPath(pngFile.ID, attachmentRelativePath(pngFile.ID))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err = os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, full); err != nil {
		t.Skip("symlinks unavailable")
	}
	w = securityRequest(a, "GET", pngFile.URL, nil, nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("followed symlink", w.Code, w.Body.String())
	}
}

func TestAttachmentSharedHistoryTrashAndConfirmedCleanup(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	at := assetsUpload(t, a, "shared.txt", []byte("shared attachment"))
	markdown := "[文件](" + at.URL + ")"
	first := coreCreateDoc(t, a, kb, nil, "一", markdown)
	second := coreCreateDoc(t, a, kb, nil, "二", markdown)
	coreStatus(t, securityRequest(a, "PUT", "/api/v1/documents/"+first.ID, map[string]any{"title": "一", "markdown": "new body", "expected_revision": 1, "snapshot": true}, nil), 200)
	preview := coreStatus(t, securityRequest(a, "POST", "/api/v1/attachments/cleanup-preview", map[string]any{"attachment_ids": []string{at.ID}}, nil), 200)
	if preview["can_delete"] != false {
		t.Fatal("history/shared attachment deletable", preview)
	}
	batch := coreDeleteDoc(t, a, first)
	token, _ := coreTrashToken(t, a, batch)
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash/"+batch, map[string]any{"confirmation_token": token}, nil), 200)
	if w := securityRequest(a, "GET", at.URL, nil, nil); w.Code != 200 {
		t.Fatal("copy lost after original purge", w.Body.String())
	}
	batch = coreDeleteDoc(t, a, second)
	preview = coreStatus(t, securityRequest(a, "POST", "/api/v1/attachments/cleanup-preview", map[string]any{"attachment_ids": []string{at.ID}}, nil), 200)
	if preview["can_delete"] != false {
		t.Fatal("trash ref ignored", preview)
	}
	token, _ = coreTrashToken(t, a, batch)
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash/"+batch, map[string]any{"confirmation_token": token}, nil), 200)
	preview = coreStatus(t, securityRequest(a, "POST", "/api/v1/attachments/cleanup-preview", map[string]any{"attachment_ids": []string{at.ID}}, nil), 200)
	if preview["can_delete"] != true {
		t.Fatal(preview)
	}
	coreStatus(t, securityRequest(a, "POST", "/api/v1/attachments/cleanup", map[string]any{"attachment_ids": []string{at.ID}, "confirmation_token": preview["confirmation_token"]}, nil), 202)
	create := securityRequest(a, "POST", "/api/v1/knowledge-bases/"+kb+"/documents", map[string]any{"parent_id": nil, "title": "不能引用待删除", "markdown": markdown, "expected_tree_revision": coreTreeRevision(t, a, kb)}, nil)
	if create.Code != 409 {
		t.Fatal("pending delete acquired reference", create.Code, create.Body.String())
	}
	a.Mu.RLock()
	a.WriteMu.Lock()
	err := a.drainCleanup(context.Background())
	a.WriteMu.Unlock()
	a.Mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	var state string
	if err = a.DB.QueryRow("SELECT state FROM attachments WHERE id=?", at.ID).Scan(&state); err != nil || state != "deleted" {
		t.Fatal(state, err)
	}
	if w := securityRequest(a, "GET", at.URL, nil, nil); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAttachmentStartupCrashRecoveryAndNoAutomaticOrphanDeletion(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.FileMu.Lock()
	pending, tmp, err := a.stageAttachment(strings.NewReader("interrupted"), "pending.txt", 100)
	a.FileMu.Unlock()
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	_, err = a.DB.Exec("INSERT INTO attachments(id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',?,?)", pending.ID, pending.OriginalName, pending.StoragePath, pending.MediaType, pending.SizeBytes, pending.SHA256, pending.CreatedAt, pending.UpdatedAt)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(struct {
		Name, SHA256 string
		Size         int64
	}{pending.OriginalName, pending.SHA256, pending.SizeBytes})
	digest := sha256.Sum256(canonical)
	record, _ := attachmentResponseRecord(202, pending)
	if _, err = a.DB.Exec("INSERT INTO idempotency_records(scope,idempotency_key,request_hash,response_json,created_at) VALUES('POST /api/v1/attachments','restart-upload',?,?,?)", hex.EncodeToString(digest[:]), string(record), Now()); err != nil {
		a.Close()
		t.Fatal(err)
	}
	orphan, err := a.PrepareAttachment(strings.NewReader("orphan bytes"), "orphan.txt", 100)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	missing := assetsUpload(t, a, "missing.txt", []byte("removed"))
	missingPath, _ := a.SafeAttachmentPath(missing.ID, attachmentRelativePath(missing.ID))
	if err = os.Remove(missingPath); err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for id, want := range map[string]string{pending.ID: "ready", orphan.ID: "ready", missing.ID: "missing"} {
		var state string
		if err = a.DB.QueryRow("SELECT state FROM attachments WHERE id=?", id).Scan(&state); err != nil || state != want {
			t.Fatalf("recover %s=%s want=%s err=%v", id, state, want, err)
		}
	}
	if _, err = os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("pending temp not renamed", err)
	}
	orphanPath, _ := a.SafeAttachmentPath(orphan.ID, orphan.StoragePath)
	if _, err = os.Stat(orphanPath); err != nil {
		t.Fatal("orphan deleted automatically", err)
	}
	w := assetsUploadRequest(t, a, "pending.txt", []byte("interrupted"), "restart-upload")
	if w.Code != 201 || responseData(t, w)["id"] != pending.ID {
		t.Fatal("restart retry changed ID", w.Code, w.Body.String())
	}
}

func TestSearchChineseLiteralPriorityPaginationAndDeletion(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	exact := coreCreateDoc(t, a, kb, nil, "中文", "plain")
	coreCreateDoc(t, a, kb, nil, "中文知识", "other")
	body := coreCreateDoc(t, a, kb, nil, "正文", "🙂前缀中国中文知识；含百分号100%和下划线a_b以及literalOR OR")
	search := func(q string, page int) ([]SearchResult, map[string]any) {
		t.Helper()
		path := "/api/v1/search?q=" + url.QueryEscape(q) + "&page_size=1"
		if page > 1 {
			path += "&page=" + strconv.Itoa(page)
		}
		w := securityRequest(a, "GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Data []SearchResult `json:"data"`
			Meta map[string]any `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data, response.Meta
	}
	results, meta := search("中文", 1)
	if meta["total"] != float64(3) || len(results) != 1 || results[0].DocumentID != exact.ID {
		t.Fatal(results, meta)
	}
	second, _ := search("中文", 2)
	if len(second) != 1 || second[0].DocumentID == exact.ID {
		t.Fatal(second)
	}
	outside, outsideMeta := search("中文", 20)
	if len(outside) != 0 || outsideMeta["total"] != float64(3) {
		t.Fatal("out-of-range total lost", outside, outsideMeta)
	}
	for _, q := range []string{"100%", "a_b", "OR"} {
		results, meta = search(q, 1)
		if meta["total"] != float64(1) || len(results) != 1 || results[0].DocumentID != body.ID {
			t.Fatal(q, results, meta)
		}
	}
	results, _ = search("中国中文", 1)
	if len(results) != 1 || len(results[0].Highlights) != 1 {
		t.Fatal(results)
	}
	mark := results[0].Highlights[0]
	if string([]rune(results[0].Snippet)[mark.Start:mark.End]) != "中国中文" {
		t.Fatal("highlight uses bytes", results)
	}
	for _, q := range []string{`" OR title:*`, "%", "_", `\\`} {
		search(q, 1)
	}
	coreDeleteDoc(t, a, body)
	results, meta = search("100%", 1)
	if meta["total"] != float64(0) || len(results) != 0 {
		t.Fatal("deleted document searchable", results, meta)
	}
}
