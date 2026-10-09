package backend

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxAttachmentBytes int64 = 50 << 20

type Attachment struct {
	ID                   string `json:"id"`
	OriginalName         string `json:"original_name"`
	StoragePath          string `json:"-"`
	MediaType            string `json:"media_type"`
	SizeBytes            int64  `json:"size_bytes"`
	SHA256               string `json:"sha256"`
	State                string `json:"state"`
	CreatedAt            int64  `json:"created_at"`
	UpdatedAt            int64  `json:"updated_at"`
	URL                  string `json:"url"`
	CurrentReferences    int    `json:"current_references"`
	HistoricalReferences int    `json:"historical_references"`
	TrashReferences      int    `json:"trash_references"`
}

const attachmentColumns = "a.id,a.original_name,a.storage_path,a.media_type,a.size_bytes,a.sha256,a.state,a.created_at,a.updated_at,(SELECT count(*) FROM document_attachments da JOIN documents d ON d.id=da.document_id JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE da.attachment_id=a.id AND d.deleted_at IS NULL AND k.deleted_at IS NULL),(SELECT count(*) FROM revision_attachments ra WHERE ra.attachment_id=a.id),(SELECT count(*) FROM document_attachments da JOIN documents d ON d.id=da.document_id JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE da.attachment_id=a.id AND (d.deleted_at IS NOT NULL OR k.deleted_at IS NOT NULL))"

type attachmentScanner interface{ Scan(...any) error }

func scanAttachment(row attachmentScanner) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.OriginalName, &a.StoragePath, &a.MediaType, &a.SizeBytes, &a.SHA256, &a.State, &a.CreatedAt, &a.UpdatedAt, &a.CurrentReferences, &a.HistoricalReferences, &a.TrashReferences)
	a.URL = "/api/v1/attachments/" + a.ID + "/content"
	return a, err
}

func attachmentRelativePath(id string) string { return "uploads/" + id[:2] + "/" + id + "/blob" }

// SafeAttachmentPath verifies the immutable, ID-derived path and rejects
// symbolic links at every existing component below the owned data directory.
func (a *App) SafeAttachmentPath(id, storage string) (string, error) {
	if !ValidID(id) || storage != attachmentRelativePath(id) {
		return "", fmt.Errorf("附件存储路径无效")
	}
	full := filepath.Join(a.DataDir, filepath.FromSlash(storage))
	current := a.DataDir
	for _, part := range strings.Split(storage, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("附件路径不允许符号链接")
		}
		if current != full && !info.IsDir() {
			return "", fmt.Errorf("附件目录损坏")
		}
		if current == full && !info.Mode().IsRegular() {
			return "", fmt.Errorf("附件内容不是普通文件")
		}
	}
	return full, nil
}

func (a *App) attachmentLimit() int64 {
	var raw string
	var limit int64
	if a.DB.QueryRow("SELECT value_json FROM settings WHERE key='max_attachment_bytes'").Scan(&raw) == nil && json.Unmarshal([]byte(raw), &limit) == nil && limit > 0 && limit <= MaxAttachmentBytes {
		return limit
	}
	return MaxAttachmentBytes
}

func validAttachmentName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", Err(400, "INVALID_ARGUMENT", "附件文件名必须为 UTF-8", nil)
	}
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 255 {
		return "", Err(400, "INVALID_ARGUMENT", "附件文件名须为 1 到 255 个字符", nil)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", Err(400, "INVALID_ARGUMENT", "附件文件名不能包含控制字符", nil)
		}
	}
	return name, nil
}

