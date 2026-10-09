package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func transferUpload(a *App, endpoint, name string, data []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://localhost"+endpoint, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-Filename", name)
	r.Header.Set("X-Data-Epoch", a.identity.Load().(identity).DataEpoch)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func transferWait(t *testing.T, a *App, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		w := securityRequest(a, "GET", "/api/v1/jobs/"+id, nil, nil)
		if w.Code != 200 {
			t.Fatalf("poll %d %s", w.Code, w.Body.String())
		}
		job := responseData(t, w)
		switch job["state"] {
		case "succeeded":
			return job
		case "failed", "cancelled":
			t.Fatalf("job failed: %s", w.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job timed out")
	return nil
}
func transferZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, v := range files {
		w, err := z.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func transferGBKName(t *testing.T, name string) string {
	t.Helper()
	encoded, err := simplifiedchinese.GBK.NewEncoder().String(name)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestTransferGBKNamesTreeLinksAndAttachments(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	docPath := "OrangePI/环境/OrangePi-AIpro-8T-环境搭建指南.md"
	childPath := "OrangePI/环境/其他文档.md"
	// GBK encodes 乗 with a 0x5c trail byte, which must not be mistaken for a backslash.
	assetPath := "OrangePI/环境/图片/乗.txt"
	data := transferZIP(t, map[string]string{
		transferGBKName(t, "OrangePI/环境/"): "",
		transferGBKName(t, docPath):        "# 安装系统\n\n[其他](其他文档.md)\n\n[附件](图片/乗.txt)\n",
		transferGBKName(t, childPath):      "中文正文",
		transferGBKName(t, assetPath):      "GBK 文件名附件内容",
		"UTF-8文件名.md":                      "UTF-8 正文",
	})
	p := coreStatus(t, transferUpload(a, "/api/v1/imports/preflight", "gbk.zip", data), 200)
	previewID := p["preview_id"].(string)
	var preview ImportPreview
	if err := readJSONFile(filepath.Join(a.DataDir, "tmp", "imports", previewID, "preview.json"), &preview); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, d := range preview.Documents {
		paths[d.Path] = true
	}
	if !paths[docPath] || !paths[childPath] || !paths["UTF-8文件名.md"] || len(preview.Attachments) != 1 || preview.Attachments[0].Path != assetPath {
		t.Fatal("decoded document or attachment paths were lost", preview)
	}
	jobID := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{
		"preview_id": previewID, "target_knowledge_base_id": kb, "expected_tree_revision": 1,
	}, nil), 202)["job_id"].(string)
	transferWait(t, a, jobID)
	var guideID, childID string
	if err := a.DB.QueryRow("SELECT id FROM documents WHERE title=?", "OrangePi-AIpro-8T-环境搭建指南").Scan(&guideID); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow("SELECT id FROM documents WHERE title=?", "其他文档").Scan(&childID); err != nil {
		t.Fatal(err)
	}
	guide, err := LoadDocument(context.Background(), a.DB, guideID, true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := LoadDocument(context.Background(), a.DB, childID, true)
	if err != nil {
		t.Fatal(err)
	}
	if guide.ParentID == nil || child.ParentID == nil || *guide.ParentID != *child.ParentID || !strings.Contains(guide.Markdown, "# 安装系统") || !strings.Contains(guide.Markdown, "/documents/"+childID) {
		t.Fatal("GBK tree or internal links were lost", guide, child)
	}
	attachments := RenderMarkdown(guide.Markdown).AttachmentIDs
	if len(attachments) != 1 {
		t.Fatal("GBK attachment link was not remapped", guide.Markdown)
	}
	content := securityRequest(a, "GET", "/api/v1/attachments/"+attachments[0]+"/content", nil, nil)
	if content.Code != 200 || content.Body.String() != "GBK 文件名附件内容" {
		t.Fatal("GBK attachment content lost", content.Code, content.Body.String())
	}
}

func TestTransferGBKRejectsUnsafeMalformedAndDuplicateNames(t *testing.T) {
	a := coreApp(t)
	cases := []struct {
		name   string
		files  map[string]string
		reason string
	}{
		{"traversal", map[string]string{transferGBKName(t, "../环境.md"): "bad"}, "unsafe archive path"},
		{"absolute", map[string]string{transferGBKName(t, "/环境.md"): "bad"}, "unsafe archive path"},
		{"backslash", map[string]string{transferGBKName(t, "环境\\坏.md"): "bad"}, "unsafe archive path"},
		{"drive", map[string]string{transferGBKName(t, "C:/环境.md"): "bad"}, "unsafe archive path"},
		{"mixed encoding collision", map[string]string{"环境.md": "utf8", transferGBKName(t, "环境.md"): "gbk"}, "duplicate normalized archive path"},
		{"incomplete GBK", map[string]string{"bad.md\x81": "bad"}, "archive filename encoding"},
		{"invalid GBK", map[string]string{"\xff.md": "bad"}, "archive filename encoding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := transferUpload(a, "/api/v1/imports/preflight", "bad.zip", transferZIP(t, tc.files))
			if w.Code != 422 || !strings.Contains(w.Body.String(), tc.reason) {
				t.Fatal("invalid archive not rejected correctly", w.Code, w.Body.String())
			}
			if tc.reason == "archive filename encoding" && !strings.Contains(w.Body.String(), "ZIP 文件名编码无效") {
				t.Fatal("encoding error reported as a dangerous path", w.Body.String())
			}
		})
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid archives created documents", count, err)
	}
}

func TestArchiveRejectsGBKNameDeclaredAsUTF8(t *testing.T) {
	f := &zip.File{FileHeader: zip.FileHeader{Name: transferGBKName(t, "环境.md"), Flags: 0x800}}
	if _, err := archiveFilePath(f); err == nil || !strings.Contains(err.Error(), "ZIP declares UTF-8") {
		t.Fatal("invalid declared UTF-8 name accepted", err)
	}
}

func transferAttachment(t *testing.T, a *App) Attachment {
	t.Helper()
	at, err := a.PrepareAttachment(strings.NewReader("test local attachment\n"), "资料.txt", AttachmentLimit)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = InsertAttachment(context.Background(), tx, at); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestTransferManifestRoundtripNewIDsTreeLinksAttachments(t *testing.T) {
	source := coreApp(t)
	kb := coreCreateKB(t, source)
	at := transferAttachment(t, source)
	parent := coreCreateDoc(t, source, kb, nil, "CON:同名", "# 正文 H1\n\n![资源](/api/v1/attachments/"+at.ID+"/content)\n")
	child := coreCreateDoc(t, source, kb, &parent.ID, "CON:同名", "[父文档](/documents/"+parent.ID+"#位置)\n\n正文中文")
	coreCreateDoc(t, source, kb, nil, "CON:同名", "另一个同名文档")
	w := securityRequest(source, "POST", "/api/v1/exports", map[string]any{"scope": "knowledge_base", "id": kb, "format": "markdown_zip", "include_attachments": true}, map[string]string{"Idempotency-Key": "roundtrip-export"})
	job := coreStatus(t, w, 202)["job_id"].(string)
	transferWait(t, source, job)
	download := securityRequest(source, "GET", "/api/v1/jobs/"+job+"/download", nil, nil)
	if download.Code != 200 {
		t.Fatal(download.Body.String())
	}
	target := coreApp(t)
	targetKB := coreCreateKB(t, target)
	previewData := coreStatus(t, transferUpload(target, "/api/v1/imports/preflight", "notes.zip", download.Body.Bytes()), 200)
	previewID := previewData["preview_id"].(string)
	req := map[string]any{"preview_id": previewID, "target_knowledge_base_id": targetKB, "target_parent_id": nil, "expected_tree_revision": 1}
	headers := map[string]string{"Idempotency-Key": "roundtrip-import"}
	first := securityRequest(target, "POST", "/api/v1/imports", req, headers)
	importID := coreStatus(t, first, 202)["job_id"].(string)
	result := transferWait(t, target, importID)
	retry := securityRequest(target, "POST", "/api/v1/imports", req, headers)
	if retry.Code != 202 || responseData(t, retry)["job_id"] != importID {
		t.Fatal("idempotent import response not retained", retry.Body.String())
	}
	mappings := map[string]string{}
	for _, v := range result["result"].(map[string]any)["documents"].([]any) {
		d := v.(map[string]any)
		mappings[d["source_id"].(string)] = d["id"].(string)
	}
	if len(mappings) != 3 || mappings[parent.ID] == parent.ID || mappings[child.ID] == child.ID {
		t.Fatal("content import must allocate new IDs", mappings)
	}
	newParent, err := LoadDocument(context.Background(), target.DB, mappings[parent.ID], true)
	if err != nil {
		t.Fatal(err)
	}
	newChild, err := LoadDocument(context.Background(), target.DB, mappings[child.ID], true)
	if err != nil {
		t.Fatal(err)
	}
	if newChild.ParentID == nil || *newChild.ParentID != newParent.ID || newParent.Title != parent.Title || !strings.Contains(newParent.Markdown, "# 正文 H1") || !strings.Contains(newChild.Markdown, "/documents/"+newParent.ID+"#") {
		t.Fatal("tree, title or source markdown was lost", newParent, newChild)
	}
	parsed := RenderMarkdown(newParent.Markdown)
	if len(parsed.AttachmentIDs) != 1 || parsed.AttachmentIDs[0] == at.ID {
		t.Fatal("resource ID was not remapped", parsed.AttachmentIDs)
	}
	content := securityRequest(target, "GET", "/api/v1/attachments/"+parsed.AttachmentIDs[0]+"/content", nil, nil)
	if content.Code != 200 || content.Body.String() != "test local attachment\n" {
		t.Fatal("resource content lost", content.Code, content.Body.String())
	}
	var count int
	target.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count)
	if count != 3 {
		t.Fatal("retry duplicated tree", count)
	}
	var persisted ImportPreview
	if err := readJSONFile(filepath.Join(target.DataDir, "tmp", "imports", previewID, "preview.json"), &persisted); err != nil {
		t.Fatal(err)
	}
	for _, d := range persisted.Documents {
		if d.Markdown != "" {
			t.Fatal("preview persisted full document bodies")
		}
	}
}
func TestTransferGenericDirectoriesMissingResourcesAndSafeArchives(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	data := transferZIP(t, map[string]string{"parent/index.md": "父正文\n[child](child.md)\n", "parent/child.md": "![asset](../resources/a.txt)\n", "resources/a.txt": "shared"})
	p := coreStatus(t, transferUpload(a, "/api/v1/imports/preflight", "generic.zip", data), 200)
	id := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{"preview_id": p["preview_id"], "target_knowledge_base_id": kb, "expected_tree_revision": 1}, nil), 202)["job_id"].(string)
	transferWait(t, a, id)
	var count int
	a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count)
	if count != 2 {
		t.Fatal("directory/index handling", count)
	}
	missing := transferUpload(a, "/api/v1/imports/preflight", "missing.zip", transferZIP(t, map[string]string{"x.md": "![x](missing.png)"}))
	if missing.Code != 422 || !strings.Contains(missing.Body.String(), "IMPORT_PREFLIGHT_FAILED") {
		t.Fatal("missing resource accepted", missing.Code, missing.Body.String())
	}
	cases := []struct {
		name  string
		files map[string]string
	}{{"traversal", map[string]string{"../outside.md": "bad"}}, {"absolute", map[string]string{"/outside.md": "bad"}}, {"windows", map[string]string{"C:/outside.md": "bad"}}, {"backslash", map[string]string{"..\\outside.md": "bad"}}, {"case collision", map[string]string{"A.md": "a", "a.md": "b"}}, {"depth", map[string]string{strings.Repeat("deep/", 34) + "x.md": "bad"}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := transferUpload(a, "/api/v1/imports/preflight", "evil.zip", transferZIP(t, tc.files))
			if w.Code < 400 {
				t.Fatal("evil archive accepted", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), a.DataDir) {
				t.Fatal("server path leaked", w.Body.String())
			}
		})
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "x.md", Method: zip.Store}
	h.SetMode(os.ModeSymlink | 0777)
	out, _ := z.CreateHeader(h)
	io.WriteString(out, "/etc/passwd")
	z.Close()
	if w := transferUpload(a, "/api/v1/imports/preflight", "symlink.zip", b.Bytes()); w.Code < 400 {
		t.Fatal("symlink accepted")
	}
	small := filepath.Join(t.TempDir(), "small.zip")
	os.WriteFile(small, transferZIP(t, map[string]string{"x.md": strings.Repeat("x", 1000)}), 0600)
	if _, err := extractArchive(small, t.TempDir(), archiveLimits{Bytes: 10, Files: 10, Depth: 32}); err == nil {
		t.Fatal("expanded-byte limit ignored")
	}
	a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count)
	if count != 2 {
		t.Fatal("invalid import left partial documents", count)
	}
}

