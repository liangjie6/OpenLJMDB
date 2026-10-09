package backend

import (
	"database/sql"
	"net/http"
)

// Link resolution reports deletion state without returning recycled content.
func (a *App) documentLinkStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidID(id) {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "文档 ID 无效", nil))
		return
	}
	var title, kbID string
	var documentDeleted, kbDeleted *int64
	var documentBatch, kbBatch *string
	err := a.DB.QueryRowContext(r.Context(), "SELECT d.title,d.knowledge_base_id,d.deleted_at,d.delete_batch_id,k.deleted_at,k.delete_batch_id FROM documents d JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE d.id=?", id).Scan(&title, &kbID, &documentDeleted, &documentBatch, &kbDeleted, &kbBatch)
	if err == sql.ErrNoRows {
		WriteData(w, 200, map[string]any{"id": id, "status": "missing"}, nil)
		return
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	status := "active"
	if documentDeleted != nil || kbDeleted != nil {
		status = "trashed"
	}
	WriteData(w, 200, map[string]any{"id": id, "status": status, "title": title, "knowledge_base_id": kbID, "delete_batch_id": documentBatch, "knowledge_base_delete_batch_id": kbBatch}, nil)
}
