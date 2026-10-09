package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

func snapshotDocument(ctx context.Context, tx *sql.Tx, d Document, reason string, now int64) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM document_revisions WHERE document_id=? AND revision=?", d.ID, d.Revision).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = NewID()
	if _, err = tx.ExecContext(ctx, "INSERT INTO document_revisions(id,document_id,revision,title,markdown,reason,created_at) VALUES(?,?,?,?,?,?,?)", id, d.ID, d.Revision, d.Title, d.Markdown, reason, now); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO revision_attachments(revision_id,attachment_id) SELECT ?,attachment_id FROM document_attachments WHERE document_id=?", id, d.ID); err != nil {
		return "", err
	}
	return id, nil
}
func historyLimit(ctx context.Context, q Queryer) (int, error) {
	var encoded string
	err := q.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key='history_limit'").Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return 100, nil
	}
	if err != nil {
		return 0, err
	}
	var limit int
	if err = json.Unmarshal([]byte(encoded), &limit); err != nil {
		return 0, err
	}
	if limit < 1 || limit > 10000 {
		return 0, Err(500, "INTERNAL_ERROR", "历史保留设置无效", nil)
	}
	return limit, nil
}
func pruneHistory(ctx context.Context, tx *sql.Tx, documentID, protectID string) error {
	limit, err := historyLimit(ctx, tx)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM document_revisions WHERE document_id=?", documentID).Scan(&count); err != nil {
		return err
	}
	if count <= limit {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM document_revisions WHERE document_id=? AND id<>? ORDER BY revision ASC,id ASC LIMIT ?", documentID, protectID, count-limit)
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
		if _, err = tx.ExecContext(ctx, "DELETE FROM revision_attachments WHERE revision_id=?", id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM document_revisions WHERE id=?", id); err != nil {
			return err
		}
	}
	return nil
}
func loadRevision(ctx context.Context, q Queryer, documentID, historyID string) (Revision, error) {
	var h Revision
	err := q.QueryRowContext(ctx, "SELECT id,document_id,revision,title,markdown,reason,created_at FROM document_revisions WHERE id=? AND document_id=?", historyID, documentID).Scan(&h.ID, &h.DocumentID, &h.Revision, &h.Title, &h.Markdown, &h.Reason, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = Err(404, "NOT_FOUND", "历史版本不存在或不属于该文档", nil)
	}
	return h, err
}
func (a *App) listHistory(w http.ResponseWriter, r *http.Request) error {
	page, size, err := Page(r)
	if err != nil {
		return err
	}
	d, err := LoadDocument(r.Context(), a.DB, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	var total int
	if err = a.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM document_revisions WHERE document_id=?", d.ID).Scan(&total); err != nil {
		return err
	}
	rows, err := a.DB.QueryContext(r.Context(), "SELECT id,document_id,revision,title,reason,created_at FROM document_revisions WHERE document_id=? ORDER BY revision DESC,id ASC LIMIT ? OFFSET ?", d.ID, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var h Revision
		if err := rows.Scan(&h.ID, &h.DocumentID, &h.Revision, &h.Title, &h.Reason, &h.CreatedAt); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": h.ID, "document_id": h.DocumentID, "revision": h.Revision, "title": h.Title, "reason": h.Reason, "created_at": h.CreatedAt})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	WriteData(w, 200, items, map[string]any{"total": total, "page": page, "page_size": size})
	return nil
}
func (a *App) getHistory(w http.ResponseWriter, r *http.Request) error {
	d, err := LoadDocument(r.Context(), a.DB, r.PathValue("id"), true)
	if err != nil {
		return err
	}
	h, err := loadRevision(r.Context(), a.DB, d.ID, r.PathValue("history_id"))
	if err != nil {
		return err
	}
	WriteData(w, 200, h, nil)
	return nil
}
func (a *App) restoreHistory(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ExpectedRevision int `json:"expected_revision"`
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
	if err = revisionConflict(d, req.ExpectedRevision); err != nil {
		return err
	}
	h, err := loadRevision(ctx, tx, d.ID, r.PathValue("history_id"))
	if err != nil {
		return err
	}
	if _, err = ValidateTitle(h.Title); err != nil {
		return err
	}
	if err = ValidateMarkdown(h.Markdown); err != nil {
		return err
	}
	rendered := RenderMarkdown(h.Markdown)
	now := Now()
	protect, err := snapshotDocument(ctx, tx, d, "before_restore", now)
	if err != nil {
		return err
	}
	if err = SyncDocumentAttachments(ctx, tx, d.ID, rendered.AttachmentIDs); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE documents SET title=?,markdown=?,html=?,search_text=?,render_version=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND deleted_at IS NULL", h.Title, h.Markdown, rendered.HTML, rendered.SearchText, rendered.Version, now, d.ID, req.ExpectedRevision)
	if err != nil {
		return err
	}
	if err = assertAffected(result, 1); err != nil {
		return err
	}
	treeDelta := 0
	if h.Title != d.Title {
		treeDelta = 1
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET tree_revision=tree_revision+?,updated_at=? WHERE id=? AND deleted_at IS NULL", treeDelta, now, d.KnowledgeBaseID); err != nil {
		return err
	}
	if err = pruneHistory(ctx, tx, d.ID, protect); err != nil {
		return err
	}
	d.Title = h.Title
	d.Markdown = h.Markdown
	d.HTML = rendered.HTML
	d.SearchText = rendered.SearchText
	d.RenderVersion = rendered.Version
	d.Revision++
	d.UpdatedAt = now
	if err = SaveIdempotency(ctx, tx, 200, d, nil); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	WriteData(w, 200, d, nil)
	return nil
}