func TestTransferWrapperFolderAndImageLinks(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	guidePath := "OrangePI/OrangePi-AIpro-8T-环境搭建指南.md"
	data := transferZIP(t, map[string]string{
		guidePath:                   "# 安装\n\n![图片](images/setup.svg)\n[子文档](notes/child.md)",
		"OrangePI/notes/child.md":   "[返回](../OrangePi-AIpro-8T-环境搭建指南.md)",
		"OrangePI/images/setup.svg": `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>`,
	})
	response := coreStatus(t, transferUpload(a, "/api/v1/imports/preflight", "OrangePI.zip", data), 200)
	var preview ImportPreview
	if err := readJSONFile(filepath.Join(a.DataDir, "tmp", "imports", response["preview_id"].(string), "preview.json"), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Documents) != 3 || len(preview.Attachments) != 1 {
		t.Fatal("wrapper or resource directory became a document", preview)
	}
	for _, doc := range preview.Documents {
		if doc.Title == "OrangePI" || doc.Title == "images" || (doc.Path == guidePath && doc.ParentSourceID != nil) {
			t.Fatal("packaging folder was retained", preview)
		}
	}
	jobID := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{
		"preview_id": response["preview_id"], "target_knowledge_base_id": kb, "expected_tree_revision": 1,
	}, nil), 202)["job_id"].(string)
	transferWait(t, a, jobID)
	var guideID, childID string
	if err := a.DB.QueryRow("SELECT id FROM documents WHERE title=?", "OrangePi-AIpro-8T-环境搭建指南").Scan(&guideID); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow("SELECT id FROM documents WHERE title=?", "child").Scan(&childID); err != nil {
		t.Fatal(err)
	}
	guide, err := LoadDocument(context.Background(), a.DB, guideID, true)
	if err != nil || guide.ParentID != nil || !strings.Contains(guide.Markdown, "/documents/"+childID) || len(RenderMarkdown(guide.Markdown).AttachmentIDs) != 1 {
		t.Fatal("wrapper removal broke root placement or links", guide, err)
	}
	child, err := LoadDocument(context.Background(), a.DB, childID, true)
	if err != nil || !strings.Contains(child.Markdown, "/documents/"+guideID) {
		t.Fatal("relative parent link was lost", child, err)
	}
}

