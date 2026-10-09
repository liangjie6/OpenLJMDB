package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
)

type APIError struct {
	Status        int
	Code, Message string
	Details       any
}

func (e *APIError) Error() string { return e.Message }
func Err(status int, code, message string, details any) error {
	return &APIError{status, code, message, details}
}

func WriteData(w http.ResponseWriter, status int, data, meta any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	response := map[string]any{"data": data}
	if meta != nil {
		response["meta"] = meta
	}
	json.NewEncoder(w).Encode(response)
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if entry, ok := r.Context().Value(requestLogKey{}).(requestLog); ok && entry.Logger != nil {
		entry.Logger.Printf("request_id=%s error=%s", entry.ID, safeErrorClass(err))
	}
	var e *APIError
	if !errors.As(err, &e) {
		switch {
		case errors.Is(err, syscall.ENOSPC):
			e = &APIError{507, "INSUFFICIENT_STORAGE", "磁盘空间不足，请释放空间后重试", nil}
		case strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked"):
			e = &APIError{503, "DATABASE_BUSY", "数据库繁忙，请稍后重试", nil}
		case strings.Contains(err.Error(), "SQLITE_FULL"):
			e = &APIError{507, "INSUFFICIENT_STORAGE", "磁盘空间不足，请释放空间后重试", nil}
		default:
			e = &APIError{500, "INTERNAL_ERROR", "操作失败，请依据请求编号检查服务日志", nil}
		}
	}
	if e.Status == 503 {
		w.Header().Set("Retry-After", "2")
	}
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": e.Code, "message": e.Message, "details": details}, "request_id": w.Header().Get("X-Request-ID")})
}

func Decode(r *http.Request, dst any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return Err(415, "UNSUPPORTED_FILE_TYPE", "请求正文必须使用 application/json", nil)
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return Err(413, "PAYLOAD_TOO_LARGE", "请求正文超过限制", map[string]any{"limit_bytes": limit.Limit})
		}
		return Err(400, "INVALID_ARGUMENT", "JSON 格式或字段不正确", nil)
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return Err(400, "INVALID_ARGUMENT", "请求只能包含一个 JSON 对象", nil)
	}
	return nil
}

