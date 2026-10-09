package backend

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 1

//go:embed migrations/*.sql
var migrationFiles embed.FS

// OpenDB opens the only connection used by this single-user application. The
// transaction lock parameter makes database/sql BeginTx use BEGIN IMMEDIATE.
func OpenDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("无法创建数据目录: %w", err)
	}
	dbPath, err := filepath.Abs(filepath.Join(dataDir, "knowledge.db"))
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}
	q := u.Query()
	q.Set("_txlock", "immediate")
	// Driver-level parameters apply again if database/sql replaces the sole
	// connection after an error; connection-local foreign keys stay enabled.
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(2)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var snapshotPath string
	fail := func(err error) (*sql.DB, error) {
		_ = db.Close()
		if snapshotPath != "" {
			err = fmt.Errorf("%w；升级前数据库恢复快照: %s", err, snapshotPath)
		}
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fail(fmt.Errorf("无法打开数据库: %w", err))
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > SchemaVersion {
		return fail(fmt.Errorf("数据库版本 %d 高于程序支持版本 %d，拒绝写入", version, SchemaVersion))
	}
	var migrationTable int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&migrationTable); err != nil {
		return fail(err)
	}
	if migrationTable == 0 {
		var tables int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return fail(err)
		}
		if version != 0 || tables != 0 {
			return fail(fmt.Errorf("数据库缺少可信迁移记录，拒绝覆盖现有数据"))
		}
	} else {
		var maximum int
		if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&maximum); err != nil {
			return fail(err)
		}
		if maximum > SchemaVersion {
			return fail(fmt.Errorf("数据库迁移版本较新，拒绝写入"))
		}
		if maximum != version {
			return fail(fmt.Errorf("数据库版本与迁移记录不一致，停止启动"))
		}
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fail(err)
	}
	if integrity != "ok" {
		return fail(fmt.Errorf("数据库完整性检查失败: %s", integrity))
	}
	for _, statement := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=FULL"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fail(err)
		}
	}
	if migrationTable != 0 {
		rows, err := db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
		if err != nil {
			return fail(err)
		}
		for rows.Next() {
			var n int
			var checksum string
			if err := rows.Scan(&n, &checksum); err != nil {
				rows.Close()
				return fail(err)
			}
			contents, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/%03d_initial.sql", n))
			if err != nil {
				rows.Close()
				return fail(err)
			}
			sum := sha256.Sum256(contents)
			if checksum != hex.EncodeToString(sum[:]) {
				rows.Close()
				return fail(fmt.Errorf("数据库迁移 %d 校验值不匹配", n))
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fail(err)
		}
	}
	if version < SchemaVersion && migrationTable != 0 {
		snapshotDir := filepath.Join(dataDir, "backups", "pre-upgrade-"+NewID())
		if err := os.MkdirAll(snapshotDir, 0700); err != nil {
			return fail(err)
		}
		path := filepath.Join(snapshotDir, "knowledge.db")
		if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
			return fail(fmt.Errorf("升级前数据库快照失败，升级已取消: %w", err))
		}
		snapshotPath = path
	}
	for n := version + 1; n <= SchemaVersion; n++ {
		contents, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/%03d_initial.sql", n))
		if err != nil {
			return fail(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fail(err)
		}
		if _, err = tx.ExecContext(ctx, string(contents)); err == nil {
			sum := sha256.Sum256(contents)
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,checksum,applied_at) VALUES(?,?,?)", n, hex.EncodeToString(sum[:]), Now())
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", n))
		}
		if err != nil {
			_ = tx.Rollback()
			return fail(fmt.Errorf("数据库迁移失败，停止写服务: %w", err))
		}
		if err = tx.Commit(); err != nil {
			return fail(err)
		}
	}
	if err = CheckSchema(ctx, db); err != nil {
		return fail(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	for key, value := range map[string]any{
		"instance_id": NewID(), "data_epoch": NewID(), "history_limit": 100,
		"locale": "zh-CN", "timezone": "Asia/Shanghai", "theme": "auto",
		"import_auto_classify": false,
	} {
		encoded, _ := json.Marshal(value)
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO settings(key,value_json,updated_at) VALUES(?,?,?)", key, string(encoded), Now()); err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	// A lightweight FTS capability probe catches unsupported SQLite builds.
	if _, err = db.ExecContext(ctx, "SELECT rowid FROM documents_fts WHERE documents_fts MATCH ? LIMIT 0", "\"ljmdb\""); err != nil {
		return fail(fmt.Errorf("FTS5 不可用: %w", err))
	}
	return db, nil
}

// CheckSchema inspects schema metadata, never scanning the full document tree.
// Missing FTS triggers would otherwise make saves appear successful while
// silently leaving the search index stale.
func CheckSchema(ctx context.Context, q Queryer) error {
	wanted := map[string]string{}
	for _, name := range []string{"trash_batches", "knowledge_bases", "documents", "document_revisions", "attachments", "document_attachments", "revision_attachments", "cleanup_tasks", "schema_migrations", "settings", "operation_jobs", "idempotency_records", "documents_fts"} {
		wanted[name] = "table"
	}
	wanted["documents_search_content"] = "view"
	for _, name := range []string{"documents_fts_ai", "documents_fts_ad", "documents_fts_au"} {
		wanted[name] = "trigger"
	}
	rows, err := q.QueryContext(ctx, "SELECT name,type FROM sqlite_master")
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, kind string
		if err = rows.Scan(&name, &kind); err != nil {
			rows.Close()
			return err
		}
		if expected, ok := wanted[name]; ok && kind == expected {
			delete(wanted, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(wanted) > 0 {
		return fmt.Errorf("数据库结构缺失，停止启动并保留原数据")
	}
	// Preparing these zero-row statements validates critical columns without
	// loading any user's Markdown or attachment content.
	for _, query := range []string{"SELECT " + documentColumns + " FROM documents d LIMIT 0", "SELECT " + knowledgeBaseColumns + " FROM knowledge_bases k LIMIT 0", "SELECT original_parent_id FROM trash_batches LIMIT 0", "SELECT id,document_id,revision,title,markdown,reason,created_at FROM document_revisions LIMIT 0", "SELECT id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at FROM attachments LIMIT 0"} {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("数据库字段与版本不一致: %w", err)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

type Queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func ValidateTitle(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8Valid(value) || len([]rune(value)) > 200 {
		return "", Err(400, "INVALID_ARGUMENT", "标题不能为空且不得超过 200 个字符", map[string]any{"field": "title", "max_length": 200})
	}
	return value, nil
}

func ValidateMarkdown(value string) error {
	if len(value) > 10<<20 {
		return Err(413, "PAYLOAD_TOO_LARGE", "Markdown 正文超过 10 MiB 限制", map[string]any{"max_bytes": 10 << 20})
	}
	if !utf8Valid(value) {
		return Err(400, "INVALID_ARGUMENT", "Markdown 必须是有效 UTF-8 文本", nil)
	}
	return nil
}