func TestTransferWrapperFolderPreservesIndexAndMultipleRoots(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		count int
	}{
		{"single guide", map[string]string{"OrangePI/guide.md": "guide", "OrangePI/images/a.txt": "asset"}, 1},
		{"real index", map[string]string{"OrangePI/INDEX.markdown": "index", "OrangePI/guide.md": "guide"}, 2},
		{"multiple roots", map[string]string{"one/a.md": "one", "two/b.md": "two"}, 4},
		{"root document", map[string]string{"root.md": "root", "OrangePI/guide.md": "guide"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			upload := filepath.Join(dir, "upload.zip")
			if err := os.WriteFile(upload, transferZIP(t, tc.files), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := preflightImport(upload, "notes.zip", filepath.Join(dir, "files"))
			if err != nil || len(p.Documents) != tc.count {
				t.Fatal("unexpected wrapper handling", p, err)
			}
		})
	}
}

func TestTransferTextExportToleratesMissingResourceAndKeepsH1(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	at := transferAttachment(t, a)
	d := coreCreateDoc(t, a, kb, nil, "独立标题", "# 正文标题\n\n![资源](/api/v1/attachments/"+at.ID+"/content)")
	os.Remove(filepath.Join(a.DataDir, at.StoragePath))
	id := coreStatus(t, securityRequest(a, "POST", "/api/v1/exports", map[string]any{"scope": "document", "id": d.ID, "format": "md"}, nil), 202)["job_id"].(string)
	job := transferWait(t, a, id)
	if len(job["result"].(map[string]any)["unresolved_links"].([]any)) == 0 {
		t.Fatal("missing attachment warning absent")
	}
	w := securityRequest(a, "GET", "/api/v1/jobs/"+id+"/download", nil, nil)
	if !strings.HasPrefix(w.Body.String(), "# 独立标题\n\n# 正文标题") {
		t.Fatal("text export changed body H1", w.Body.String())
	}
}