func Page(r *http.Request) (int, int, error) {
	page, size := 1, 20
	for key, target := range map[string]*int{"page": &page, "page_size": &size} {
		if raw, ok := r.URL.Query()[key]; ok {
			if len(raw) != 1 {
				return 0, 0, Err(400, "INVALID_ARGUMENT", "分页参数不能重复", nil)
			}
			value, err := strconv.Atoi(raw[0])
			if err != nil || value < 1 {
				return 0, 0, Err(400, "INVALID_ARGUMENT", "分页参数必须为正整数", nil)
			}
			*target = value
		}
	}
	if size > 100 || page > 10000000 {
		return 0, 0, Err(400, "INVALID_ARGUMENT", "分页范围超过限制", map[string]any{"max_page_size": 100})
	}
	return page, size, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	a.RegisterCore(mux)
	a.RegisterAssets(mux)
	a.RegisterTransfer(mux)
	mux.HandleFunc("GET /api/v1/health", a.health)
	mux.HandleFunc("GET /api/v1/settings", a.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", a.putSettings)
	mux.HandleFunc("GET /api/v1/documents/{id}/status", a.documentLinkStatus)
	mux.HandleFunc("POST /api/v1/maintenance/rebuild-index", a.rebuildIndex)
	mux.HandleFunc("POST /api/v1/maintenance/check-integrity", a.checkIntegrity)
	mux.HandleFunc("/", a.staticOrNotFound)
	return a.protect(mux)
}

func (a *App) hostAllowed(host string) bool {
	if a.AllowedHosts != nil {
		return a.AllowedHosts[strings.ToLower(host)]
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	return name == "localhost" || name == "127.0.0.1" || name == "::1" || name == "[::1]"
}

func (a *App) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := NewID()
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), requestLogKey{}, requestLog{a.Logger, id}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		defer func() {
			if recover() != nil {
				WriteError(w, r, Err(500, "INTERNAL_ERROR", "服务处理请求时发生异常", nil))
			}
		}()
		if !a.hostAllowed(r.Host) {
			WriteError(w, r, Err(403, "INVALID_HOST", "访问主机不在允许范围内", nil))
			return
		}
		write := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if write {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Scheme != "http" || u.Host != r.Host || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
					WriteError(w, r, Err(403, "INVALID_ORIGIN", "拒绝跨来源写入请求", nil))
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				WriteError(w, r, Err(403, "INVALID_ORIGIN", "拒绝跨站写入请求", nil))
				return
			}
		}
		// JSON escaping can expand a valid 10 MiB source string sixfold.
		limit := int64(61 << 20)
		switch r.URL.Path {
		case "/api/v1/attachments":
			limit = 51 << 20
		case "/api/v1/imports/preflight":
			limit = 501 << 20
		case "/api/v1/backups/validate":
			limit = 1 << 62
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if r.Method == "POST" && r.URL.Path == "/api/v1/backups/restore" && a.RestoreReplay(w, r) {
			return
		}
		if r.URL.Path == "/api/v1/health" || r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/jobs/") && !strings.HasSuffix(r.URL.Path, "/download") {
			next.ServeHTTP(w, r)
			return
		}
		if a.maintenance.Load() {
			WriteError(w, r, Err(503, "MAINTENANCE_MODE", "正在备份或恢复，请保留草稿并稍后重试", nil))
			return
		}
		a.Mu.RLock()
		defer a.Mu.RUnlock()
		if a.maintenance.Load() {
			WriteError(w, r, Err(503, "MAINTENANCE_MODE", "正在维护，请稍后重试", nil))
			return
		}
		if write {
			// Classification only reads a staged file and calls an external service.
			// Keep epoch/restore protection, but do not block local saves while waiting.
			if r.URL.Path != "/api/v1/imports/classify" {
				a.WriteMu.Lock()
				defer a.WriteMu.Unlock()
			}
			state := a.identity.Load().(identity)
			if r.Header.Get("X-Data-Epoch") != state.DataEpoch {
				WriteError(w, r, Err(409, "DATA_EPOCH_CHANGED", "数据集合已发生变化，请保留草稿并重新加载", map[string]any{"data_epoch": state.DataEpoch}))
				return
			}
		}
		if write && r.Method == "POST" && r.Header.Get("Idempotency-Key") != "" && r.URL.Path != "/api/v1/backups/restore" {
			media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if media == "application/json" || r.URL.Path == "/api/v1/backups" {
				a.idempotent(w, r, next)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type recordedResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
type idempotencyContextKey struct{}
type idempotencyInput struct{ Scope, Key, Hash string }

// SaveIdempotency commits the response together with the domain mutation.
func SaveIdempotency(ctx context.Context, tx *sql.Tx, status int, data, meta any) error {
	input, ok := ctx.Value(idempotencyContextKey{}).(idempotencyInput)
	if !ok {
		return nil
	}
	response := map[string]any{"data": data}
	if meta != nil {
		response["meta"] = meta
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	saved, err := json.Marshal(recordedResponse{status, body})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO idempotency_records(scope,idempotency_key,request_hash,response_json,created_at) VALUES(?,?,?,?,?)", input.Scope, input.Key, input.Hash, string(saved), Now())
	return err
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (a *App) idempotent(w http.ResponseWriter, r *http.Request, next http.Handler) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 200 || strings.TrimSpace(key) == "" {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "幂等键长度须为 1 到 200 字节", nil))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, r, Err(413, "PAYLOAD_TOO_LARGE", "请求正文超过限制", nil))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	h := sha256.Sum256(body)
	hash := hex.EncodeToString(h[:])
	scope := r.Method + " " + r.URL.Path
	r = r.WithContext(context.WithValue(r.Context(), idempotencyContextKey{}, idempotencyInput{scope, key, hash}))
	var oldHash string
	var raw *string
	err = a.DB.QueryRowContext(r.Context(), "SELECT request_hash,response_json FROM idempotency_records WHERE scope=? AND idempotency_key=?", scope, key).Scan(&oldHash, &raw)
	if err == nil {
		if oldHash != hash {
			WriteError(w, r, Err(409, "IDEMPOTENCY_KEY_REUSED", "该幂等键已用于不同请求", nil))
			return
		}
		if raw != nil {
			var saved recordedResponse
			if json.Unmarshal([]byte(*raw), &saved) == nil {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(saved.Status)
				w.Write(saved.Body)
				return
			}
		}
		WriteError(w, r, Err(409, "IDEMPOTENCY_IN_PROGRESS", "该请求已受理，请查询任务或刷新状态", nil))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		WriteError(w, r, err)
		return
	}
	captured := &captureWriter{ResponseWriter: w}
	next.ServeHTTP(captured, r)
	if captured.status >= 200 && captured.status < 300 {
		saved, _ := json.Marshal(recordedResponse{captured.status, captured.body.Bytes()})
		// Domain jobs also persist their result in the transaction. This record
		// caches the HTTP response for retries whose first response was lost.
		_, err = a.DB.ExecContext(r.Context(), "INSERT INTO idempotency_records(scope,idempotency_key,request_hash,response_json,created_at) VALUES(?,?,?,?,?) ON CONFLICT(scope,idempotency_key) DO UPDATE SET response_json=excluded.response_json", scope, key, hash, string(saved), Now())
		if err != nil {
			fmt.Println("幂等响应缓存写入失败，已提交结果请通过查询核对")
		}
	}
}
