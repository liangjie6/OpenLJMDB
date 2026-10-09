package backend

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

type coreEndpoint func(http.ResponseWriter, *http.Request) error

func coreHandler(fn coreEndpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}

func (a *App) RegisterCore(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/knowledge-bases", coreHandler(a.listKnowledgeBases))
	mux.HandleFunc("POST /api/v1/knowledge-bases", coreHandler(a.createKnowledgeBase))
	mux.HandleFunc("GET /api/v1/knowledge-bases/{id}", coreHandler(a.getKnowledgeBase))
	mux.HandleFunc("PUT /api/v1/knowledge-bases/{id}", coreHandler(a.updateKnowledgeBase))
	mux.HandleFunc("DELETE /api/v1/knowledge-bases/{id}", coreHandler(a.deleteKnowledgeBase))
	mux.HandleFunc("GET /api/v1/knowledge-bases/{id}/tree", coreHandler(a.getTree))
	mux.HandleFunc("POST /api/v1/knowledge-bases/{id}/documents", coreHandler(a.createDocument))
	mux.HandleFunc("GET /api/v1/documents/{id}", coreHandler(a.getDocument))
	mux.HandleFunc("PUT /api/v1/documents/{id}", coreHandler(a.updateDocument))
	mux.HandleFunc("DELETE /api/v1/documents/{id}", coreHandler(a.deleteDocument))
	mux.HandleFunc("GET /api/v1/documents/{id}/history", coreHandler(a.listHistory))
	mux.HandleFunc("GET /api/v1/documents/{id}/history/{history_id}", coreHandler(a.getHistory))
	mux.HandleFunc("POST /api/v1/documents/{id}/history/{history_id}/restore", coreHandler(a.restoreHistory))
	mux.HandleFunc("GET /api/v1/trash", coreHandler(a.listTrash))
	mux.HandleFunc("GET /api/v1/trash/empty-preview", coreHandler(a.previewEmptyTrash))
	mux.HandleFunc("DELETE /api/v1/trash", coreHandler(a.emptyTrash))
	mux.HandleFunc("GET /api/v1/trash/{batch_id}", coreHandler(a.getTrash))
	mux.HandleFunc("POST /api/v1/trash/{batch_id}/restore", coreHandler(a.restoreTrash))
	mux.HandleFunc("DELETE /api/v1/trash/{batch_id}", coreHandler(a.purgeTrash))
}