// stageAttachment writes and fsyncs the entire bounded stream before any ready
// state is possible. The temporary name also makes interrupted uploads resumable.
func (a *App) stageAttachment(reader io.Reader, name string, maxBytes int64) (Attachment, string, error) {
	name, err := validAttachmentName(name)
	if err != nil {
		return Attachment{}, "", err
	}
	if maxBytes <= 0 || maxBytes > MaxAttachmentBytes {
		maxBytes = MaxAttachmentBytes
	}
	id := NewID()
	now := Now()
	attachment := Attachment{ID: id, OriginalName: name, StoragePath: attachmentRelativePath(id), State: "pending", CreatedAt: now, UpdatedAt: now, URL: "/api/v1/attachments/" + id + "/content"}
	tmp := filepath.Join(a.DataDir, "tmp", "upload-"+id+".partial")
	if info, err := os.Lstat(filepath.Join(a.DataDir, "tmp")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Attachment{}, "", fmt.Errorf("上传暂存目录无效")
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Attachment{}, "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(reader, maxBytes+1))
	if copyErr == nil && n > maxBytes {
		copyErr = Err(413, "PAYLOAD_TOO_LARGE", "附件超过容量限制", map[string]any{"limit_bytes": maxBytes})
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		os.Remove(tmp)
		var limit *http.MaxBytesError
		if errors.As(copyErr, &limit) {
			copyErr = Err(413, "PAYLOAD_TOO_LARGE", "上传请求超过容量限制", map[string]any{"limit_bytes": maxBytes})
		}
		return Attachment{}, "", copyErr
	}
	attachment.SizeBytes = n
	attachment.SHA256 = hex.EncodeToString(h.Sum(nil))
	check, err := os.Open(tmp)
	if err != nil {
		os.Remove(tmp)
		return Attachment{}, "", err
	}
	var sample [512]byte
	count, _ := check.Read(sample[:])
	attachment.MediaType = http.DetectContentType(sample[:count])
	// Only positively decoded raster formats are ever served inline.
	if attachment.MediaType == "image/png" || attachment.MediaType == "image/jpeg" || attachment.MediaType == "image/gif" {
		check.Seek(0, io.SeekStart)
		if _, _, err := image.DecodeConfig(check); err != nil {
			attachment.MediaType = "application/octet-stream"
		}
	}
	check.Close()
	return attachment, tmp, nil
}

func (a *App) finalizeAttachment(attachment Attachment, tmp string) error {
	full, err := a.SafeAttachmentPath(attachment.ID, attachment.StoragePath)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		return err
	}
	if _, err = a.SafeAttachmentPath(attachment.ID, attachment.StoragePath); err != nil {
		return err
	}
	if _, err = os.Lstat(full); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("附件 ID 已存在")
	}
	if err = os.Rename(tmp, full); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(full))
	if err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// PrepareAttachment is used by atomic imports. It makes an immutable file but
// does not write metadata; failed import transactions leave a recoverable,
// unreferenced file rather than a partially visible document tree.
func (a *App) PrepareAttachment(reader io.Reader, name string, maxBytes int64) (Attachment, error) {
	a.FileMu.Lock()
	defer a.FileMu.Unlock()
	attachment, tmp, err := a.stageAttachment(reader, name, maxBytes)
	if err != nil {
		return Attachment{}, err
	}
	if err = a.finalizeAttachment(attachment, tmp); err != nil {
		os.Remove(tmp)
		return Attachment{}, err
	}
	attachment.State = "ready"
	return attachment, nil
}

