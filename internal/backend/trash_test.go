package backend

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func emptyTrashToken(t *testing.T, a *App) (string, map[string]any) {
	t.Helper()
	data := coreStatus(t, securityRequest(a, "GET", "/api/v1/trash/empty-preview", nil, nil), 200)
	return data["confirmation_token"].(string), data["delete_preview"].(map[string]any)
}

func TestEmptyTrashSameLibraryAndOverlappingBatches(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	shared := transferAttachment(t, a)
	orphan := transferAttachment(t, a)
	sharedLink := "[shared](/api/v1/attachments/" + shared.ID + "/content)"
	orphanLink := "[orphan](/api/v1/attachments/" + orphan.ID + "/content)"
	live := coreCreateDoc(t, a, kb, nil, "保留文档", sharedLink)
	parent := coreCreateDoc(t, a, kb, nil, "父文档", "parent")
	child := coreCreateDoc(t, a, kb, &parent.ID, "独立删除子文档", sharedLink)
	sibling := coreCreateDoc(t, a, kb, nil, "同库其他删除批次", "sibling")
	coreDeleteDoc(t, a, child)
	coreDeleteDoc(t, a, parent)
	coreDeleteDoc(t, a, sibling)
	deletedKB := coreCreateKB(t, a)
	root := coreCreateDoc(t, a, deletedKB, nil, "整库删除父文档", orphanLink)
	leaf := coreCreateDoc(t, a, deletedKB, &root.ID, "先删除子文档", orphanLink)
	coreStatus(t, securityRequest(a, "PUT", "/api/v1/documents/"+root.ID, map[string]any{"title": root.Title, "markdown": orphanLink + " updated", "expected_revision": 1}, nil), 200)
	coreDeleteDoc(t, a, leaf)
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/knowledge-bases/"+deletedKB, map[string]any{"expected_tree_revision": coreTreeRevision(t, a, deletedKB)}, nil), 200)
	emptyKB := coreCreateKB(t, a)
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/knowledge-bases/"+emptyKB, map[string]any{"expected_tree_revision": 1}, nil), 200)
	token, preview := emptyTrashToken(t, a)
	if preview["batch_count"] != float64(6) || preview["knowledge_base_count"] != float64(2) || preview["document_count"] != float64(5) || preview["history_count"] != float64(1) {
		t.Fatal("overlapping batches counted twice", preview)
	}
	result := coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil), 200)
	if result["purged_batch_count"] != float64(6) || result["purged_document_count"] != float64(5) || result["history_count"] != float64(1) {
		t.Fatal(result)
	}
	candidates := result["attachment_cleanup_candidates"].([]any)
	if len(candidates) != 1 || candidates[0] != orphan.ID {
		t.Fatal("shared attachment retention or candidate deduplication", candidates)
	}
	retained, err := LoadDocument(context.Background(), a.DB, live.ID, true)
	if err != nil || retained.Markdown != sharedLink {
		t.Fatal("live document changed", retained, err)
	}
	for query, want := range map[string]int{
		"SELECT count(*) FROM documents":                                                     1,
		"SELECT count(*) FROM knowledge_bases":                                               1,
		"SELECT count(*) FROM document_revisions":                                            0,
		"SELECT count(*) FROM trash_batches WHERE purged_at IS NOT NULL":                     6,
		"SELECT count(*) FROM trash_batches WHERE purged_at IS NULL AND restored_at IS NULL": 0,
		"SELECT count(*) FROM pragma_foreign_key_check":                                      0,
	} {
		var got int
		if err := a.DB.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatal(query, got, err)
		}
	}
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil), 409)
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{}, nil), 400)
	token, preview = emptyTrashToken(t, a)
	if preview["batch_count"] != float64(0) {
		t.Fatal(preview)
	}
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil), 200)
}

func TestEmptyTrashStaleConfirmationRejectsEntireOperation(t *testing.T) {
	for _, change := range []string{"new_batch", "restored_batch", "content"} {
		t.Run(change, func(t *testing.T) {
			a := coreApp(t)
			kb := coreCreateKB(t, a)
			one := coreCreateDoc(t, a, kb, nil, "一", "one")
			two := coreCreateDoc(t, a, kb, nil, "二", "two")
			first := coreDeleteDoc(t, a, one)
			coreDeleteDoc(t, a, two)
			token, _ := emptyTrashToken(t, a)
			switch change {
			case "new_batch":
				three := coreCreateDoc(t, a, kb, nil, "三", "three")
				coreDeleteDoc(t, a, three)
			case "restored_batch":
				coreStatus(t, securityRequest(a, "POST", "/api/v1/trash/"+first+"/restore", map[string]any{"expected_tree_revision": coreTreeRevision(t, a, kb)}, nil), 200)
			case "content":
				if _, err := a.DB.Exec("UPDATE documents SET markdown='modified' WHERE id=?", two.ID); err != nil {
					t.Fatal(err)
				}
			}
			w := securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil)
			coreStatus(t, w, 409)
			if !strings.Contains(w.Body.String(), "CONFIRMATION_EXPIRED") {
				t.Fatal(w.Body.String())
			}
			for _, id := range []string{one.ID, two.ID} {
				if _, err := LoadDocument(context.Background(), a.DB, id, false); err != nil {
					t.Fatal("stale preview partially deleted trash", err)
				}
			}
		})
	}
}

func TestEmptyTrashRollbackAfterEarlierBatchWasPurged(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	for _, title := range []string{"一", "二"} {
		d := coreCreateDoc(t, a, kb, nil, title, title)
		coreDeleteDoc(t, a, d)
	}
	token, _ := emptyTrashToken(t, a)
	_, scopes, err := buildEmptyTrashPreview(context.Background(), a.DB)
	if err != nil {
		t.Fatal(err)
	}
	lastID := scopes[1].documents[0].ID
	if _, err := a.DB.Exec(fmt.Sprintf("CREATE TRIGGER fail_last_purge BEFORE DELETE ON documents WHEN OLD.id='%s' BEGIN SELECT RAISE(ABORT,'test failure'); END", lastID)); err != nil {
		t.Fatal(err)
	}
	w := securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil)
	if w.Code < 400 {
		t.Fatal("failure was not returned", w.Body.String())
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 2 {
		t.Fatal("partial purge committed", count, err)
	}
	if err := a.DB.QueryRow("SELECT count(*) FROM trash_batches WHERE purged_at IS NOT NULL").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial purge marked batches", count, err)
	}
	if _, err := a.DB.Exec("DROP TRIGGER fail_last_purge"); err != nil {
		t.Fatal(err)
	}
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil), 200)
}

func TestEmptyTrashIncludesBatchesBeyondFirstPage(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	for i := 0; i < 51; i++ {
		d := coreCreateDoc(t, a, kb, nil, fmt.Sprintf("文档 %d", i), "body")
		coreDeleteDoc(t, a, d)
	}
	token, preview := emptyTrashToken(t, a)
	if preview["batch_count"] != float64(51) || preview["document_count"] != float64(51) {
		t.Fatal(preview)
	}
	coreStatus(t, securityRequest(a, "DELETE", "/api/v1/trash", map[string]any{"confirmation_token": token}, nil), 200)
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 0 {
		t.Fatal("only cleared first page", count, err)
	}
}