func (a *App) listKnowledgeBases(w http.ResponseWriter, r *http.Request) error {
	page, size, err := Page(r)
	if err != nil {
		return err
	}
	var total int
	if err = a.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM knowledge_bases WHERE deleted_at IS NULL").Scan(&total); err != nil {
		return err
	}
	rows, err := a.DB.QueryContext(r.Context(), "SELECT "+knowledgeBaseColumns+" FROM knowledge_bases k WHERE k.deleted_at IS NULL ORDER BY k.updated_at DESC,k.id ASC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []KnowledgeBase{}
	for rows.Next() {
		k, err := scanKnowledgeBase(rows)
		if err != nil {
			return err
		}
		items = append(items, k)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	WriteData(w, 200, items, map[string]any{"total": total, "page": page, "page_size": size})
	return nil
}
func (a *App) createKnowledgeBase(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        *string `json:"name"`
		Description string  `json:"description"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	if req.Name == nil {
		return Err(400, "INVALID_ARGUMENT", "必须提供 name", nil)
	}
	name, err := ValidateTitle(*req.Name)
	if err != nil {
		return err
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	k := KnowledgeBase{ID: NewID(), Name: name, Description: req.Description, TreeRevision: 1, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO knowledge_bases(id,name,description,tree_revision,created_at,updated_at) VALUES(?,?,?,?,?,?)", k.ID, k.Name, k.Description, 1, now, now); err != nil {
		return err
	}
	if err = SaveIdempotency(r.Context(), tx, 201, k, nil); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 201, k, nil)
	return nil
}
func (a *App) getKnowledgeBase(w http.ResponseWriter, r *http.Request) error {
	k, err := LoadKnowledgeBase(r.Context(), a.DB, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	WriteData(w, 200, k, nil)
	return nil
}
func (a *App) updateKnowledgeBase(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	if req.Name == nil || req.Description == nil {
		return Err(400, "INVALID_ARGUMENT", "必须提供 name 和 description", nil)
	}
	name, err := ValidateTitle(*req.Name)
	if err != nil {
		return err
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	k, err := LoadKnowledgeBase(r.Context(), tx, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	k.Name = name
	k.Description = *req.Description
	k.UpdatedAt = Now()
	if _, err = tx.ExecContext(r.Context(), "UPDATE knowledge_bases SET name=?,description=?,updated_at=? WHERE id=? AND deleted_at IS NULL", k.Name, k.Description, k.UpdatedAt, k.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, k, nil)
	return nil
}
func (a *App) getTree(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	k, err := LoadKnowledgeBase(ctx, tx, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	if err = ValidateTree(ctx, tx, k.ID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,parent_id,title,sort_order,revision,updated_at FROM documents WHERE knowledge_base_id=? AND deleted_at IS NULL ORDER BY sort_order,id", k.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	flat := []*TreeNode{}
	byID := map[string]*TreeNode{}
	for rows.Next() {
		node := &TreeNode{Children: []*TreeNode{}}
		if err := rows.Scan(&node.ID, &node.ParentID, &node.Title, &node.SortOrder, &node.Revision, &node.UpdatedAt); err != nil {
			return err
		}
		flat = append(flat, node)
		byID[node.ID] = node
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	roots := []*TreeNode{}
	for _, node := range flat {
		if node.ParentID == nil {
			roots = append(roots, node)
		} else {
			byID[*node.ParentID].Children = append(byID[*node.ParentID].Children, node)
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, map[string]any{"knowledge_base_id": k.ID, "tree_revision": k.TreeRevision, "nodes": roots}, nil)
	return nil
}
func decodeParent(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, Err(400, "INVALID_ARGUMENT", "parent_id 必须显式提供，根节点为 null", nil)
	}
	var parent *string
	if err := json.Unmarshal(raw, &parent); err != nil {
		return nil, Err(400, "INVALID_ARGUMENT", "parent_id 必须是文档 ID 或 null", nil)
	}
	if parent != nil && *parent == "" {
		return nil, Err(400, "INVALID_ARGUMENT", "parent_id 不得为空字符串", nil)
	}
	return parent, nil
}
func (a *App) createDocument(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ParentID             json.RawMessage `json:"parent_id"`
		Title                *string         `json:"title"`
		Markdown             string          `json:"markdown"`
		ExpectedTreeRevision int             `json:"expected_tree_revision"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	parent, err := decodeParent(req.ParentID)
	if err != nil {
		return err
	}
	title := "无标题"
	if req.Title != nil {
		title = *req.Title
	}
	title, err = ValidateTitle(title)
	if err != nil {
		return err
	}
	if err = ValidateMarkdown(req.Markdown); err != nil {
		return err
	}
	rendered := RenderMarkdown(req.Markdown)
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	k, err := LoadKnowledgeBase(ctx, tx, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	if err = treeConflict(k, req.ExpectedTreeRevision); err != nil {
		return err
	}
	if k.DocumentCount >= MaxTreeNodes {
		return Err(413, "TREE_LIMIT_EXCEEDED", "知识库有效文档数量已达到文档树限制", map[string]any{"max_tree_nodes": MaxTreeNodes})
	}
	if err = ValidateTree(ctx, tx, k.ID); err != nil {
		return err
	}
	depth, err := ParentDepth(ctx, tx, k.ID, parent)
	if err != nil {
		return err
	}
	if depth >= 32 {
		return Err(400, "TREE_DEPTH_EXCEEDED", "新建文档将超过 32 层限制", map[string]any{"max_depth": 32})
	}
	var order int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sort_order),-1)+1 FROM documents WHERE knowledge_base_id=? AND parent_id IS ? AND deleted_at IS NULL", k.ID, parent).Scan(&order); err != nil {
		return err
	}
	now := Now()
	d := Document{ID: NewID(), KnowledgeBaseID: k.ID, ParentID: parent, Title: title, Markdown: req.Markdown, HTML: rendered.HTML, SearchText: rendered.SearchText, RenderVersion: rendered.Version, SortOrder: order, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, "INSERT INTO documents(id,knowledge_base_id,parent_id,title,markdown,html,search_text,render_version,sort_order,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", d.ID, d.KnowledgeBaseID, d.ParentID, d.Title, d.Markdown, d.HTML, d.SearchText, d.RenderVersion, d.SortOrder, d.Revision, d.CreatedAt, d.UpdatedAt); err != nil {
		return err
	}
	if err = SyncDocumentAttachments(ctx, tx, d.ID, rendered.AttachmentIDs); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET tree_revision=tree_revision+1,updated_at=? WHERE id=?", now, k.ID); err != nil {
		return err
	}
	meta := map[string]any{"tree_revision": k.TreeRevision + 1}
	if err = SaveIdempotency(ctx, tx, 201, d, meta); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 201, d, meta)
	return nil
}
func (a *App) getDocument(w http.ResponseWriter, r *http.Request) error {
	d, err := LoadDocument(r.Context(), a.DB, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	if RenderVersion != d.RenderVersion {
		rendered := RenderMarkdown(d.Markdown)
		a.WriteMu.Lock()
		result, updateErr := a.DB.ExecContext(r.Context(), "UPDATE documents SET html=?,search_text=?,render_version=? WHERE id=? AND revision=? AND deleted_at IS NULL", rendered.HTML, rendered.SearchText, rendered.Version, d.ID, d.Revision)
		a.WriteMu.Unlock()
		if updateErr != nil {
			return updateErr
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			d, err = LoadDocument(r.Context(), a.DB, d.ID, true)
			if err != nil {
				return err
			}
		} else {
			d.HTML = rendered.HTML
			d.SearchText = rendered.SearchText
			d.RenderVersion = rendered.Version
		}
	}
	WriteData(w, 200, d, nil)
	return nil
}
func (a *App) updateDocument(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Title            *string `json:"title"`
		Markdown         *string `json:"markdown"`
		ExpectedRevision int     `json:"expected_revision"`
		Snapshot         bool    `json:"snapshot"`
	}
	if err := Decode(r, &req); err != nil {
		return err
	}
	if req.Title == nil || req.Markdown == nil {
		return Err(400, "INVALID_ARGUMENT", "必须提供 title、markdown 和 expected_revision", nil)
	}
	title, err := ValidateTitle(*req.Title)
	if err != nil {
		return err
	}
	if err = ValidateMarkdown(*req.Markdown); err != nil {
		return err
	}
	rendered := RenderMarkdown(*req.Markdown)
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := LoadDocument(ctx, tx, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	if err = revisionConflict(d, req.ExpectedRevision); err != nil {
		return err
	}
	now := Now()
	changed := d.Title != title || d.Markdown != *req.Markdown
	protect := ""
	if req.Snapshot || changed {
		reason := "automatic"
		save := req.Snapshot
		if req.Snapshot {
			reason = "manual"
		} else {
			var count int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM document_revisions WHERE document_id=? AND reason='automatic' AND created_at>=?", d.ID, (now/300000)*300000).Scan(&count); err != nil {
				return err
			}
			save = count == 0
		}
		if save {
			protect, err = snapshotDocument(ctx, tx, d, reason, now)
			if err != nil {
				return err
			}
		}
	}
	if err = SyncDocumentAttachments(ctx, tx, d.ID, rendered.AttachmentIDs); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE documents SET title=?,markdown=?,html=?,search_text=?,render_version=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND deleted_at IS NULL", title, *req.Markdown, rendered.HTML, rendered.SearchText, rendered.Version, now, d.ID, req.ExpectedRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return Err(409, "REVISION_CONFLICT", "文档版本已变化", nil)
	}
	treeDelta := 0
	if title != d.Title {
		treeDelta = 1
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET updated_at=?,tree_revision=tree_revision+? WHERE id=? AND deleted_at IS NULL", now, treeDelta, d.KnowledgeBaseID); err != nil {
		return err
	}
	if err = pruneHistory(ctx, tx, d.ID, protect); err != nil {
		return err
	}
	d.Title = title
	d.Markdown = *req.Markdown
	d.HTML = rendered.HTML
	d.SearchText = rendered.SearchText
	d.RenderVersion = rendered.Version
	d.Revision++
	d.UpdatedAt = now
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, d, nil)
	return nil
}