// RegisterUnreferencedAttachments makes import files visible as manual cleanup
// candidates after a transaction rolls back. Existing committed metadata is
// preserved. The caller must roll back its transaction before calling this
// helper and keep the application's writer lock while registering candidates.
func (a *App) RegisterUnreferencedAttachments(ctx context.Context, attachments []Attachment) error {
	a.FileMu.RLock()
	defer a.FileMu.RUnlock()
	for _, at := range attachments {
		full, err := a.SafeAttachmentPath(at.ID, at.StoragePath)
		if err != nil {
			return err
		}
		info, err := os.Lstat(full)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != at.SizeBytes {
			return fmt.Errorf("未绑定附件文件不完整")
		}
		_, err = a.DB.ExecContext(ctx, "INSERT OR IGNORE INTO attachments(id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'ready',?,?)", at.ID, at.OriginalName, at.StoragePath, at.MediaType, at.SizeBytes, at.SHA256, at.CreatedAt, at.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func InsertAttachment(ctx context.Context, tx *sql.Tx, attachment Attachment) error {
	if !ValidID(attachment.ID) || attachment.StoragePath != attachmentRelativePath(attachment.ID) || attachment.State != "ready" {
		return fmt.Errorf("待导入附件元信息无效")
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO attachments(id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", attachment.ID, attachment.OriginalName, attachment.StoragePath, attachment.MediaType, attachment.SizeBytes, attachment.SHA256, attachment.State, attachment.CreatedAt, attachment.UpdatedAt)
	return err
}

func (a *App) RegisterAssets(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/attachments", a.uploadAttachment)
	mux.HandleFunc("GET /api/v1/attachments", a.listAttachments)
	mux.HandleFunc("GET /api/v1/attachments/{id}", a.getAttachment)
	mux.HandleFunc("GET /api/v1/attachments/{id}/content", a.attachmentContent)
	mux.HandleFunc("HEAD /api/v1/attachments/{id}/content", a.attachmentContent)
	mux.HandleFunc("POST /api/v1/attachments/cleanup-preview", a.cleanupPreview)
	mux.HandleFunc("POST /api/v1/attachments/cleanup", a.cleanupAttachments)
	mux.HandleFunc("GET /api/v1/search", a.search)
}

func attachmentResponseRecord(status int, attachment Attachment) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"data": attachment})
	if err != nil {
		return nil, err
	}
	return json.Marshal(recordedResponse{Status: status, Body: body})
}

func (a *App) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key != "" && (len(key) > 200 || strings.TrimSpace(key) == "") {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "幂等键长度须为 1 到 200 字节", nil))
		return
	}
	multipart, err := r.MultipartReader()
	if err != nil {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "上传必须使用 multipart/form-data", nil))
		return
	}
	part, err := multipart.NextPart()
	if err != nil || part.FileName() == "" || part.FormName() != "file" {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "请选择一个 file 附件", nil))
		return
	}
	a.FileMu.Lock()
	defer a.FileMu.Unlock()
	attachment, tmp, err := a.stageAttachment(part, part.FileName(), a.attachmentLimit())
	part.Close()
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// Until metadata is committed the temporary file has no recovery identity.
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			os.Remove(tmp)
		}
	}()
	if next, err := multipart.NextPart(); err != io.EOF {
		if next != nil {
			next.Close()
		}
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "每次上传只能包含一个附件", nil))
		return
	}
	canonical, _ := json.Marshal(struct {
		Name, SHA256 string
		Size         int64
	}{attachment.OriginalName, attachment.SHA256, attachment.SizeBytes})
	digest := sha256.Sum256(canonical)
	requestHash := hex.EncodeToString(digest[:])
	const scope = "POST /api/v1/attachments"
	reused := false
	if key != "" {
		var oldHash string
		var raw sql.NullString
		err = a.DB.QueryRowContext(r.Context(), "SELECT request_hash,response_json FROM idempotency_records WHERE scope=? AND idempotency_key=?", scope, key).Scan(&oldHash, &raw)
		if err == nil {
			if oldHash != requestHash {
				WriteError(w, r, Err(409, "IDEMPOTENCY_KEY_REUSED", "该幂等键已用于不同附件或文件名", nil))
				return
			}
			var saved recordedResponse
			var body struct {
				Data Attachment `json:"data"`
			}
			if !raw.Valid || json.Unmarshal([]byte(raw.String), &saved) != nil || json.Unmarshal(saved.Body, &body) != nil || !ValidID(body.Data.ID) {
				WriteError(w, r, Err(409, "IDEMPOTENCY_IN_PROGRESS", "该上传尚未完成，请稍后重试", nil))
				return
			}
			if saved.Status == 201 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(201)
				w.Write(saved.Body)
				return
			}
			attachment, err = scanAttachment(a.DB.QueryRowContext(r.Context(), "SELECT "+attachmentColumns+" FROM attachments a WHERE a.id=?", body.Data.ID))
			if err != nil {
				WriteError(w, r, err)
				return
			}
			if attachment.State != "pending" && attachment.State != "missing" && attachment.State != "ready" {
				WriteError(w, r, Err(409, "ATTACHMENT_UNAVAILABLE", "此前上传的附件当前不可用", map[string]any{"attachment_id": attachment.ID}))
				return
			}
			reused = true
		} else if err != sql.ErrNoRows {
			WriteError(w, r, err)
			return
		}
	}
	if !reused {
		tx, err := a.DB.BeginTx(r.Context(), nil)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(r.Context(), "INSERT INTO attachments(id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", attachment.ID, attachment.OriginalName, attachment.StoragePath, attachment.MediaType, attachment.SizeBytes, attachment.SHA256, "pending", attachment.CreatedAt, attachment.UpdatedAt)
		if err == nil && key != "" {
			record, encodeErr := attachmentResponseRecord(202, attachment)
			err = encodeErr
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "INSERT INTO idempotency_records(scope,idempotency_key,request_hash,response_json,created_at) VALUES(?,?,?,?,?)", scope, key, requestHash, string(record), Now())
			}
		}
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if err = tx.Commit(); err != nil {
			WriteError(w, r, err)
			return
		}
		keepTemporary = true
	}
	if reused {
		full, pathErr := a.SafeAttachmentPath(attachment.ID, attachment.StoragePath)
		if pathErr != nil {
			WriteError(w, r, pathErr)
			return
		}
		if _, statErr := os.Lstat(full); statErr == nil {
			if err = verifyBlob(full, attachment.SizeBytes, attachment.SHA256); err != nil {
				WriteError(w, r, Err(409, "ATTACHMENT_DAMAGED", "此前上传的文件校验失败，请先修复附件", map[string]any{"attachment_id": attachment.ID}))
				return
			}
			os.Remove(tmp)
		} else if errors.Is(statErr, os.ErrNotExist) {
			if err = a.finalizeAttachment(attachment, tmp); err != nil {
				WriteError(w, r, err)
				return
			}
		} else {
			WriteError(w, r, statErr)
			return
		}
		os.Remove(filepath.Join(a.DataDir, "tmp", "upload-"+attachment.ID+".partial"))
	} else if err = a.finalizeAttachment(attachment, tmp); err != nil {
		WriteError(w, r, err)
		return
	}
	attachment.State = "ready"
	attachment.UpdatedAt = Now()
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), "UPDATE attachments SET state='ready',updated_at=? WHERE id=? AND state IN ('pending','missing','ready')", attachment.UpdatedAt, attachment.ID)
	if err == nil && key != "" {
		record, encodeErr := attachmentResponseRecord(201, attachment)
		err = encodeErr
		if err == nil {
			_, err = tx.ExecContext(r.Context(), "UPDATE idempotency_records SET response_json=? WHERE scope=? AND idempotency_key=? AND request_hash=?", string(record), scope, key, requestHash)
		}
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 201, attachment, nil)
}

