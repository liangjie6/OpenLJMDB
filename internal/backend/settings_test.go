package backend

import "testing"

func TestPreferenceDefaultsUpgradeAndPersistence(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defaults := coreStatus(t, securityRequest(a, "GET", "/api/v1/settings", nil, nil), 200)
	if defaults["locale"] != "zh-CN" || defaults["timezone"] != "Asia/Shanghai" || defaults["theme"] != "auto" || defaults["import_auto_classify"] != false {
		t.Fatal(defaults)
	}
	// Simulate an existing database created before preferences were supported.
	if _, err := a.DB.Exec("DELETE FROM settings WHERE key IN ('locale','timezone','theme','import_auto_classify')"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := coreStatus(t, securityRequest(a, "GET", "/api/v1/settings", nil, nil), 200)
	if upgraded["timezone"] != "Asia/Shanghai" {
		t.Fatal(upgraded)
	}
	patch := map[string]any{"locale": "zh-CN", "timezone": "America/New_York", "theme": "dark", "import_auto_classify": true}
	saved := coreStatus(t, securityRequest(a, "PUT", "/api/v1/settings", patch, nil), 200)
	if saved["timezone"] != patch["timezone"] || saved["theme"] != patch["theme"] || saved["history_limit"] != float64(100) {
		t.Fatal(saved)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	persisted := coreStatus(t, securityRequest(a, "GET", "/api/v1/settings", nil, nil), 200)
	if persisted["timezone"] != patch["timezone"] || persisted["theme"] != patch["theme"] || persisted["import_auto_classify"] != true {
		t.Fatal(persisted)
	}
	for _, patch := range []map[string]any{
		{"timezone": "invalid/timezone", "theme": "light"},
		{"timezone": "", "theme": "light"},
		{"locale": "en-US", "theme": "light"},
		{"theme": "invalid"},
		{"history_limit": 0, "theme": "light"},
		{"import_auto_classify": "true", "theme": "light"},
	} {
		coreStatus(t, securityRequest(a, "PUT", "/api/v1/settings", patch, nil), 400)
	}
	after := coreStatus(t, securityRequest(a, "GET", "/api/v1/settings", nil, nil), 200)
	if after["theme"] != "dark" || after["timezone"] != "America/New_York" {
		t.Fatal("invalid update changed preferences", after)
	}
}