func TestTransferManifestCycleHashAndUnexpectedFiles(t *testing.T) {
	a := coreApp(t)
	one, two := NewID(), NewID()
	m := ContentManifest{Format: "ljmdb-content", FormatVersion: 1, Documents: []ContentDocument{{SourceID: one, Title: "one", ParentSourceID: &two, Path: "documents/a.md"}, {SourceID: two, Title: "two", ParentSourceID: &one, Path: "documents/b.md"}}, Attachments: []ContentAttachment{}, UnresolvedLinks: []string{}}
	j, _ := json.Marshal(m)
	w := transferUpload(a, "/api/v1/imports/preflight", "cycle.zip", transferZIP(t, map[string]string{"manifest.json": string(j), "documents/a.md": "a", "documents/b.md": "b"}))
	if w.Code != 422 {
		t.Fatal("cyclic manifest accepted", w.Code, w.Body.String())
	}
	m.Documents[0].ParentSourceID = nil
	m.Documents[1].ParentSourceID = &one
	j, _ = json.Marshal(m)
	w = transferUpload(a, "/api/v1/imports/preflight", "extra.zip", transferZIP(t, map[string]string{"manifest.json": string(j), "documents/a.md": "a", "documents/b.md": "b", "extra.txt": "unexpected"}))
	if w.Code != 422 {
		t.Fatal("manifest extra file accepted", w.Code, w.Body.String())
	}
}