func (a *App) listAttachments(w http.ResponseWriter, r *http.Request) {
	page, size, err := Page(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	where := "a.state<>'deleted'"
	filter := r.URL.Query().Get("filter")
	if filter == "" && r.URL.Query().Get("unreferenced") == "true" {
		filter = "unreferenced"
	}
	if filter == "" && r.URL.Query().Get("missing") == "true" {
		filter = "missing"
	}
	switch filter {
	case "":
	case "unreferenced":
		where += " AND a.state='ready' AND NOT EXISTS(SELECT 1 FROM document_attachments WHERE attachment_id=a.id) AND NOT EXISTS(SELECT 1 FROM revision_attachments WHERE attachment_id=a.id)"
	case "missing":
		where += " AND a.state='missing'"
	default:
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "附件筛选条件无效", nil))
		return
	}
	var total int
	if err = a.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM attachments a WHERE "+where).Scan(&total); err != nil {
		WriteError(w, r, err)
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), "SELECT "+attachmentColumns+" FROM attachments a WHERE "+where+" ORDER BY a.created_at DESC,a.id ASC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer rows.Close()
	list := []Attachment{}
	for rows.Next() {
		at, err := scanAttachment(rows)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		list = append(list, at)
	}
	if err = rows.Err(); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 200, list, map[string]any{"total": total, "page": page, "page_size": size})
}

func (a *App) getAttachment(w http.ResponseWriter, r *http.Request) {
	attachment, err := scanAttachment(a.DB.QueryRowContext(r.Context(), "SELECT "+attachmentColumns+" FROM attachments a WHERE a.id=?", r.PathValue("id")))
	if err == sql.ErrNoRows {
		err = Err(404, "NOT_FOUND", "附件不存在", nil)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 200, attachment, nil)
}

