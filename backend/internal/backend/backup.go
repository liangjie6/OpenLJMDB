package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BackupFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
type BackupManifest struct {
	Format             string       `json:"format"`
	FormatVersion      int          `json:"format_version"`
	ApplicationVersion string       `json:"application_version"`
	SchemaVersion      int          `json:"schema_version"`
	InstanceID         string       `json:"instance_id"`
	CreatedAt          string       `json:"created_at"`
	Database           BackupFile   `json:"database"`
	Attachments        []BackupFile `json:"attachments"`
}
type validatedBackup struct {
	ID        string         `json:"validated_backup_id"`
	ExpiresAt int64          `json:"expires_at"`
	Manifest  BackupManifest `json:"manifest"`
}
type restoreState struct {
	JobID          string `json:"job_id"`
	State          string `json:"state"`
	Phase          string `json:"phase"`
	Progress       int    `json:"progress"`
	IdempotencyKey string `json:"idempotency_key"`
	RequestHash    string `json:"request_hash"`
	ValidationID   string `json:"validated_backup_id"`
	RollbackBackup string `json:"rollback_backup"`
	WorkDir        string `json:"work_dir"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	ErrorSummary   string `json:"error_summary,omitempty"`
	DataEpoch      string `json:"data_epoch,omitempty"`
}

func (s restoreState) job() map[string]any {
	j := map[string]any{"id": s.JobID, "kind": "restore", "state": s.State, "phase": s.Phase, "progress": s.Progress, "created_at": s.CreatedAt, "updated_at": s.UpdatedAt}
	if s.ErrorSummary != "" {
		j["error_code"] = "RESTORE_FAILED"
		j["error_summary"] = s.ErrorSummary
	}
	if s.RollbackBackup != "" {
		j["result"] = map[string]any{"rollback_backup_available": true, "rollback_backup_job_id": strings.TrimSuffix(filepath.Base(s.RollbackBackup), ".zip")}
	}
	if s.State == "succeeded" {
		j["result"] = map[string]any{"data_epoch": s.DataEpoch, "reload_required": true, "rollback_backup_available": s.RollbackBackup != "", "rollback_backup_job_id": strings.TrimSuffix(filepath.Base(s.RollbackBackup), ".zip")}
	}
	return j
}
func readRestoreState(dir string) (restoreState, error) {
	var s restoreState
	err := readJSONFile(filepath.Join(dir, "runtime", "restore-state.json"), &s)
	return s, err
}
func restoreKeyPath(dir, key string) string {
	hash := sha256.Sum256([]byte(key))
	return filepath.Join(dir, "runtime", "restore-idempotency", hex.EncodeToString(hash[:])+".json")
}
func readRestoreKey(dir, key string) (restoreState, error) {
	if cur, err := readRestoreState(dir); err == nil && cur.IdempotencyKey == key {
		return cur, nil
	}
	var s restoreState
	err := readJSONFile(restoreKeyPath(dir, key), &s)
	return s, err
}
func readRestoreJob(dir, id string) (restoreState, error) {
	var s restoreState
	if !ValidID(id) {
		return s, os.ErrNotExist
	}
	if cur, err := readRestoreState(dir); err == nil && cur.JobID == id {
		return cur, nil
	}
	err := readJSONFile(filepath.Join(dir, "runtime", "restore-jobs", id+".json"), &s)
	return s, err
}
func writeRestoreState(dir string, s *restoreState) error {
	s.UpdatedAt = Now()
	if err := atomicJSON(filepath.Join(dir, "runtime", "restore-jobs", s.JobID+".json"), s); err != nil {
		return err
	}
	if s.IdempotencyKey != "" {
		if err := atomicJSON(restoreKeyPath(dir, s.IdempotencyKey), s); err != nil {
			return err
		}
	}
	return atomicJSON(filepath.Join(dir, "runtime", "restore-state.json"), s)
}

func (a *App) cacheRestoreState(s *restoreState) {
	job := operationJob{ID: s.JobID, Kind: "restore", State: s.State, Phase: s.Phase, Progress: s.Progress, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Result: s.job()["result"]}
	if s.ErrorSummary != "" {
		code, message := "RESTORE_FAILED", s.ErrorSummary
		job.ErrorCode = &code
		job.ErrorSummary = &message
	}
	a.liveJobs.Store(s.JobID, job)
}
func (a *App) persistRestoreState(s *restoreState) error {
	var err error
	if a.restoreStateWriter != nil {
		err = a.restoreStateWriter(a.DataDir, s)
	} else {
		err = writeRestoreState(a.DataDir, s)
	}
	if err == nil {
		a.cacheRestoreState(s)
	}
	return err
}

func (a *App) backupCreate(w http.ResponseWriter, r *http.Request) {
	if !a.maintenance.CompareAndSwap(false, true) {
		WriteError(w, r, Err(503, "MAINTENANCE_MODE", "维护任务正在进行", nil))
		return
	}
	id, err := a.createJob(r.Context(), "backup")
	if err != nil {
		a.maintenance.Store(false)
		WriteError(w, r, err)
		return
	}
	a.startJob(id, true, func(ctx context.Context) (string, any, error) {
		a.FileMu.Lock()
		defer a.FileMu.Unlock()
		p, m, err := a.buildBackup(ctx, id)
		return p, m, err
	})
	WriteData(w, 202, map[string]any{"job_id": id, "state": "queued"}, nil)
}

// buildBackup runs with all database writers paused and FileMu held. VACUUM
// INTO produces an independent SQLite snapshot, including committed WAL data.
func (a *App) buildBackup(ctx context.Context, id string) (string, BackupManifest, error) {
	m := BackupManifest{Format: "ljmdb-backup", FormatVersion: 1, ApplicationVersion: Version, SchemaVersion: SchemaVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339), Attachments: []BackupFile{}}
	var raw string
	if err := a.DB.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key='instance_id'").Scan(&raw); err != nil {
		return "", m, err
	}
	if err := json.Unmarshal([]byte(raw), &m.InstanceID); err != nil {
		return "", m, err
	}
	stage := filepath.Join(a.DataDir, "tmp", "backup-"+id)
	if err := os.MkdirAll(stage, 0700); err != nil {
		return "", m, err
	}
	defer os.RemoveAll(stage)
	var pages, pageSize, attachmentBytes int64
	if err := a.DB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return "", m, err
	}
	if err := a.DB.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return "", m, err
	}
	if err := a.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(size_bytes),0) FROM attachments WHERE state!='deleted'").Scan(&attachmentBytes); err != nil {
		return "", m, err
	}
	a.jobEstimate(id, map[string]any{"estimated_database_bytes": pages * pageSize, "attachment_bytes": attachmentBytes, "estimated_required_free_bytes": pages*pageSize*2 + attachmentBytes})
	if err := ensureDiskSpace(a.DataDir, pages*pageSize*2+attachmentBytes); err != nil {
		return "", m, err
	}
	snapshot := filepath.Join(stage, "knowledge.db")
	if _, err := a.DB.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return "", m, err
	}
	hash, n, err := hashFile(snapshot)
	if err != nil {
		return "", m, err
	}
	m.Database = BackupFile{"knowledge.db", n, hash}
	a.jobProgress(id, 20)
	rows, err := a.DB.QueryContext(ctx, "SELECT storage_path,size_bytes,sha256 FROM attachments WHERE state!='deleted' ORDER BY storage_path")
	if err != nil {
		return "", m, err
	}
	for rows.Next() {
		var f BackupFile
		if err = rows.Scan(&f.Path, &f.SizeBytes, &f.SHA256); err != nil {
			rows.Close()
			return "", m, err
		}
		m.Attachments = append(m.Attachments, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", m, err
	}
	var hashedBytes int64
	for _, f := range m.Attachments {
		if _, err := safeArchivePath(f.Path); err != nil {
			return "", m, err
		}
		if !strings.HasPrefix(f.Path, "uploads/") {
			return "", m, errors.New("attachment storage path is outside uploads")
		}
		full, err := a.SafeAttachmentPath(attachmentIDFromPath(f.Path), f.Path)
		if err != nil {
			return "", m, err
		}
		h, n, err := hashFileProgress(ctx, full, func(n int64) error {
			a.jobProgress(id, 20+int((hashedBytes+n)*20/max(int64(1), attachmentBytes)))
			return nil
		})
		hashedBytes += n
		if err != nil || h != f.SHA256 || n != f.SizeBytes {
			return "", m, fmt.Errorf("cannot back up missing or damaged attachment: %s", f.Path)
		}
	}
	relative := filepath.ToSlash(filepath.Join("backups", id+".zip"))
	filename := filepath.Join(a.DataDir, filepath.FromSlash(relative))
	f, err := os.OpenFile(filename+".partial", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", m, err
	}
	done := false
	defer func() {
		f.Close()
		if !done {
			os.Remove(filename + ".partial")
		}
	}()
	z := zip.NewWriter(f)
	if err = addZIPFileContext(ctx, z, m.Database.Path, snapshot); err != nil {
		z.Close()
		return "", m, err
	}
	var archivedBytes int64
	for _, at := range m.Attachments {
		if ctx.Err() != nil {
			z.Close()
			return "", m, ctx.Err()
		}
		full, pathErr := a.SafeAttachmentPath(attachmentIDFromPath(at.Path), at.Path)
		if pathErr != nil {
			z.Close()
			return "", m, pathErr
		}
		if err = addZIPFileProgress(ctx, z, at.Path, full, func(n int64) error {
			a.jobProgress(id, 40+int((archivedBytes+n)*50/max(int64(1), attachmentBytes)))
			return nil
		}); err != nil {
			z.Close()
			return "", m, err
		}
		archivedBytes += at.SizeBytes
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		z.Close()
		return "", m, err
	}
	if err = addZIPBytes(z, "manifest.json", encoded); err != nil {
		z.Close()
		return "", m, err
	}
	if err = z.Close(); err != nil {
		return "", m, err
	}
	if err = f.Sync(); err != nil {
		return "", m, err
	}
	if err = f.Close(); err != nil {
		return "", m, err
	}
	if err = os.Rename(filename+".partial", filename); err != nil {
		return "", m, err
	}
	if err = syncDir(filepath.Dir(filename)); err != nil {
		return "", m, err
	}
	done = true
	return relative, m, nil
}
func (a *App) backupList(w http.ResponseWriter, r *http.Request) {
	page, size, err := Page(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var count int
	if err = a.DB.QueryRow("SELECT count(*) FROM operation_jobs WHERE kind='backup'").Scan(&count); err != nil {
		WriteError(w, r, err)
		return
	}
	rows, err := a.DB.Query("SELECT id FROM operation_jobs WHERE kind='backup' ORDER BY created_at DESC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			WriteError(w, r, err)
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		WriteError(w, r, err)
		return
	}
	jobs := []operationJob{}
	for _, id := range ids {
		j, err := a.loadJob(id)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		jobs = append(jobs, j)
	}
	WriteData(w, 200, jobs, map[string]any{"page": page, "page_size": size, "total": count})
}
func (a *App) backupValidate(w http.ResponseWriter, r *http.Request) {
	id := NewID()
	dir := filepath.Join(a.DataDir, "tmp", "validated-backups", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		WriteError(w, r, err)
		return
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(dir)
		}
	}()
	upload := filepath.Join(dir, "upload.zip")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req struct {
			BackupID string `json:"backup_id"`
		}
		if err := Decode(r, &req); err != nil {
			WriteError(w, r, err)
			return
		}
		job, err := a.loadJob(req.BackupID)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if job.Kind != "backup" || job.State != "succeeded" || job.ResultPath == nil {
			WriteError(w, r, Err(400, "INVALID_BACKUP", "指定备份不可用", nil))
			return
		}
		if err = copyFile(filepath.Join(a.DataDir, *job.ResultPath), upload); err != nil {
			WriteError(w, r, err)
			return
		}
	} else {
		if _, err := receiveUpload(r, upload, 0); err != nil {
			WriteError(w, r, err)
			return
		}
	}
	manifest, err := validateBackupArchiveContext(r.Context(), upload, filepath.Join(dir, "files"))
	if err != nil {
		WriteError(w, r, transferValidationError(err, true))
		return
	}
	validated := validatedBackup{ID: id, ExpiresAt: Now() + int64(24*time.Hour/time.Millisecond), Manifest: manifest}
	if err = atomicJSON(filepath.Join(dir, "validated.json"), validated); err != nil {
		WriteError(w, r, err)
		return
	}
	done = true
	WriteData(w, 200, map[string]any{"validated_backup_id": id, "expires_at": validated.ExpiresAt, "manifest": manifest, "confirmation_token": a.Token("restore", validated), "will_replace_all_data": true}, nil)
}
func validateBackupArchive(filename, dest string) (BackupManifest, error) {
	return validateBackupArchiveContext(context.Background(), filename, dest)
}
func validateBackupArchiveContext(ctx context.Context, filename, dest string) (BackupManifest, error) {
	var m BackupManifest
	z, err := zip.OpenReader(filename)
	if err != nil {
		return m, err
	}
	var manifest *zip.File
	for _, f := range z.File {
		if f.Name == "manifest.json" {
			if manifest != nil {
				z.Close()
				return m, errors.New("duplicate backup manifest")
			}
			manifest = f
		}
	}
	if manifest == nil || manifest.UncompressedSize64 > 16<<20 {
		z.Close()
		return m, errors.New("backup manifest missing or too large")
	}
	r, err := manifest.Open()
	if err != nil {
		z.Close()
		return m, err
	}
	decoder := json.NewDecoder(io.LimitReader(r, 16<<20))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&m)
	if err == nil {
		var more any
		if decoder.Decode(&more) != io.EOF {
			err = errors.New("invalid backup manifest JSON")
		}
	}
	r.Close()
	z.Close()
	if err != nil {
		return m, err
	}
	if m.Format != "ljmdb-backup" || m.FormatVersion != 1 {
		return m, errors.New("unsupported full backup format")
	}
	if m.SchemaVersion < 1 || m.SchemaVersion > SchemaVersion {
		return m, Err(409, "BACKUP_INCOMPATIBLE", "备份数据库版本不受当前程序支持", map[string]any{"backup_schema_version": m.SchemaVersion, "supported_schema_version": SchemaVersion})
	}
	if !ValidID(m.InstanceID) || m.Database.Path != "knowledge.db" {
		return m, errors.New("invalid backup identity or database path")
	}
	expected := map[string]BackupFile{m.Database.Path: m.Database}
	total := int64(16 << 20)
	for _, f := range append([]BackupFile{m.Database}, m.Attachments...) {
		if _, err := safeArchivePath(f.Path); err != nil {
			return m, err
		}
		if f.SizeBytes < 0 || len(f.SHA256) != 64 {
			return m, errors.New("invalid backup size or hash")
		}
		if _, err := hex.DecodeString(f.SHA256); err != nil {
			return m, errors.New("invalid backup hash")
		}
		if f.SizeBytes > 1<<60-total {
			return m, errors.New("backup manifest size overflows")
		}
		total += f.SizeBytes
	}
	for _, f := range m.Attachments {
		if !strings.HasPrefix(f.Path, "uploads/") {
			return m, errors.New("backup attachment outside uploads")
		}
		if _, exists := expected[f.Path]; exists {
			return m, errors.New("duplicate backup file")
		}
		expected[f.Path] = f
	}
	check, err := zip.OpenReader(filename)
	if err != nil {
		return m, err
	}
	for _, f := range check.File {
		if f.FileInfo().IsDir() {
			check.Close()
			return m, errors.New("unexpected backup directory entry")
		}
		if f.Name == "manifest.json" {
			continue
		}
		want, ok := expected[f.Name]
		if !ok || f.UncompressedSize64 != uint64(want.SizeBytes) {
			check.Close()
			return m, fmt.Errorf("backup header does not match manifest: %s", f.Name)
		}
	}
	check.Close()
	entries, err := extractArchiveContext(ctx, filename, dest, archiveLimits{total, len(expected) + 1, 64, 0})
	if err != nil {
		return m, err
	}
	if len(entries) != len(expected)+1 {
		return m, errors.New("backup files do not match manifest")
	}
	for name, e := range entries {
		if name == "manifest.json" {
			continue
		}
		wanted, ok := expected[name]
		if !ok {
			return m, fmt.Errorf("unexpected backup file: %s", name)
		}
		if wanted.SHA256 != e.SHA256 || wanted.SizeBytes != e.Size {
			return m, fmt.Errorf("backup checksum or size mismatch: %s", name)
		}
	}
	if err = validateSnapshotContext(ctx, filepath.Join(dest, "knowledge.db"), m); err != nil {
		return m, err
	}
	return m, nil
}
func validateSnapshot(filename string, m BackupManifest) error {
	return validateSnapshotContext(context.Background(), filename, m)
}
func validateSnapshotContext(ctx context.Context, filename string, m BackupManifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filename)}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != m.SchemaVersion {
		return errors.New("database schema differs from manifest")
	}
	var integrity string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("database integrity check failed: %s", integrity)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if bad {
		return errors.New("database foreign-key check failed")
	}
	if err = CheckSchema(ctx, db); err != nil {
		return err
	}
	var raw string
	if err = db.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key='instance_id'").Scan(&raw); err != nil {
		return err
	}
	var instance string
	if err = json.Unmarshal([]byte(raw), &instance); err != nil {
		return err
	}
	if instance != m.InstanceID {
		return errors.New("database instance ID differs from manifest")
	}
	rows, err = db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	versions := 0
	for rows.Next() {
		var n int
		var checksum string
		if err = rows.Scan(&n, &checksum); err != nil {
			rows.Close()
			return err
		}
		b, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/%03d_initial.sql", n))
		if err != nil {
			rows.Close()
			return err
		}
		h := sha256.Sum256(b)
		if checksum != hex.EncodeToString(h[:]) {
			rows.Close()
			return errors.New("backup schema migration checksum mismatch")
		}
		versions++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if versions != m.SchemaVersion {
		return errors.New("backup schema migration history incomplete")
	}
	wanted := map[string]BackupFile{}
	for _, f := range m.Attachments {
		wanted[f.Path] = f
	}
	rows, err = db.QueryContext(ctx, "SELECT id,storage_path,size_bytes,sha256 FROM attachments WHERE state!='deleted'")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id, p, h string
		var n int64
		if err = rows.Scan(&id, &p, &n, &h); err != nil {
			rows.Close()
			return err
		}
		if !ValidID(id) || p != attachmentRelativePath(id) {
			rows.Close()
			return errors.New("backup attachment storage path is invalid")
		}
		f, ok := wanted[p]
		if !ok || f.SizeBytes != n || f.SHA256 != h {
			rows.Close()
			return fmt.Errorf("database resource differs from manifest: %s", p)
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != len(wanted) {
		return errors.New("manifest lists resources absent from database")
	}
	var unavailableReferences int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT attachment_id FROM document_attachments UNION SELECT attachment_id FROM revision_attachments) refs JOIN attachments a ON a.id=refs.attachment_id WHERE a.state!='ready'").Scan(&unavailableReferences); err != nil {
		return err
	}
	if unavailableReferences > 0 {
		return errors.New("backup contains references to unavailable resources")
	}
	rows, err = db.QueryContext(ctx, "SELECT id FROM knowledge_bases")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
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
		if err = ValidateTree(ctx, db, id); err != nil {
			return err
		}
	}
	// Check deleted trees as well: snapshots must not restore corrupted hidden data.
	rows, err = db.QueryContext(ctx, "SELECT id,knowledge_base_id,parent_id FROM documents")
	if err != nil {
		return err
	}
	type node struct {
		kb     string
		parent *string
	}
	nodes := map[string]node{}
	for rows.Next() {
		var id string
		var n node
		if err = rows.Scan(&id, &n.kb, &n.parent); err != nil {
			rows.Close()
			return err
		}
		nodes[id] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id := range nodes {
		seen := map[string]bool{}
		cur := id
		for depth := 1; ; depth++ {
			if depth > 32 || seen[cur] {
				return errors.New("backup contains invalid or overdeep document tree")
			}
			seen[cur] = true
			n := nodes[cur]
			if n.parent == nil {
				break
			}
			parent, ok := nodes[*n.parent]
			if !ok || parent.kb != n.kb {
				return errors.New("backup document parent is missing or belongs to another library")
			}
			cur = *n.parent
		}
	}
	return nil
}

type restoreRequest struct {
	ValidatedBackupID string `json:"validated_backup_id"`
	ConfirmationToken string `json:"confirmation_token"`
}

func (a *App) RestoreReplay(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "POST" || r.URL.Path != "/api/v1/backups/restore" {
		return false
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return false
	}
	s, err := readRestoreKey(a.DataDir, key)
	if err != nil || s.IdempotencyKey != key {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > 1<<20 {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "恢复请求过大或无法读取", nil))
		return true
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != s.RequestHash {
		WriteError(w, r, Err(409, "IDEMPOTENCY_KEY_REUSED", "幂等键已用于不同恢复请求", nil))
		return true
	}
	WriteData(w, 202, map[string]any{"job_id": s.JobID, "state": s.State}, nil)
	return true
}
func (a *App) backupRestore(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "恢复请求过大", nil))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req restoreRequest
	if err = Decode(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	if !ValidID(req.ValidatedBackupID) {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "无效 validated_backup_id", nil))
		return
	}
	var validated validatedBackup
	if err = readJSONFile(filepath.Join(a.DataDir, "tmp", "validated-backups", req.ValidatedBackupID, "validated.json"), &validated); err != nil || validated.ExpiresAt < Now() {
		WriteError(w, r, Err(410, "BACKUP_VALIDATION_EXPIRED", "备份验证已过期，请重新验证", nil))
		return
	}
	if !a.VerifyToken(req.ConfirmationToken, "restore", validated) {
		WriteError(w, r, Err(400, "CONFIRMATION_REQUIRED", "恢复确认令牌无效或已过期", nil))
		return
	}
	if !a.maintenance.CompareAndSwap(false, true) {
		WriteError(w, r, Err(503, "MAINTENANCE_MODE", "维护任务正在进行", nil))
		return
	}
	hash := sha256.Sum256(body)
	s := restoreState{JobID: NewID(), State: "queued", Phase: "accepted", Progress: 0, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestHash: hex.EncodeToString(hash[:]), ValidationID: req.ValidatedBackupID, WorkDir: filepath.ToSlash(filepath.Join("runtime", "restore-"+NewID())), CreatedAt: Now()}
	if err = a.persistRestoreState(&s); err != nil {
		a.maintenance.Store(false)
		WriteError(w, r, err)
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			if a.DB != nil && s.Phase != "rollback_failed" {
				a.maintenance.Store(false)
			}
		}()
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.FileMu.Lock()
		defer a.FileMu.Unlock()
		a.performRestore(&s, validated)
	}()
	WriteData(w, 202, map[string]any{"job_id": s.JobID, "state": "queued"}, nil)
}
func (a *App) performRestore(s *restoreState, v validatedBackup) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	fail := func(cause error) {
		s.State = "failed"
		_, s.ErrorSummary = jobFailure(cause)
		s.Phase = "failed"
		a.persistRestoreState(s)
	}
	if ctx.Err() != nil {
		fail(ctx.Err())
		return
	}
	s.State = "running"
	s.Phase = "preparing"
	s.Progress = 5
	if err := a.persistRestoreState(s); err != nil {
		fail(err)
		return
	}
	source := filepath.Join(a.DataDir, "tmp", "validated-backups", v.ID, "files")
	if err := validatePreparedBackupContext(ctx, source, v.Manifest); err != nil {
		fail(err)
		return
	}
	var incomingBytes int64
	incomingBytes = v.Manifest.Database.SizeBytes
	for _, at := range v.Manifest.Attachments {
		incomingBytes += at.SizeBytes
	}
	if err := ensureDiskSpace(a.DataDir, incomingBytes); err != nil {
		fail(err)
		return
	}
	work := filepath.Join(a.DataDir, filepath.FromSlash(s.WorkDir))
	incoming := filepath.Join(work, "incoming")
	original := filepath.Join(work, "original")
	if err := os.MkdirAll(filepath.Join(incoming, "uploads"), 0700); err != nil {
		fail(err)
		return
	}
	if err := os.MkdirAll(original, 0700); err != nil {
		fail(err)
		return
	}
	var copiedBytes int64
	for _, f := range append([]BackupFile{v.Manifest.Database}, v.Manifest.Attachments...) {
		if err := copyFileProgress(ctx, filepath.Join(source, filepath.FromSlash(f.Path)), filepath.Join(incoming, filepath.FromSlash(f.Path)), func(n int64) error {
			progress := 5 + int((copiedBytes+n)*20/max(int64(1), incomingBytes))
			if progress >= s.Progress+5 {
				s.Progress = progress
				return a.persistRestoreState(s)
			}
			return nil
		}); err != nil {
			fail(err)
			return
		}
		copiedBytes += f.SizeBytes
	}
	s.Phase = "rollback_backup"
	s.Progress = 25
	if err := a.persistRestoreState(s); err != nil {
		fail(err)
		return
	}
	backupID := NewID()
	relative, _, err := a.buildBackup(ctx, backupID)
	if err != nil {
		fail(err)
		return
	}
	s.RollbackBackup = relative
	if _, err = a.DB.Exec("INSERT INTO operation_jobs(id,kind,state,progress,result_path,created_at,updated_at) VALUES(?,'backup','succeeded',100,?,?,?)", backupID, s.RollbackBackup, Now(), Now()); err != nil {
		fail(err)
		return
	}
	s.Phase = "prepared"
	s.Progress = 35
	if err = a.persistRestoreState(s); err != nil {
		fail(err)
		return
	}
	if ctx.Err() != nil {
		fail(ctx.Err())
		return
	}
	if _, err = a.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		fail(err)
		return
	}
	if err = a.DB.Close(); err != nil {
		fail(err)
		return
	}
	a.DB = nil
	s.Phase = "switching"
	s.Progress = 50
	if err = a.persistRestoreState(s); err == nil {
		err = swapRestoreData(a.DataDir, work)
	}
	if err == nil {
		a.DB, err = OpenDB(a.DataDir)
	}
	if err == nil {
		epoch := NewID()
		raw, _ := json.Marshal(epoch)
		_, err = a.DB.Exec("UPDATE settings SET value_json=?,updated_at=? WHERE key='data_epoch'", string(raw), Now())
		s.DataEpoch = epoch
	}
	if err == nil {
		err = a.RecoverTransferJobs()
	}
	if err == nil {
		_, err = a.DB.Exec("INSERT OR IGNORE INTO operation_jobs(id,kind,state,progress,result_path,created_at,updated_at) VALUES(?,'backup','succeeded',100,?,?,?)", backupID, s.RollbackBackup, Now(), Now())
	}
	if err == nil {
		err = a.RefreshIdentity()
	}
	if err == nil {
		err = validateStagedBackupContext(ctx, a.DataDir, BackupManifest{SchemaVersion: SchemaVersion, InstanceID: v.Manifest.InstanceID, Attachments: v.Manifest.Attachments, Database: v.Manifest.Database})
	}
	if err != nil {
		a.rollbackRestore(s, work, err)
		return
	}
	a.completeRestore(s, work)
}

// The caller holds Mu and FileMu. A failure to persist completion is a restore
// failure: roll back immediately before allowing any subsequent application writes.
func (a *App) completeRestore(s *restoreState, work string) error {
	s.State = "succeeded"
	s.Phase = "complete"
	s.Progress = 100
	if err := a.persistRestoreState(s); err != nil {
		a.rollbackRestore(s, work, err)
		return err
	}
	os.RemoveAll(work)
	return nil
}
func (a *App) rollbackRestore(s *restoreState, work string, cause error) {

	if a.DB != nil {
		a.DB.Close()
		a.DB = nil
	}
	rollbackErr := rollbackRestoreData(a.DataDir, work)
	if rollbackErr == nil {
		a.DB, rollbackErr = OpenDB(a.DataDir)
	}
	if rollbackErr == nil {
		rollbackErr = a.RefreshIdentity()
	}
	s.State = "failed"
	s.Phase = "rolled_back"
	_, s.ErrorSummary = jobFailure(cause)
	if rollbackErr != nil {
		s.Phase = "rollback_failed"
		s.ErrorSummary = "恢复失败且无法自动回退，请保留数据目录并依据恢复日志处理"
	}
	if stateErr := a.persistRestoreState(s); stateErr != nil {
		s.Phase = "rollback_failed"
		s.ErrorSummary = "恢复已回退，但无法保存恢复日志；服务保持维护状态，请检查磁盘或目录权限"
		if a.Logger != nil {
			a.Logger.Printf("restore rollback state persistence failed job=%s error=%s", s.JobID, safeErrorClass(stateErr))
		}
	}
	if s.Phase == "rollback_failed" {
		a.cacheRestoreState(s)
		a.maintenance.Store(true)
	}
}

func attachmentIDFromPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) != 4 || parts[0] != "uploads" || parts[3] != "blob" {
		return ""
	}
	return parts[2]
}
func validatePreparedBackup(dir string, m BackupManifest) error {
	return validatePreparedBackupContext(context.Background(), dir, m)
}
func validatePreparedBackupContext(ctx context.Context, dir string, m BackupManifest) error {
	h, n, err := hashFileProgress(ctx, filepath.Join(dir, "knowledge.db"), nil)
	if err != nil {
		return err
	}
	if h != m.Database.SHA256 || n != m.Database.SizeBytes {
		return errors.New("staged backup database checksum mismatch")
	}
	return validateStagedBackupContext(ctx, dir, m)
}
func validateStagedBackup(dir string, m BackupManifest) error {
	return validateStagedBackupContext(context.Background(), dir, m)
}
func validateStagedBackupContext(ctx context.Context, dir string, m BackupManifest) error {
	for _, f := range m.Attachments {
		checker := App{DataDir: dir}
		full, err := checker.SafeAttachmentPath(attachmentIDFromPath(f.Path), f.Path)
		if err != nil {
			return err
		}
		h, n, err := hashFileProgress(ctx, full, nil)
		if err != nil {
			return err
		}
		if err != nil || h != f.SHA256 || n != f.SizeBytes {
			return fmt.Errorf("staged backup attachment changed: %s", f.Path)
		}
	}
	return validateSnapshotContext(ctx, filepath.Join(dir, "knowledge.db"), m)
}
func swapRestoreData(dir, work string) error {
	liveDB := filepath.Join(dir, "knowledge.db")
	old := filepath.Join(work, "original")
	incoming := filepath.Join(work, "incoming")
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(liveDB + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, name := range []string{"knowledge.db", "uploads"} {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(old, name)); err != nil {
			return err
		}
		if err := syncDir(dir); err != nil {
			return err
		}
		if err := syncDir(old); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(incoming, name), filepath.Join(dir, name)); err != nil {
			return err
		}
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}
func rollbackRestoreData(dir, work string) error {
	old := filepath.Join(work, "original")
	for _, name := range []string{"knowledge.db", "uploads"} {
		saved := filepath.Join(old, name)
		if _, err := os.Stat(saved); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		live := filepath.Join(dir, name)
		if name == "knowledge.db" {
			for _, suffix := range []string{"-wal", "-shm"} {
				if err := os.Remove(live + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		if err := os.RemoveAll(live); err != nil {
			return err
		}
		if err := os.Rename(saved, live); err != nil {
			return err
		}
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// RecoverRestore is called while holding runtime.lock, before opening SQLite.
// An interrupted multi-file switch always restores the prior complete dataset.
func RecoverRestore(dir string) error {
	s, err := readRestoreState(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read restore recovery log: %w", err)
	}
	if s.Phase == "complete" || s.Phase == "rolled_back" || s.Phase == "failed" {
		return nil
	}
	if _, err = safeArchivePath(s.WorkDir); err != nil || !strings.HasPrefix(s.WorkDir, "runtime/restore-") {
		return errors.New("invalid restore recovery work directory")
	}
	work := filepath.Join(dir, filepath.FromSlash(s.WorkDir))
	if err = rollbackRestoreData(dir, work); err != nil {
		return fmt.Errorf("interrupted restore rollback failed: %w", err)
	}
	s.State = "failed"
	s.Phase = "rolled_back"
	s.ErrorSummary = "恢复因进程中断而回退，原数据已保留"
	return writeRestoreState(dir, &s)
}