func TestTransferFailedCommitRegistersPreparedFilesWithoutPartialTree(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	p := coreStatus(t, transferUpload(a, "/api/v1/imports/preflight", "notes.zip", transferZIP(t, map[string]string{"x.md": "[resource](a.txt)", "a.txt": "original"})), 200)
	id := p["preview_id"].(string)
	if err := os.WriteFile(filepath.Join(a.DataDir, "tmp", "imports", id, "files", "a.txt"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	accepted := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{"preview_id": id, "target_knowledge_base_id": kb, "expected_tree_revision": 1}, nil), 202)
	jobID := accepted["job_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := securityRequest(a, "GET", "/api/v1/jobs/"+jobID, nil, nil)
		job := coreStatus(t, w, 200)
		if job["state"] == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.Mu.RLock()
	a.WriteMu.Lock()
	defer a.Mu.RUnlock()
	defer a.WriteMu.Unlock()
	var docs, attachments, refs int
	a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&docs)
	a.DB.QueryRow("SELECT count(*) FROM attachments WHERE state='ready'").Scan(&attachments)
	a.DB.QueryRow("SELECT count(*) FROM document_attachments").Scan(&refs)
	if docs != 0 || refs != 0 || attachments != 1 {
		t.Fatal("failed import left partial tree or lost prepared candidate", docs, refs, attachments)
	}
}

