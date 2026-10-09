package backend

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
)

const trashColumns = "b.id,b.kind,b.root_document_id,b.knowledge_base_id,b.created_at,b.restored_at,b.purged_at,CASE WHEN b.kind='knowledge_base' THEN COALESCE(k.name,'已永久删除的知识库') ELSE COALESCE(d.title,'已永久删除的文档') END,(SELECT count(*) FROM documents m WHERE m.delete_batch_id=b.id)"

func scanTrash(row rowScanner) (TrashBatch, error) {
	var b TrashBatch
	err := row.Scan(&b.ID, &b.Kind, &b.RootDocumentID, &b.KnowledgeBaseID, &b.CreatedAt, &b.RestoredAt, &b.PurgedAt, &b.Title, &b.DocumentCount)
	return b, err
}
func loadTrash(ctx context.Context, q Queryer, id string) (TrashBatch, error) {
	b, err := scanTrash(q.QueryRowContext(ctx, "SELECT "+trashColumns+" FROM trash_batches b LEFT JOIN knowledge_bases k ON k.id=b.knowledge_base_id LEFT JOIN documents d ON d.id=b.root_document_id WHERE b.id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = Err(404, "NOT_FOUND", "删除批次不存在", nil)
	}
	return b, err
}
func activeTrash(b TrashBatch) error {
	if b.PurgedAt != nil {
		return Err(409, "TRASH_ALREADY_PURGED", "删除批次已永久删除", nil)
	}
	if b.RestoredAt != nil {
		return Err(409, "TRASH_ALREADY_RESTORED", "删除批次已恢复", nil)
	}
	return nil
}
func (a *App) listTrash(w http.ResponseWriter, r *http.Request) error {
	page, size, err := Page(r)
	if err != nil {
		return err
	}
	where := "b.restored_at IS NULL AND b.purged_at IS NULL"
	args := []any{}
	if kb := r.URL.Query().Get("knowledge_base_id"); kb != "" {
		where += " AND b.knowledge_base_id=?"
		args = append(args, kb)
	}
	var total int
	if err = a.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM trash_batches b WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, size, (page-1)*size)
	rows, err := a.DB.QueryContext(r.Context(), "SELECT "+trashColumns+" FROM trash_batches b LEFT JOIN knowledge_bases k ON k.id=b.knowledge_base_id LEFT JOIN documents d ON d.id=b.root_document_id WHERE "+where+" ORDER BY b.created_at DESC,b.id ASC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []TrashBatch{}
	for rows.Next() {
		b, err := scanTrash(rows)
		if err != nil {
			return err
		}
		items = append(items, b)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	WriteData(w, 200, items, map[string]any{"total": total, "page": page, "page_size": size})
	return nil
}

type purgePreview struct {
	BatchID                   string `json:"batch_id"`
	KnowledgeBaseID           string `json:"knowledge_base_id"`
	KnowledgeBaseName         string `json:"knowledge_base_name"`
	DocumentCount             int    `json:"document_count"`
	HistoryCount              int    `json:"history_count"`
	ReferencedAttachmentCount int    `json:"referenced_attachment_count"`
	IndependentBatchCount     int    `json:"independent_batch_count"`
	ContentDigest             string `json:"content_digest"`
}

func batchDocuments(ctx context.Context, q Queryer, b TrashBatch, purge bool) ([]Document, error) {
	clause := "d.delete_batch_id=?"
	arg := b.ID
	if purge && b.Kind == "knowledge_base" {
		clause = "d.knowledge_base_id=?"
		arg = b.KnowledgeBaseID
	}
	columns := "d.id,d.knowledge_base_id,d.parent_id,d.title,'','','',d.render_version,d.sort_order,d.revision,d.created_at,d.updated_at,d.deleted_at,d.delete_batch_id"
	rows, err := q.QueryContext(ctx, "SELECT "+columns+" FROM documents d WHERE "+clause+" ORDER BY d.sort_order,d.id", arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documents := []Document{}
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		documents = append(documents, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return documents, nil
}
func buildPurgePreview(ctx context.Context, q Queryer, b TrashBatch) (purgePreview, []Document, error) {
	k, err := LoadKnowledgeBase(ctx, q, b.KnowledgeBaseID, false)
	if err != nil {
		return purgePreview{}, nil, err
	}
	documents, err := batchDocuments(ctx, q, b, true)
	if err != nil {
		return purgePreview{}, nil, err
	}
	preview := purgePreview{BatchID: b.ID, KnowledgeBaseID: k.ID, KnowledgeBaseName: k.Name, DocumentCount: len(documents)}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	_ = encoder.Encode(b)
	_ = encoder.Encode(k)
	refs := map[string]bool{}
	batches := map[string]bool{}
	for _, d := range documents {
		_ = encoder.Encode(d)
		var markdown string
		if err := q.QueryRowContext(ctx, "SELECT markdown FROM documents WHERE id=?", d.ID).Scan(&markdown); err != nil {
			return preview, nil, err
		}
		contentHash := sha256.Sum256([]byte(markdown))
		_ = encoder.Encode(hex.EncodeToString(contentHash[:]))
		if d.DeleteBatchID != nil && *d.DeleteBatchID != b.ID {
			batches[*d.DeleteBatchID] = true
		}
		rows, err := q.QueryContext(ctx, "SELECT id,revision,title,markdown,reason,created_at FROM document_revisions WHERE document_id=? ORDER BY id", d.ID)
		if err != nil {
			return preview, nil, err
		}
		for rows.Next() {
			var id, title, markdown, reason string
			var revision int
			var createdAt int64
			if err := rows.Scan(&id, &revision, &title, &markdown, &reason, &createdAt); err != nil {
				rows.Close()
				return preview, nil, err
			}
			_ = encoder.Encode([]any{id, revision, title, markdown, reason, createdAt})
			preview.HistoryCount++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return preview, nil, err
		}
		rows, err = q.QueryContext(ctx, "SELECT attachment_id FROM document_attachments WHERE document_id=? UNION SELECT ra.attachment_id FROM revision_attachments ra JOIN document_revisions r ON r.id=ra.revision_id WHERE r.document_id=? ORDER BY attachment_id", d.ID, d.ID)
		if err != nil {
			return preview, nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return preview, nil, err
			}
			refs[id] = true
			_ = encoder.Encode([]string{d.ID, id})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return preview, nil, err
		}
	}
	preview.ReferencedAttachmentCount = len(refs)
	preview.IndependentBatchCount = len(batches)
	preview.ContentDigest = hex.EncodeToString(h.Sum(nil))
	return preview, documents, nil
}
func trashNode(d Document) map[string]any {
	return map[string]any{"id": d.ID, "parent_id": d.ParentID, "title": d.Title, "sort_order": d.SortOrder, "revision": d.Revision, "deleted_at": d.DeletedAt, "delete_batch_id": d.DeleteBatchID}
}
func (a *App) getTrash(w http.ResponseWriter, r *http.Request) error {
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := loadTrash(r.Context(), tx, r.PathValue("batch_id"))
	if err != nil {
		return err
	}
	if err = activeTrash(b); err != nil {
		return err
	}
	preview, all, err := buildPurgePreview(r.Context(), tx, b)
	if err != nil {
		return err
	}
	nodes := []map[string]any{}
	for _, d := range all {
		if d.DeleteBatchID != nil && *d.DeleteBatchID == b.ID {
			nodes = append(nodes, trashNode(d))
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, map[string]any{"batch": b, "nodes": nodes, "delete_preview": preview, "confirmation_token": a.Token("purge-trash", preview)}, nil)
	return nil
}

type parentAdjustment struct {
	DocumentID       string  `json:"document_id"`
	OriginalParentID *string `json:"original_parent_id"`
	ParentID         *string `json:"parent_id"`
	Reason           string  `json:"reason"`
}

func (a *App) restoreTrash(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ExpectedTreeRevision int `json:"expected_tree_revision"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := loadTrash(ctx, tx, r.PathValue("batch_id"))
	if err != nil {
		return err
	}
	if err = activeTrash(b); err != nil {
		return err
	}
	k, err := LoadKnowledgeBase(ctx, tx, b.KnowledgeBaseID, false)
	if err != nil {
		return err
	}
	if err = treeConflict(k, req.ExpectedTreeRevision); err != nil {
		return err
	}
	if b.Kind == "document_subtree" && k.DeletedAt != nil {
		return Err(409, "KNOWLEDGE_BASE_DELETED", "请先恢复文档所属的知识库批次", map[string]any{"knowledge_base_id": k.ID, "delete_batch_id": k.DeleteBatchID})
	}
	if b.Kind == "knowledge_base" && (k.DeleteBatchID == nil || *k.DeleteBatchID != b.ID) {
		return Err(409, "TRASH_BATCH_CHANGED", "知识库删除状态已变化", nil)
	}
	documents, err := batchDocuments(ctx, tx, b, false)
	if err != nil {
		return err
	}
	ids := documentIDs(documents)
	adjustments := []parentAdjustment{}
	nextOrders := map[string]int{}
	now := Now()
	if b.Kind == "knowledge_base" {
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET deleted_at=NULL,delete_batch_id=NULL WHERE id=?", k.ID); err != nil {
			return err
		}
	}
	var originalRootParent *string
	if b.Kind == "document_subtree" {
		if err = tx.QueryRowContext(ctx, "SELECT original_parent_id FROM trash_batches WHERE id=?", b.ID).Scan(&originalRootParent); err != nil {
			return err
		}
	}
	for i := range documents {
		d := &documents[i]
		if d.ParentID != nil && ids[*d.ParentID] {
			continue
		}
		// Purging an earlier parent may have detached this surviving batch to
		// keep foreign keys valid. Its original parent is still audit metadata;
		// unavailable original parents restore at the library root.
		if b.RootDocumentID != nil && d.ID == *b.RootDocumentID && originalRootParent != nil {
			d.ParentID = originalRootParent
		}
		original := d.ParentID
		reason := ""
		if d.ParentID != nil {
			parent, err := LoadDocument(ctx, tx, *d.ParentID, true)
			if err != nil {
				var api *APIError
				if !errors.As(err, &api) || api.Status != 404 {
					return err
				}
				d.ParentID = nil
				reason = "原父节点不可用，已恢复至知识库根级"
			} else if parent.KnowledgeBaseID != k.ID {
				return Err(409, "TREE_INTEGRITY_ERROR", "回收站批次含跨库父节点", nil)
			}
		}
		if reason == "" && b.RootDocumentID != nil && d.ID == *b.RootDocumentID && originalRootParent != nil && d.ParentID == nil {
			original = originalRootParent
			reason = "原父节点已永久删除，已恢复至知识库根级"
		}
		if reason != "" {
			adjustments = append(adjustments, parentAdjustment{DocumentID: d.ID, OriginalParentID: original, ParentID: d.ParentID, Reason: reason})
		}
		depth, err := ParentDepth(ctx, tx, k.ID, d.ParentID)
		if err != nil {
			return err
		}
		if depth >= 32 {
			return Err(400, "TREE_DEPTH_EXCEEDED", "恢复后的文档树将超过 32 层限制", map[string]any{"max_depth": 32})
		}
		key := nullableKey(d.ParentID)
		order, ok := nextOrders[key]
		if !ok {
			if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sort_order),-1)+1 FROM documents WHERE knowledge_base_id=? AND parent_id IS ? AND deleted_at IS NULL", k.ID, d.ParentID).Scan(&order); err != nil {
				return err
			}
		}
		d.SortOrder = order
		nextOrders[key] = order + 1
	}
	for _, d := range documents {
		var unusable int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM document_attachments da JOIN attachments a ON a.id=da.attachment_id WHERE da.document_id=? AND a.state<>'ready'", d.ID).Scan(&unusable); err != nil {
			return err
		}
		if unusable > 0 {
			return Err(409, "ATTACHMENT_UNAVAILABLE", "文档引用的附件不可用，请先修复文件", map[string]any{"document_id": d.ID})
		}
		if _, err = tx.ExecContext(ctx, "UPDATE documents SET parent_id=?,sort_order=?,deleted_at=NULL,delete_batch_id=NULL WHERE id=? AND delete_batch_id=?", d.ParentID, d.SortOrder, d.ID, b.ID); err != nil {
			return err
		}
	}
	if err = ValidateTree(ctx, tx, k.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET tree_revision=tree_revision+1,updated_at=? WHERE id=?", now, k.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE trash_batches SET restored_at=? WHERE id=?", now, b.ID); err != nil {
		return err
	}
	data := map[string]any{"batch_id": b.ID, "restored_count": len(documents), "tree_revision": k.TreeRevision + 1, "parent_adjustments": adjustments}
	if err = SaveIdempotency(ctx, tx, 200, data, nil); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, data, nil)
	return nil
}

// Preserve independently deleted descendants before removing their old parent.
// Parent foreign keys remain RESTRICT; surviving batches keep their documents.
func detachSurvivors(ctx context.Context, tx *sql.Tx, kbID string, documents []Document) ([]parentAdjustment, error) {
	removing := documentIDs(documents)
	byID := map[string]Document{}
	for _, d := range documents {
		byID[d.ID] = d
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,parent_id FROM documents WHERE knowledge_base_id=? ORDER BY id", kbID)
	if err != nil {
		return nil, err
	}
	type relation struct {
		id     string
		parent *string
	}
	survivors := []relation{}
	for rows.Next() {
		var item relation
		if err := rows.Scan(&item.id, &item.parent); err != nil {
			rows.Close()
			return nil, err
		}
		if !removing[item.id] && item.parent != nil && removing[*item.parent] {
			survivors = append(survivors, item)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	adjustments := []parentAdjustment{}
	for _, item := range survivors {
		parent := item.parent
		seen := map[string]bool{}
		for parent != nil && removing[*parent] {
			if seen[*parent] {
				return nil, Err(409, "TREE_INTEGRITY_ERROR", "待删除批次存在循环，无法安全清除", nil)
			}
			seen[*parent] = true
			parent = byID[*parent].ParentID
		}
		// A surviving deleted subtree lost its external parent. Keep it as a
		// root while it is in trash, matching the documented restore fallback.
		parent = nil
		if _, err = tx.ExecContext(ctx, "UPDATE documents SET parent_id=? WHERE id=?", parent, item.id); err != nil {
			return nil, err
		}
		adjustments = append(adjustments, parentAdjustment{DocumentID: item.id, OriginalParentID: item.parent, ParentID: parent, Reason: "原父节点永久删除，已调整保留批次的父关系"})
	}
	return adjustments, nil
}
func leafFirst(documents []Document) ([]Document, error) {
	byID := map[string]Document{}
	for _, d := range documents {
		byID[d.ID] = d
	}
	depths := map[string]int{}
	for _, d := range documents {
		seen := map[string]bool{}
		depth := 0
		cur := d.ID
		for {
			if seen[cur] {
				return nil, Err(409, "TREE_INTEGRITY_ERROR", "待删除文档存在循环，无法安全清除", nil)
			}
			seen[cur] = true
			depth++
			parent := byID[cur].ParentID
			if parent == nil {
				break
			}
			if _, ok := byID[*parent]; !ok {
				break
			}
			cur = *parent
		}
		depths[d.ID] = depth
	}
	ordered := append([]Document(nil), documents...)
	sort.Slice(ordered, func(i, j int) bool {
		if depths[ordered[i].ID] != depths[ordered[j].ID] {
			return depths[ordered[i].ID] > depths[ordered[j].ID]
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered, nil
}
func (a *App) purgeTrash(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	if req.ConfirmationToken == "" {
		return Err(400, "INVALID_ARGUMENT", "必须提供删除预览中的 confirmation_token", nil)
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := loadTrash(ctx, tx, r.PathValue("batch_id"))
	if err != nil {
		return err
	}
	if err = activeTrash(b); err != nil {
		return err
	}
	preview, documents, err := buildPurgePreview(ctx, tx, b)
	if err != nil {
		return err
	}
	if !a.VerifyToken(req.ConfirmationToken, "purge-trash", preview) {
		return Err(409, "CONFIRMATION_EXPIRED", "删除确认已过期或影响范围已变化，请重新预览", nil)
	}
	result, err := purgeTrashBatch(ctx, tx, b, preview, documents)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, result, nil)
	return nil
}

type purgeResult struct {
	BatchID                     string             `json:"batch_id"`
	PurgedCount                 int                `json:"purged_count"`
	HistoryCount                int                `json:"history_count"`
	AttachmentCleanupCandidates []string           `json:"attachment_cleanup_candidates"`
	ParentAdjustments           []parentAdjustment `json:"parent_adjustments"`
}

func purgeTrashBatch(ctx context.Context, tx *sql.Tx, b TrashBatch, preview purgePreview, documents []Document) (purgeResult, error) {
	ordered, err := leafFirst(documents)
	if err != nil {
		return purgeResult{}, err
	}
	adjustments, err := detachSurvivors(ctx, tx, b.KnowledgeBaseID, documents)
	if err != nil {
		return purgeResult{}, err
	}
	now := Now()
	candidateIDs := map[string]bool{}
	for _, d := range documents {
		rows, err := tx.QueryContext(ctx, "SELECT attachment_id FROM document_attachments WHERE document_id=? UNION SELECT ra.attachment_id FROM revision_attachments ra JOIN document_revisions r ON r.id=ra.revision_id WHERE r.document_id=?", d.ID, d.ID)
		if err != nil {
			return purgeResult{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return purgeResult{}, err
			}
			candidateIDs[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return purgeResult{}, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM document_attachments WHERE document_id=?", d.ID); err != nil {
			return purgeResult{}, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM revision_attachments WHERE revision_id IN (SELECT id FROM document_revisions WHERE document_id=?)", d.ID); err != nil {
			return purgeResult{}, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM document_revisions WHERE document_id=?", d.ID); err != nil {
			return purgeResult{}, err
		}
	}
	for _, d := range ordered {
		result, err := tx.ExecContext(ctx, "DELETE FROM documents WHERE id=?", d.ID)
		if err != nil {
			return purgeResult{}, err
		}
		if err = assertAffected(result, 1); err != nil {
			return purgeResult{}, err
		}
	}
	if b.Kind == "knowledge_base" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM knowledge_bases WHERE id=?", b.KnowledgeBaseID); err != nil {
			return purgeResult{}, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE trash_batches SET purged_at=? WHERE knowledge_base_id=? AND restored_at IS NULL AND purged_at IS NULL", now, b.KnowledgeBaseID); err != nil {
			return purgeResult{}, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET tree_revision=tree_revision+1,updated_at=? WHERE id=?", now, b.KnowledgeBaseID); err != nil {
			return purgeResult{}, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE trash_batches SET purged_at=? WHERE id=?", now, b.ID); err != nil {
			return purgeResult{}, err
		}
	}
	candidates := []string{}
	for id := range candidateIDs {
		var references int
		if err = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM document_attachments WHERE attachment_id=?)+(SELECT count(*) FROM revision_attachments WHERE attachment_id=?)", id, id).Scan(&references); err != nil {
			return purgeResult{}, err
		}
		if references == 0 {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	return purgeResult{b.ID, len(documents), preview.HistoryCount, candidates, adjustments}, nil
}

type emptyTrashPreview struct {
	BatchCount         int    `json:"batch_count"`
	KnowledgeBaseCount int    `json:"knowledge_base_count"`
	DocumentCount      int    `json:"document_count"`
	HistoryCount       int    `json:"history_count"`
	ContentDigest      string `json:"content_digest"`
}

type trashPurgeScope struct {
	batch     TrashBatch
	preview   purgePreview
	documents []Document
}

func buildEmptyTrashPreview(ctx context.Context, q Queryer) (emptyTrashPreview, []trashPurgeScope, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+trashColumns+" FROM trash_batches b LEFT JOIN knowledge_bases k ON k.id=b.knowledge_base_id LEFT JOIN documents d ON d.id=b.root_document_id WHERE b.restored_at IS NULL AND b.purged_at IS NULL ORDER BY b.id")
	if err != nil {
		return emptyTrashPreview{}, nil, err
	}
	all := []TrashBatch{}
	libraries := map[string]bool{}
	for rows.Next() {
		b, err := scanTrash(rows)
		if err != nil {
			rows.Close()
			return emptyTrashPreview{}, nil, err
		}
		all = append(all, b)
		if b.Kind == "knowledge_base" {
			libraries[b.KnowledgeBaseID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return emptyTrashPreview{}, nil, err
	}
	preview := emptyTrashPreview{BatchCount: len(all), KnowledgeBaseCount: len(libraries)}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	_ = encoder.Encode(all)
	scopes := []trashPurgeScope{}
	for _, b := range all {
		// A library purge already includes its independently deleted subtrees.
		if b.Kind == "document_subtree" && libraries[b.KnowledgeBaseID] {
			continue
		}
		p, documents, err := buildPurgePreview(ctx, q, b)
		if err != nil {
			return preview, nil, err
		}
		_ = encoder.Encode(p)
		preview.DocumentCount += p.DocumentCount
		preview.HistoryCount += p.HistoryCount
		scopes = append(scopes, trashPurgeScope{b, p, documents})
	}
	preview.ContentDigest = hex.EncodeToString(h.Sum(nil))
	return preview, scopes, nil
}

func (a *App) previewEmptyTrash(w http.ResponseWriter, r *http.Request) error {
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	preview, _, err := buildEmptyTrashPreview(r.Context(), tx)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, map[string]any{"delete_preview": preview, "confirmation_token": a.Token("empty-trash", preview)}, nil)
	return nil
}

func (a *App) emptyTrash(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	if req.ConfirmationToken == "" {
		return Err(400, "INVALID_ARGUMENT", "必须提供清空预览中的 confirmation_token", nil)
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	preview, scopes, err := buildEmptyTrashPreview(ctx, tx)
	if err != nil {
		return err
	}
	if !a.VerifyToken(req.ConfirmationToken, "empty-trash", preview) {
		return Err(409, "CONFIRMATION_EXPIRED", "回收站内容已变化或确认已过期，请重新预览", nil)
	}
	candidates := map[string]bool{}
	for _, scope := range scopes {
		result, err := purgeTrashBatch(ctx, tx, scope.batch, scope.preview, scope.documents)
		if err != nil {
			return err
		}
		for _, id := range result.AttachmentCleanupCandidates {
			candidates[id] = true
		}
	}
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, map[string]any{"purged_batch_count": preview.BatchCount, "purged_document_count": preview.DocumentCount, "history_count": preview.HistoryCount, "attachment_cleanup_candidates": ids}, nil)
	return nil
}

// CheckTreeIntegrity is an explicit maintenance operation, avoiding a costly
// full tree scan at every startup while retaining a repair diagnostic.
func (a *App) CheckTreeIntegrity(ctx context.Context) error {
	rows, err := a.DB.QueryContext(ctx, "SELECT id FROM knowledge_bases WHERE deleted_at IS NULL ORDER BY id")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = ValidateTree(ctx, a.DB, id); err != nil {
			return fmt.Errorf("知识库 %s: %w", id, err)
		}
	}
	return nil
}