func (a *App) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request) error {
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
	k, err := LoadKnowledgeBase(ctx, tx, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	if err = treeConflict(k, req.ExpectedTreeRevision); err != nil {
		return err
	}
	if err = ValidateTree(ctx, tx, k.ID); err != nil {
		return err
	}
	now := Now()
	b := TrashBatch{ID: NewID(), Kind: "knowledge_base", KnowledgeBaseID: k.ID, Title: k.Name, CreatedAt: now, DocumentCount: k.DocumentCount}
	if _, err = tx.ExecContext(ctx, "INSERT INTO trash_batches(id,kind,knowledge_base_id,created_at) VALUES(?,?,?,?)", b.ID, b.Kind, k.ID, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE documents SET deleted_at=?,delete_batch_id=? WHERE knowledge_base_id=? AND deleted_at IS NULL", now, b.ID, k.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET deleted_at=?,delete_batch_id=?,updated_at=?,tree_revision=tree_revision+1 WHERE id=?", now, b.ID, now, k.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, b, map[string]any{"tree_revision": k.TreeRevision + 1})
	return nil
}
func (a *App) deleteDocument(w http.ResponseWriter, r *http.Request) error {
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
	d, err := LoadDocument(ctx, tx, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	k, err := LoadKnowledgeBase(ctx, tx, d.KnowledgeBaseID, true)
	if err != nil {
		return err
	}
	if err = treeConflict(k, req.ExpectedTreeRevision); err != nil {
		return err
	}
	if err = ValidateTree(ctx, tx, k.ID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,parent_id FROM documents WHERE knowledge_base_id=? AND deleted_at IS NULL", k.ID)
	if err != nil {
		return err
	}
	children := map[string][]string{}
	for rows.Next() {
		var id string
		var parent *string
		if err := rows.Scan(&id, &parent); err != nil {
			rows.Close()
			return err
		}
		if parent != nil {
			children[*parent] = append(children[*parent], id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ids := []string{d.ID}
	for i := 0; i < len(ids); i++ {
		ids = append(ids, children[ids[i]]...)
	}
	now := Now()
	b := TrashBatch{ID: NewID(), Kind: "document_subtree", RootDocumentID: &d.ID, KnowledgeBaseID: k.ID, Title: d.Title, CreatedAt: now, DocumentCount: len(ids)}
	if _, err = tx.ExecContext(ctx, "INSERT INTO trash_batches(id,kind,root_document_id,original_parent_id,knowledge_base_id,created_at) VALUES(?,?,?,?,?,?)", b.ID, b.Kind, d.ID, d.ParentID, k.ID, now); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "UPDATE documents SET deleted_at=?,delete_batch_id=? WHERE id=? AND deleted_at IS NULL", now, b.ID, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET tree_revision=tree_revision+1,updated_at=? WHERE id=?", now, k.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, b, map[string]any{"tree_revision": k.TreeRevision + 1})
	return nil
}

// stableDocumentOrder is used by restore/purge and keeps sibling order
// deterministic when several documents have the same user-visible title.
func stableDocumentOrder(documents []Document) {
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].SortOrder != documents[j].SortOrder {
			return documents[i].SortOrder < documents[j].SortOrder
		}
		return documents[i].ID < documents[j].ID
	})
}
func nullableKey(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func documentIDs(documents []Document) map[string]bool {
	ids := make(map[string]bool, len(documents))
	for _, d := range documents {
		ids[d.ID] = true
	}
	return ids
}
func assertAffected(result sql.Result, want int64) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("数据库更新条目不符合预期")
	}
	return nil
}