func (a *App) attachmentContent(w http.ResponseWriter, r *http.Request) {
	a.FileMu.RLock()
	defer a.FileMu.RUnlock()
	attachment, err := scanAttachment(a.DB.QueryRowContext(r.Context(), "SELECT "+attachmentColumns+" FROM attachments a WHERE a.id=?", r.PathValue("id")))
	if err == sql.ErrNoRows || err == nil && attachment.State != "ready" {
		WriteError(w, r, Err(404, "ATTACHMENT_UNAVAILABLE", "附件不存在或当前不可用", nil))
		return
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	full, err := a.SafeAttachmentPath(attachment.ID, attachment.StoragePath)
	if err != nil {
		WriteError(w, r, Err(404, "ATTACHMENT_UNAVAILABLE", "附件文件不可用", nil))
		return
	}
	f, err := os.Open(full)
	if err != nil {
		WriteError(w, r, Err(404, "ATTACHMENT_UNAVAILABLE", "附件文件缺失或不可读", nil))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != attachment.SizeBytes {
		WriteError(w, r, Err(404, "ATTACHMENT_UNAVAILABLE", "附件文件损坏", nil))
		return
	}
	disposition, media := "attachment", "application/octet-stream"
	if attachment.MediaType == "image/png" || attachment.MediaType == "image/jpeg" || attachment.MediaType == "image/gif" {
		disposition = "inline"
		media = attachment.MediaType
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.OriginalName}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("ETag", `"`+attachment.SHA256+`"`)
	http.ServeContent(w, r, attachment.OriginalName, info.ModTime(), f)
}

type cleanupRequest struct {
	AttachmentIDs     []string `json:"attachment_ids"`
	ConfirmationToken string   `json:"confirmation_token,omitempty"`
}

func normalizedAttachmentIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, Err(400, "INVALID_ARGUMENT", "请选择 1 到 1000 个附件", nil)
	}
	set := map[string]bool{}
	for _, id := range ids {
		if !ValidID(id) {
			return nil, Err(400, "INVALID_ARGUMENT", "附件 ID 无效", nil)
		}
		set[id] = true
	}
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func (a *App) cleanupCandidates(ctx context.Context, ids []string) ([]Attachment, error) {
	list := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		at, err := scanAttachment(a.DB.QueryRowContext(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.id=?", id))
		if err == sql.ErrNoRows {
			return nil, Err(404, "NOT_FOUND", "待清理附件不存在", map[string]any{"attachment_id": id})
		}
		if err != nil {
			return nil, err
		}
		list = append(list, at)
	}
	return list, nil
}
func cleanupPayload(list []Attachment) any {
	type item struct {
		ID, State, SHA256       string
		Current, History, Trash int
	}
	items := make([]item, 0, len(list))
	for _, a := range list {
		items = append(items, item{a.ID, a.State, a.SHA256, a.CurrentReferences, a.HistoricalReferences, a.TrashReferences})
	}
	return items
}

func (a *App) cleanupPreview(w http.ResponseWriter, r *http.Request) {
	var request cleanupRequest
	if err := Decode(r, &request); err != nil {
		WriteError(w, r, err)
		return
	}
	ids, err := normalizedAttachmentIDs(request.AttachmentIDs)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	list, err := a.cleanupCandidates(r.Context(), ids)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var bytes int64
	canDelete := true
	for _, at := range list {
		if at.CurrentReferences+at.HistoricalReferences+at.TrashReferences > 0 || at.State != "ready" && at.State != "missing" {
			canDelete = false
		}
		bytes += at.SizeBytes
	}
	token := ""
	if canDelete {
		token = a.Token("attachment-cleanup", cleanupPayload(list))
	}
	WriteData(w, 200, map[string]any{"attachments": list, "size_bytes": bytes, "can_delete": canDelete, "confirmation_token": token, "expires_in_seconds": 600}, nil)
}

func (a *App) cleanupAttachments(w http.ResponseWriter, r *http.Request) {
	var request cleanupRequest
	if err := Decode(r, &request); err != nil {
		WriteError(w, r, err)
		return
	}
	ids, err := normalizedAttachmentIDs(request.AttachmentIDs)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	list, err := a.cleanupCandidates(r.Context(), ids)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if !a.VerifyToken(request.ConfirmationToken, "attachment-cleanup", cleanupPayload(list)) {
		WriteError(w, r, Err(409, "CONFIRMATION_EXPIRED", "清理预览已过期或引用发生变化，请重新预览", nil))
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	tasks := []string{}
	for _, at := range list {
		var refs int
		var state string
		err = tx.QueryRowContext(r.Context(), "SELECT state,(SELECT count(*) FROM document_attachments WHERE attachment_id=attachments.id)+(SELECT count(*) FROM revision_attachments WHERE attachment_id=attachments.id) FROM attachments WHERE id=?", at.ID).Scan(&state, &refs)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if refs > 0 {
			WriteError(w, r, Err(409, "ATTACHMENT_IN_USE", "附件仍被正文、历史或回收站引用", map[string]any{"attachment_id": at.ID, "references": refs}))
			return
		}
		if state != "ready" && state != "missing" {
			WriteError(w, r, Err(409, "ATTACHMENT_UNAVAILABLE", "附件当前不能清理", nil))
			return
		}
		taskID := NewID()
		if _, err = tx.ExecContext(r.Context(), "UPDATE attachments SET state='pending_delete',updated_at=? WHERE id=?", Now(), at.ID); err != nil {
			WriteError(w, r, err)
			return
		}
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO cleanup_tasks(id,attachment_id,state,created_at,updated_at) VALUES(?,?,'queued',?,?)", taskID, at.ID, Now(), Now()); err != nil {
			WriteError(w, r, err)
			return
		}
		tasks = append(tasks, taskID)
	}
	if err = tx.Commit(); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 202, map[string]any{"task_ids": tasks, "state": "queued"}, nil)
}

// StartCleanupWorker is called once by the application after startup recovery.
func (a *App) StartCleanupWorker() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				if a.maintenance.Load() {
					continue
				}
				a.Mu.RLock()
				a.WriteMu.Lock()
				if !a.maintenance.Load() {
					if err := a.drainCleanup(a.ctx); err != nil {
						if a.Logger != nil {
							a.Logger.Printf("附件清理任务失败，将稍后重试: %s", safeErrorClass(err))
						}
					}
				}
				a.WriteMu.Unlock()
				a.Mu.RUnlock()
			}
		}
	}()
}

