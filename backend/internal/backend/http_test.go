package backend

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func securityRequest(a *App, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, "http://localhost"+path, reader)
	r.Header.Set("Content-Type", "application/json")
	if state := a.identity.Load(); state != nil {
		r.Header.Set("X-Data-Epoch", state.(identity).DataEpoch)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func responseData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("bad JSON: %s", w.Body.String())
	}
	return response.Data
}

func TestLocalSecurityAndIdempotentCreation(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	payload := map[string]any{"name": "资料", "description": "中文"}
	for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"Sec-Fetch-Site": "cross-site"}} {
		w := securityRequest(a, "POST", "/api/v1/knowledge-bases", payload, headers)
		if w.Code != 403 {
			t.Fatalf("cross-site accepted: %d %s", w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "http://evil.example/api/v1/health", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("Host accepted: %d", w.Code)
	}
	w = securityRequest(a, "POST", "/api/v1/knowledge-bases", payload, map[string]string{"X-Data-Epoch": "old"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "DATA_EPOCH_CHANGED") {
		t.Fatal(w.Body.String())
	}
	first := securityRequest(a, "POST", "/api/v1/knowledge-bases", payload, map[string]string{"Idempotency-Key": "create-1"})
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	second := securityRequest(a, "POST", "/api/v1/knowledge-bases", payload, map[string]string{"Idempotency-Key": "create-1"})
	if second.Code != 201 || responseData(t, first)["id"] != responseData(t, second)["id"] {
		t.Fatal("duplicate creation", second.Body.String())
	}
	changed := securityRequest(a, "POST", "/api/v1/knowledge-bases", map[string]any{"name": "不同"}, map[string]string{"Idempotency-Key": "create-1"})
	if changed.Code != 409 {
		t.Fatal("key reused", changed.Body.String())
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM knowledge_bases").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	bad := securityRequest(a, "POST", "/api/v1/knowledge-bases", map[string]any{"name": "正文", "html": "<script>"}, nil)
	if bad.Code != 400 {
		t.Fatal("unknown trusted field", bad.Body.String())
	}
}

func TestDirectoryLockAndPersistedEpoch(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := a.identity.Load().(identity)
	if other, err := New(dir); err == nil {
		other.Close()
		t.Fatal("second instance acquired lock")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if after := b.identity.Load().(identity); after != before {
		t.Fatal("restart changed dataset", before, after)
	}
	file := filepath.Join(t.TempDir(), "not-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if bad, err := New(file); err == nil {
		bad.Close()
		t.Fatal("invalid directory accepted")
	}
}

func TestSettingsReductionAndHealthDuringMaintenance(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := securityRequest(a, "PUT", "/api/v1/settings", map[string]any{"history_limit": 2}, nil)
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	w = securityRequest(a, "PUT", "/api/v1/settings", map[string]any{"history_limit": 2, "confirm_history_reduction": true}, nil)
	if w.Code != 200 || responseData(t, w)["history_limit"] != float64(2) {
		t.Fatal(w.Body.String())
	}
	w = securityRequest(a, "PUT", "/api/v1/settings", map[string]any{"data_dir": "elsewhere"}, nil)
	if w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	a.maintenance.Store(true)
	w = securityRequest(a, "GET", "/api/v1/health", nil, nil)
	if w.Code != 200 || responseData(t, w)["writable"] != false {
		t.Fatal(w.Body.String())
	}
	w = securityRequest(a, "POST", "/api/v1/knowledge-bases", map[string]any{"name": "阻止"}, nil)
	if w.Code != 503 {
		t.Fatal(w.Body.String())
	}
	a.maintenance.Store(false)
}

func TestErrorMessagesDontExposeContent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	w := httptest.NewRecorder()
	w.Header().Set("X-Request-ID", "example")
	WriteError(w, r, io.ErrUnexpectedEOF)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "request_id") || strings.Contains(w.Body.String(), "unexpected EOF") {
		t.Fatal(w.Body.String())
	}
}

func TestStaticIntegrationPreservesAPIIsolation(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.FrontendFS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("frontend-fixture")}, "assets/app.js": &fstest.MapFile{Data: []byte("fixture")}}
	for _, path := range []string{"/", "/documents/" + NewID()} {
		w := securityRequest(a, "GET", path, nil, map[string]string{"Accept": "text/html"})
		if w.Code != 200 || w.Body.String() != "frontend-fixture" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := securityRequest(a, "GET", "/assets/app.js", nil, nil)
	if w.Code != 200 || w.Body.String() != "fixture" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = securityRequest(a, "GET", "/api/v1/not-found", nil, map[string]string{"Accept": "text/html"})
	if w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = securityRequest(a, "GET", "/assets/missing.js", nil, nil)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
