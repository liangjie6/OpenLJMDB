package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrecedenceAndLoopback(t *testing.T) {
	t.Setenv("LJMDB_HOST", "127.0.0.1")
	t.Setenv("LJMDB_PORT", "9000")
	t.Setenv("LJMDB_DATA_DIR", t.TempDir())
	c, err := Parse([]string{"--port", "9001", "--no-open"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 9001 || !c.NoOpen || !filepath.IsAbs(c.DataDir) {
		t.Fatalf("unexpected %+v", c)
	}
	for _, args := range [][]string{{"--host", "0.0.0.0"}, {"--host", "example.com"}, {"--port", "0"}, {"--portable"}, {"--data-dir", "x", "unexpected"}} {
		if _, err := Parse(args); err == nil {
			t.Fatal("accepted", args)
		}
	}
}

func TestInvalidEnvironmentPortCanBeOverridden(t *testing.T) {
	t.Setenv("LJMDB_HOST", "127.0.0.1")
	t.Setenv("LJMDB_PORT", "invalid")
	t.Setenv("LJMDB_DATA_DIR", t.TempDir())
	c, err := Parse([]string{"--port", "8081"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8081 {
		t.Fatalf("port = %d, want 8081", c.Port)
	}
	if _, err := Parse(nil); err == nil {
		t.Fatal("accepted invalid environment port without a CLI override")
	}
}

func TestLoadEnvFile(t *testing.T) {
	t.Setenv("LJMDB_TEST_BASE_URL", "")
	t.Setenv("LJMDB_TEST_API_KEY", "")
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := `# comment line
LJMDB_TEST_BASE_URL="https://example.com"
export LJMDB_TEST_API_KEY='secret-key-123'
EMPTY_VAL=
`
	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	loadEnvFile(envPath)
	if got := os.Getenv("LJMDB_TEST_BASE_URL"); got != "https://example.com" {
		t.Fatalf("expected https://example.com, got %s", got)
	}
	if got := os.Getenv("LJMDB_TEST_API_KEY"); got != "secret-key-123" {
		t.Fatalf("expected secret-key-123, got %s", got)
	}
}