func (a *App) drainCleanup(ctx context.Context) error {
	rows, err := a.DB.QueryContext(ctx, "SELECT id,attachment_id FROM cleanup_tasks WHERE state IN ('queued','running','failed') AND (next_attempt_at IS NULL OR next_attempt_at<=?) ORDER BY created_at,id LIMIT 100", Now())
	if err != nil {
		return err
	}
	type task struct{ ID, AttachmentID string }
	var tasks []task
	for rows.Next() {
		var t task
		if err = rows.Scan(&t.ID, &t.AttachmentID); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, t := range tasks {
		var state, storage string
		var refs int
		err = a.DB.QueryRowContext(ctx, "SELECT state,storage_path,(SELECT count(*) FROM document_attachments WHERE attachment_id=attachments.id)+(SELECT count(*) FROM revision_attachments WHERE attachment_id=attachments.id) FROM attachments WHERE id=?", t.AttachmentID).Scan(&state, &storage, &refs)
		if err != nil {
			return err
		}
		if refs > 0 || state != "pending_delete" && state != "deleted" {
			_, err = a.DB.ExecContext(ctx, "UPDATE cleanup_tasks SET state='cancelled',last_error='附件状态或引用变化，已取消清理',updated_at=? WHERE id=?", Now(), t.ID)
			if err != nil {
				return err
			}
			if state == "pending_delete" {
				availableState := "ready"
				a.FileMu.RLock()
				full, pathErr := a.SafeAttachmentPath(t.AttachmentID, storage)
				if pathErr != nil {
					availableState = "missing"
				} else if info, statErr := os.Lstat(full); statErr != nil || !info.Mode().IsRegular() {
					availableState = "missing"
				}
				a.FileMu.RUnlock()
				if _, err = a.DB.ExecContext(ctx, "UPDATE attachments SET state=?,updated_at=? WHERE id=?", availableState, Now(), t.AttachmentID); err != nil {
					return err
				}
			}
			if a.Logger != nil {
				a.Logger.Printf("附件清理任务 %s 已取消：状态或引用变化，需要核对引用", t.ID)
			}
			continue
		}
		if _, err = a.DB.ExecContext(ctx, "UPDATE cleanup_tasks SET state='running',attempts=attempts+1,updated_at=? WHERE id=?", Now(), t.ID); err != nil {
			return err
		}
		a.FileMu.Lock()
		full, pathErr := a.SafeAttachmentPath(t.AttachmentID, storage)
		if pathErr == nil {
			pathErr = os.Remove(full)
			if errors.Is(pathErr, os.ErrNotExist) {
				pathErr = nil
			}
		}
		a.FileMu.Unlock()
		if pathErr != nil {
			if _, err = a.DB.ExecContext(ctx, "UPDATE cleanup_tasks SET state='failed',next_attempt_at=?,last_error='文件删除失败，请检查磁盘权限',updated_at=? WHERE id=?", Now()+30000, Now(), t.ID); err != nil {
				return err
			}
			continue
		}
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE attachments SET state='deleted',updated_at=? WHERE id=?", Now(), t.AttachmentID); err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE cleanup_tasks SET state='done',last_error=NULL,next_attempt_at=NULL,updated_at=? WHERE id=?", Now(), t.ID)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// RecoverFiles reconciles upload crash windows and reports missing files. It
// never deletes an unreferenced blob: orphan files become visible candidates.
func (a *App) RecoverFiles() error {
	a.FileMu.Lock()
	if info, err := os.Lstat(filepath.Join(a.DataDir, "tmp")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		a.FileMu.Unlock()
		return fmt.Errorf("上传暂存目录无效")
	}
	rows, err := a.DB.Query("SELECT id,storage_path,state,size_bytes,sha256 FROM attachments")
	if err != nil {
		a.FileMu.Unlock()
		return err
	}
	type record struct {
		ID, Path, State, Hash string
		Size                  int64
	}
	var records []record
	known := map[string]bool{}
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.ID, &r.Path, &r.State, &r.Size, &r.Hash); err != nil {
			rows.Close()
			a.FileMu.Unlock()
			return err
		}
		records = append(records, r)
		known[r.ID] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.FileMu.Unlock()
		return err
	}
	for _, r := range records {
		if r.State == "deleted" {
			continue
		}
		full, pathErr := a.SafeAttachmentPath(r.ID, r.Path)
		if pathErr != nil {
			a.FileMu.Unlock()
			return pathErr
		}
		if r.State == "pending" {
			if _, err = os.Stat(full); errors.Is(err, os.ErrNotExist) {
				tmp := filepath.Join(a.DataDir, "tmp", "upload-"+r.ID+".partial")
				if verifyBlob(tmp, r.Size, r.Hash) == nil {
					if err = a.finalizeAttachment(Attachment{ID: r.ID, StoragePath: r.Path}, tmp); err != nil {
						a.FileMu.Unlock()
						return err
					}
				}
			}
			if verifyBlob(full, r.Size, r.Hash) == nil {
				_, err = a.DB.Exec("UPDATE attachments SET state='ready',updated_at=? WHERE id=?", Now(), r.ID)
			} else {
				_, err = a.DB.Exec("UPDATE attachments SET state='missing',updated_at=? WHERE id=?", Now(), r.ID)
			}
		} else if r.State == "ready" {
			info, statErr := os.Lstat(full)
			if statErr != nil || !info.Mode().IsRegular() || info.Size() != r.Size {
				_, err = a.DB.Exec("UPDATE attachments SET state='missing',updated_at=? WHERE id=?", Now(), r.ID)
			}
		}
		if err != nil {
			a.FileMu.Unlock()
			return err
		}
	}
	unmanagedFiles := 0
	err = filepath.WalkDir(filepath.Join(a.DataDir, "uploads"), func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("附件目录包含符号链接")
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != "blob" {
			unmanagedFiles++
			return nil
		}
		id := filepath.Base(filepath.Dir(full))
		if !ValidID(id) {
			unmanagedFiles++
			return nil
		}
		if known[id] {
			return nil
		}
		storage := attachmentRelativePath(id)
		expected, err := a.SafeAttachmentPath(id, storage)
		if err != nil {
			return err
		}
		if expected != full {
			return fmt.Errorf("孤立附件路径无效")
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		h := sha256.New()
		size, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		now := Now()
		_, err = a.DB.Exec("INSERT INTO attachments(id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at) VALUES(?,?,?,'application/octet-stream',?,?,'ready',?,?)", id, "恢复的未绑定附件-"+id, storage, size, hex.EncodeToString(h.Sum(nil)), now, now)
		return err
	})
	a.FileMu.Unlock()
	if unmanagedFiles > 0 && a.Logger != nil {
		a.Logger.Printf("附件目录含 %d 个不符合存储布局的文件，已保留，请人工核对", unmanagedFiles)
	}
	if err != nil {
		return err
	}
	return a.drainCleanup(context.Background())
}

func verifyBlob(full string, size int64, hash string) error {
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf("附件大小或类型不一致")
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != hash {
		return fmt.Errorf("附件校验失败")
	}
	return nil
}
