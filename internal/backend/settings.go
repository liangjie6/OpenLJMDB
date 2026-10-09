package backend

import (
	"encoding/json"
	"net/http"
	"time"
	_ "time/tzdata"
)

var ResourceLimits = map[string]any{
	"title_characters": 200, "markdown_bytes": 10 << 20, "attachment_bytes": 50 << 20,
	"import_archive_bytes": 500 << 20, "archive_expanded_bytes": 1 << 30, "archive_entries": 10000,
	"tree_depth": 32, "search_characters": 200, "default_page_size": 20, "max_page_size": 100,
	"tree_nodes": MaxTreeNodes,
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	state := a.identity.Load().(identity)
	maintenance := a.maintenance.Load()
	WriteData(w, 200, map[string]any{"instance_id": state.InstanceID, "data_epoch": state.DataEpoch, "version": Version, "schema_version": SchemaVersion, "database_status": "ready", "writable": !maintenance, "maintenance": maintenance, "limits": ResourceLimits}, nil)
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	settings := map[string]any{"data_dir": a.DataDir, "limits": ResourceLimits, "startup_settings_read_only": true, "restart_required": false}
	_, _, configErr := decisionConfig()
	settings["import_classifier_configured"] = configErr == nil
	rows, err := a.DB.QueryContext(r.Context(), "SELECT key,value_json FROM settings WHERE key IN ('history_limit','locale','timezone','theme','import_auto_classify')")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		var value any
		if err = rows.Scan(&key, &raw); err == nil {
			err = json.Unmarshal([]byte(raw), &value)
		}
		if err != nil {
			WriteError(w, r, err)
			return
		}
		settings[key] = value
	}
	if err = rows.Err(); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 200, settings, nil)
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HistoryLimit            *int    `json:"history_limit"`
		Locale                  *string `json:"locale"`
		Timezone                *string `json:"timezone"`
		Theme                   *string `json:"theme"`
		ImportAutoClassify      *bool   `json:"import_auto_classify"`
		ConfirmHistoryReduction bool    `json:"confirm_history_reduction"`
	}
	if err := Decode(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	if req.HistoryLimit != nil && (*req.HistoryLimit < 1 || *req.HistoryLimit > 1000) {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "history_limit 必须为 1 到 1000 的整数", nil))
		return
	}
	if req.Locale != nil && *req.Locale != "zh-CN" {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "当前仅支持简体中文 zh-CN", nil))
		return
	}
	if req.Timezone != nil {
		if _, err := time.LoadLocation(*req.Timezone); *req.Timezone == "" || err != nil {
			WriteError(w, r, Err(400, "INVALID_ARGUMENT", "timezone 必须为有效的 IANA 时区", nil))
			return
		}
	}
	if req.Theme != nil && *req.Theme != "auto" && *req.Theme != "light" && *req.Theme != "dark" {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "theme 必须为 auto、light 或 dark", nil))
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(r.Context(), "SELECT value_json FROM settings WHERE key='history_limit'").Scan(&raw); err != nil {
		WriteError(w, r, err)
		return
	}
	var old int
	json.Unmarshal([]byte(raw), &old)
	if req.HistoryLimit != nil && *req.HistoryLimit < old && !req.ConfirmHistoryReduction {
		WriteError(w, r, Err(409, "CONFIRMATION_REQUIRED", "减少历史上限将删除较旧快照，请确认后提交", map[string]any{"current_limit": old, "requested_limit": *req.HistoryLimit}))
		return
	}
	if req.HistoryLimit != nil && *req.HistoryLimit < old {
		// Window ranking handles all documents including trash. References are
		// removed before snapshots, so resource retention remains consistent.
		query := `SELECT id FROM (SELECT id,row_number() OVER(PARTITION BY document_id ORDER BY created_at DESC,revision DESC,id DESC) AS rn FROM document_revisions) WHERE rn>?`
		rows, e := tx.QueryContext(r.Context(), query, *req.HistoryLimit)
		if e != nil {
			WriteError(w, r, e)
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				WriteError(w, r, e)
				return
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			WriteError(w, r, e)
			return
		}
		for _, id := range ids {
			if _, err = tx.ExecContext(r.Context(), "DELETE FROM revision_attachments WHERE revision_id=?", id); err != nil {
				WriteError(w, r, err)
				return
			}
			if _, err = tx.ExecContext(r.Context(), "DELETE FROM document_revisions WHERE id=?", id); err != nil {
				WriteError(w, r, err)
				return
			}
		}
	}
	updates := map[string]any{}
	if req.HistoryLimit != nil {
		updates["history_limit"] = *req.HistoryLimit
	}
	if req.Locale != nil {
		updates["locale"] = *req.Locale
	}
	if req.Timezone != nil {
		updates["timezone"] = *req.Timezone
	}
	if req.Theme != nil {
		updates["theme"] = *req.Theme
	}
	if req.ImportAutoClassify != nil {
		updates["import_auto_classify"] = *req.ImportAutoClassify
	}
	for key, value := range updates {
		encoded, _ := json.Marshal(value)
		if _, err = tx.ExecContext(r.Context(), "UPDATE settings SET value_json=?,updated_at=? WHERE key=?", string(encoded), Now(), key); err != nil {
			WriteError(w, r, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		WriteError(w, r, err)
		return
	}
	a.getSettings(w, r)
}

func (a *App) rebuildIndex(w http.ResponseWriter, r *http.Request) {
	a.maintenance.Store(true)
	defer a.maintenance.Store(false)
	var cursor int64
	count := 0
	for {
		rows, err := a.DB.QueryContext(r.Context(), "SELECT row_id,id,revision,markdown FROM documents WHERE row_id>? ORDER BY row_id LIMIT 100", cursor)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		type source struct {
			RowID    int64
			ID       string
			Revision int
			Markdown string
		}
		batch := []source{}
		for rows.Next() {
			var s source
			if err = rows.Scan(&s.RowID, &s.ID, &s.Revision, &s.Markdown); err != nil {
				rows.Close()
				WriteError(w, r, err)
				return
			}
			batch = append(batch, s)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if len(batch) == 0 {
			break
		}
		for _, s := range batch {
			rendered := RenderMarkdown(s.Markdown)
			if _, err = a.DB.ExecContext(r.Context(), "UPDATE documents SET html=?,search_text=?,render_version=? WHERE id=? AND revision=?", rendered.HTML, rendered.SearchText, rendered.Version, s.ID, s.Revision); err != nil {
				WriteError(w, r, err)
				return
			}
			cursor = s.RowID
			count++
		}
	}
	if _, err := a.DB.ExecContext(r.Context(), "INSERT INTO documents_fts(documents_fts) VALUES('rebuild')"); err != nil {
		WriteError(w, r, err)
		return
	}
	if _, err := a.DB.ExecContext(r.Context(), "INSERT INTO documents_fts(documents_fts,rank) VALUES('integrity-check',1)"); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 200, map[string]any{"rebuilt": true, "documents_processed": count, "render_version": RenderVersion}, nil)
}

func (a *App) checkIntegrity(w http.ResponseWriter, r *http.Request) {
	var integrity string
	if err := a.DB.QueryRowContext(r.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
		WriteError(w, r, err)
		return
	}
	if integrity != "ok" {
		WriteError(w, r, Err(409, "DATABASE_INTEGRITY_ERROR", "数据库完整性检查失败，请恢复可信备份", nil))
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	broken := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if broken {
		WriteError(w, r, Err(409, "DATABASE_INTEGRITY_ERROR", "数据库外键关系损坏，请恢复可信备份", nil))
		return
	}
	if err = a.CheckTreeIntegrity(r.Context()); err != nil {
		WriteError(w, r, err)
		return
	}
	if _, err = a.DB.ExecContext(r.Context(), "INSERT INTO documents_fts(documents_fts,rank) VALUES('integrity-check',1)"); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 200, map[string]any{"database": "ok", "foreign_keys": "ok", "trees": "ok", "fts": "ok"}, nil)
}