func TestTransferJobDownloadRejectsRestoredMetadataFileAccess(t *testing.T) {
	a := coreApp(t)
	id := NewID()
	_, err := a.DB.Exec("INSERT INTO operation_jobs(id,kind,state,progress,result_path,created_at,updated_at) VALUES(?,'export','succeeded',100,'knowledge.db',?,?)", id, Now(), Now())
	if err != nil {
		t.Fatal(err)
	}
	w := securityRequest(a, "GET", "/api/v1/jobs/"+id+"/download", nil, nil)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "INVALID_JOB_RESULT") {
		t.Fatal("job metadata allowed arbitrary data file read", w.Code, w.Body.String())
	}
}

func multipartImport(t *testing.T, a *App, names []string, bodies [][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i, name := range names {
		part, err := form.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bodies[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/v1/imports/preflight", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("X-Data-Epoch", a.identity.Load().(identity).DataEpoch)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestMultipartImportBatchLinksAndRejections(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	preview := coreStatus(t, multipartImport(t, a, []string{"一.md", "二.markdown"}, [][]byte{[]byte("[第二篇](二.markdown)"), []byte("第二篇正文")}), 200)
	if len(preview["documents"].([]any)) != 2 {
		t.Fatal(preview)
	}
	id := coreStatus(t, securityRequest(a, "POST", "/api/v1/imports", map[string]any{"preview_id": preview["preview_id"], "target_knowledge_base_id": kb, "expected_tree_revision": 1}, nil), 202)["job_id"].(string)
	job := transferWait(t, a, id)
	var one, two Document
	for _, v := range job["result"].(map[string]any)["documents"].([]any) {
		d, err := LoadDocument(context.Background(), a.DB, v.(map[string]any)["id"].(string), true)
		if err != nil {
			t.Fatal(err)
		}
		if d.Title == "一" {
			one = d
		} else {
			two = d
		}
	}
	if one.ID == "" || two.Markdown != "第二篇正文" || !strings.Contains(one.Markdown, "/documents/"+two.ID) {
		t.Fatal("batch content or links lost", one, two)
	}
	zipData := transferZIP(t, map[string]string{"三.md": "第三篇正文"})
	coreStatus(t, multipartImport(t, a, []string{"notes.zip"}, [][]byte{zipData}), 200)
	for _, tc := range []struct {
		names  []string
		bodies [][]byte
	}{
		{[]string{"a.md", "A.md"}, [][]byte{[]byte("one"), []byte("two")}},
		{[]string{"a.md", "b.md"}, [][]byte{[]byte("one"), {0xff}}},
		{[]string{"a.md", "notes.zip"}, [][]byte{[]byte("one"), zipData}},
		{[]string{"notes.zip", "a.md"}, [][]byte{zipData, []byte("one")}},
	} {
		before, err := os.ReadDir(filepath.Join(a.DataDir, "tmp", "imports"))
		if err != nil {
			t.Fatal(err)
		}
		coreStatus(t, multipartImport(t, a, tc.names, tc.bodies), 422)
		after, err := os.ReadDir(filepath.Join(a.DataDir, "tmp", "imports"))
		if err != nil {
			t.Fatal(err)
		}
		if len(before) != len(after) {
			t.Fatal("failed preview left staged files")
		}
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("rejected batch wrote documents", count)
	}
}
