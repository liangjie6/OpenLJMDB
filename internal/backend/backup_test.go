package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func backupArchive(t *testing.T, a *App) (string, []byte) {
	t.Helper()
	id := coreStatus(t, securityRequest(a, "POST", "/api/v1/backups", nil, map[string]string{"Idempotency-Key": NewID()}), 202)["job_id"].(string)
	transferWait(t, a, id)
	w := securityRequest(a, "GET", "/api/v1/jobs/"+id+"/download", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return id, append([]byte(nil), w.Body.Bytes()...)
}
func replaceBackupZIP(t *testing.T, data []byte, mutate func(string, []byte) []byte) []byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, f := range z.File {
		in, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(in)
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
		b = mutate(f.Name, b)
		if b == nil {
			continue
		}
		w, err := writer.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestBackupRestoreFreshDirectoryPreservesFullDatasetAndChangesEpoch(t *testing.T) {
	source := coreApp(t)
	kb := coreCreateKB(t, source)
	at := transferAttachment(t, source)
	parent := coreCreateDoc(t, source, kb, nil, "父文档", "初始正文\n[资源](/api/v1/attachments/"+at.ID+"/content)")
	child := coreCreateDoc(t, source, kb, &parent.ID, "回收站子文档", "被删除的正文")
	coreStatus(t, securityRequest(source, "PUT", "/api/v1/documents/"+parent.ID, map[string]any{"title": "新标题", "markdown": "现在正文", "expected_revision": 1, "snapshot": true}, nil), 200)
	batch := coreDeleteDoc(t, source, child)
	coreStatus(t, securityRequest(source, "PUT", "/api/v1/settings", map[string]any{"history_limit": 150}, nil), 200)
	sourceIdentity := source.identity.Load().(identity)
	_, archive := backupArchive(t, source)
	target := coreApp(t)
	targetOriginalIdentity := target.identity.Load().(identity)
	validated := coreStatus(t, transferUpload(target, "/api/v1/backups/validate", "full.zip", archive), 200)
	request := map[string]any{"validated_backup_id": validated["validated_backup_id"], "confirmation_token": validated["confirmation_token"]}
	headers := map[string]string{"Idempotency-Key": "restore-full-once", "X-Data-Epoch": targetOriginalIdentity.DataEpoch}
	accepted := coreStatus(t, securityRequest(target, "POST", "/api/v1/backups/restore", request, headers), 202)
	id := accepted["job_id"].(string)
	job := transferWait(t, target, id)
	target.Mu.RLock()
	newIdentity := target.identity.Load().(identity)
	d, err := LoadDocument(context.Background(), target.DB, parent.ID, true)
	if err != nil {
		target.Mu.RUnlock()
		t.Fatal(err)
	}
	deleted, err := LoadDocument(context.Background(), target.DB, child.ID, false)
	if err != nil {
		target.Mu.RUnlock()
		t.Fatal(err)
	}
	var history, refs, trash int
	target.DB.QueryRow("SELECT count(*) FROM document_revisions WHERE document_id=?", parent.ID).Scan(&history)
	target.DB.QueryRow("SELECT count(*) FROM revision_attachments WHERE attachment_id=?", at.ID).Scan(&refs)
	target.DB.QueryRow("SELECT count(*) FROM trash_batches WHERE id=?", batch).Scan(&trash)
	var historyLimit string
	target.DB.QueryRow("SELECT value_json FROM settings WHERE key='history_limit'").Scan(&historyLimit)
	target.Mu.RUnlock()
	if newIdentity.InstanceID != sourceIdentity.InstanceID || newIdentity.DataEpoch == sourceIdentity.DataEpoch || newIdentity.DataEpoch == targetOriginalIdentity.DataEpoch {
		t.Fatal("instance/epoch restore semantics violated", newIdentity, sourceIdentity, targetOriginalIdentity)
	}
	if d.Title != "新标题" || d.Markdown != "现在正文" || d.Revision != 2 || deleted.DeletedAt == nil || deleted.ParentID == nil || *deleted.ParentID != parent.ID || history != 1 || refs != 1 || trash != 1 || historyLimit != "150" {
		t.Fatal("full dataset not preserved", d, deleted, history, refs, trash, historyLimit)
	}
	content := securityRequest(target, "GET", "/api/v1/attachments/"+at.ID+"/content", nil, nil)
	if content.Code != 200 || content.Body.String() != "test local attachment\n" {
		t.Fatal("history-only retained attachment lost", content.Code, content.Body.String())
	}
	stale := securityRequest(target, "PUT", "/api/v1/documents/"+parent.ID, map[string]any{"title": "旧会话", "markdown": "错误覆盖", "expected_revision": 2}, map[string]string{"X-Data-Epoch": sourceIdentity.DataEpoch})
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "DATA_EPOCH_CHANGED") {
		t.Fatal("old epoch accepted", stale.Code, stale.Body.String())
	}
	retry := securityRequest(target, "POST", "/api/v1/backups/restore", request, headers)
	if retry.Code != 202 || responseData(t, retry)["job_id"] != id {
		t.Fatal("restore retry lost external identity", retry.Code, retry.Body.String())
	}
	rollbackID := job["result"].(map[string]any)["rollback_backup_job_id"].(string)
	if w := securityRequest(target, "GET", "/api/v1/jobs/"+rollbackID+"/download", nil, nil); w.Code != 200 {
		t.Fatal("pre-restore rollback backup not downloadable", w.Code, w.Body.String())
	}
	dir := target.DataDir
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	poll := securityRequest(reopened, "GET", "/api/v1/jobs/"+id, nil, nil)
	if poll.Code != 200 || responseData(t, poll)["state"] != "succeeded" {
		t.Fatal("external restore job did not survive restart", poll.Code, poll.Body.String())
	}
	retry = securityRequest(reopened, "POST", "/api/v1/backups/restore", request, headers)
	if retry.Code != 202 || responseData(t, retry)["job_id"] != id {
		t.Fatal("restart restore retry lost identity", retry.Code, retry.Body.String())
	}
}
func TestBackupValidationRejectsDamageMissingUnexpectedAndNewerSchema(t *testing.T) {
	source := coreApp(t)
	kb := coreCreateKB(t, source)
	at := transferAttachment(t, source)
	coreCreateDoc(t, source, kb, nil, "正文", "[资源](/api/v1/attachments/"+at.ID+"/content)")
	_, original := backupArchive(t, source)
	target := coreApp(t)
	targetIdentity := target.identity.Load().(identity)
	cases := []struct {
		name   string
		data   []byte
		status int
	}{{"database hash", replaceBackupZIP(t, original, func(n string, b []byte) []byte {
		if n == "knowledge.db" {
			b[len(b)-1] ^= 1
		}
		return b
	}), 400}, {"missing resource", replaceBackupZIP(t, original, func(n string, b []byte) []byte {
		if strings.HasPrefix(n, "uploads/") {
			return nil
		}
		return b
	}), 400}, {"newer schema", replaceBackupZIP(t, original, func(n string, b []byte) []byte {
		if n == "manifest.json" {
			var m BackupManifest
			json.Unmarshal(b, &m)
			m.SchemaVersion = SchemaVersion + 1
			b, _ = json.Marshal(m)
		}
		return b
	}), 409}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := transferUpload(target, "/api/v1/backups/validate", "damaged.zip", tc.data)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), target.DataDir) {
				t.Fatal("server path disclosed")
			}
			if target.identity.Load().(identity) != targetIdentity {
				t.Fatal("validation changed live dataset")
			}
		})
	}
	malformed := transferZIP(t, map[string]string{"manifest.json": "{}", "../outside": "bad"})
	if w := transferUpload(target, "/api/v1/backups/validate", "evil.zip", malformed); w.Code < 400 {
		t.Fatal("malformed backup accepted")
	}
}
func TestBackupLargeCompressibleAttachmentHasIndependentLimits(t *testing.T) {
	a := coreApp(t)
	id := NewID()
	storage := attachmentRelativePath(id)
	filename := filepath.Join(a.DataDir, storage)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(51 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	hash, n, err := hashFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	at := Attachment{ID: id, OriginalName: "large.bin", StoragePath: storage, MediaType: "application/octet-stream", SizeBytes: n, SHA256: hash, State: "ready", CreatedAt: Now(), UpdatedAt: Now()}
	tx, _ := a.DB.Begin()
	if err = InsertAttachment(context.Background(), tx, at); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, data := backupArchive(t, a)
	filename = filepath.Join(t.TempDir(), "backup.zip")
	os.WriteFile(filename, data, 0600)
	m, err := validateBackupArchive(filename, t.TempDir())
	if err != nil {
		t.Fatal("normal backup wrongly used content import limits", err)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].SizeBytes != 51<<20 {
		t.Fatal("large attachment lost")
	}
}

func TestRecoverRestoreRollsBackEveryPartialFileSwitch(t *testing.T) {
	for moves := 0; moves <= 4; moves++ {
		t.Run(string(rune('0'+moves)), func(t *testing.T) {
			a := coreApp(t)
			kb := coreCreateKB(t, a)
			d := coreCreateDoc(t, a, kb, nil, "原文档", "原数据")
			dir := a.DataDir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			workRel := "runtime/restore-" + NewID()
			work := filepath.Join(dir, workRel)
			old := filepath.Join(work, "original")
			incoming := filepath.Join(work, "incoming")
			os.MkdirAll(old, 0700)
			os.MkdirAll(filepath.Join(incoming, "uploads"), 0700)
			if err := copyFile(filepath.Join(dir, "knowledge.db"), filepath.Join(incoming, "knowledge.db")); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(incoming, "uploads", "new-marker"), []byte("new"), 0600)
			os.WriteFile(filepath.Join(dir, "uploads", "original-marker"), []byte("old"), 0600)
			s := restoreState{JobID: NewID(), State: "running", Phase: "switching", WorkDir: workRel, CreatedAt: Now()}
			if err := writeRestoreState(dir, &s); err != nil {
				t.Fatal(err)
			}
			operations := [][2]string{{filepath.Join(dir, "knowledge.db"), filepath.Join(old, "knowledge.db")}, {filepath.Join(incoming, "knowledge.db"), filepath.Join(dir, "knowledge.db")}, {filepath.Join(dir, "uploads"), filepath.Join(old, "uploads")}, {filepath.Join(incoming, "uploads"), filepath.Join(dir, "uploads")}}
			for i := 0; i < moves; i++ {
				if err := os.Rename(operations[i][0], operations[i][1]); err != nil {
					t.Fatal(err)
				}
			}
			if err := RecoverRestore(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "uploads", "original-marker")); err != nil {
				t.Fatal("original files lost", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "uploads", "new-marker")); !os.IsNotExist(err) {
				t.Fatal("partial incoming uploads survived")
			}
			reopened, err := New(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			doc, err := LoadDocument(context.Background(), reopened.DB, d.ID, true)
			if err != nil || doc.Markdown != "原数据" {
				t.Fatal("old database not restored", doc, err)
			}
			status, err := readRestoreState(dir)
			if err != nil || status.Phase != "rolled_back" {
				t.Fatal("recovery result not persisted", status, err)
			}
		})
	}
}

func TestBackupValidatorsHonorCancellation(t *testing.T) {
	a := coreApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateSnapshotContext(ctx, filepath.Join(a.DataDir, "knowledge.db"), BackupManifest{SchemaVersion: SchemaVersion}); !errors.Is(err, context.Canceled) {
		t.Fatal("snapshot validation ignored cancellation", err)
	}
	if err := validatePreparedBackupContext(ctx, a.DataDir, BackupManifest{}); !errors.Is(err, context.Canceled) {
		t.Fatal("staged checksum validation ignored cancellation", err)
	}
}

func TestRestoreCompletionLogFailureRollsBackBeforeWritesResume(t *testing.T) {
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "文档", "原数据")
	a.maintenance.Store(true)
	a.Mu.Lock()
	a.FileMu.Lock()
	defer a.Mu.Unlock()
	defer a.FileMu.Unlock()
	workRel := "runtime/restore-" + NewID()
	work := filepath.Join(a.DataDir, workRel)
	original := filepath.Join(work, "original")
	os.MkdirAll(filepath.Join(original, "uploads"), 0700)
	if _, err := a.DB.Exec("VACUUM INTO ?", filepath.Join(original, "knowledge.db")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(original, "uploads", "old-marker"), []byte("old"), 0600)
	os.WriteFile(filepath.Join(a.DataDir, "uploads", "new-marker"), []byte("new"), 0600)
	if _, err := a.DB.Exec("UPDATE documents SET markdown='切换后的新数据' WHERE id=?", d.ID); err != nil {
		t.Fatal(err)
	}
	s := restoreState{JobID: NewID(), State: "running", Phase: "switching", WorkDir: workRel, CreatedAt: Now()}
	if err := writeRestoreState(a.DataDir, &s); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(a.DataDir, "runtime", "restore-jobs", s.JobID+".json")
	os.Remove(blocked)
	os.Mkdir(blocked, 0700)
	if err := a.completeRestore(&s, work); err == nil {
		t.Fatal("completion state unexpectedly persisted through a directory")
	}
	if !a.maintenance.Load() || s.State != "failed" || s.Phase != "rollback_failed" {
		t.Fatal("unrecorded restore made data writable", s, a.maintenance.Load())
	}
	restored, err := LoadDocument(context.Background(), a.DB, d.ID, true)
	if err != nil || restored.Markdown != "原数据" {
		t.Fatal("final state persistence failure did not immediately roll back", restored, err)
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "uploads", "old-marker")); err != nil {
		t.Fatal("original upload tree not restored", err)
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "uploads", "new-marker")); !os.IsNotExist(err) {
		t.Fatal("incoming files remained live")
	}
	w := securityRequest(a, "GET", "/api/v1/jobs/"+s.JobID, nil, nil)
	if w.Code != 200 || responseData(t, w)["state"] != "failed" {
		t.Fatal("failed restore reported stale running checkpoint", w.Code, w.Body.String())
	}
	current, err := readRestoreState(a.DataDir)
	if err != nil || current.Phase != "switching" {
		t.Fatal("original crash log was not retained", current, err)
	}
	a.DB.Close()
	a.DB = nil
	os.RemoveAll(blocked)
	if err := RecoverRestore(a.DataDir); err != nil {
		t.Fatal(err)
	}
	a.DB, err = OpenDB(a.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshIdentity(); err != nil {
		t.Fatal(err)
	}
	current, err = readRestoreState(a.DataDir)
	if err != nil || current.Phase != "rolled_back" {
		t.Fatal("restart did not record safe rollback", current, err)
	}
	a.maintenance.Store(false)
}
func TestJobResultNeverSucceedsWithoutDurableTerminalRecord(t *testing.T) {
	a := coreApp(t)
	id, err := a.createJob(context.Background(), "export")
	if err != nil {
		t.Fatal(err)
	}
	a.startJob(id, false, func(ctx context.Context) (string, any, error) {
		if err := a.DB.Close(); err != nil {
			return "", nil, err
		}
		return "", map[string]any{"would_have_succeeded": true}, nil
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cached, ok := a.liveJobs.Load(id); ok {
			job := cached.(operationJob)
			if job.State == "succeeded" {
				t.Fatal("job claimed success with a closed database")
			}
			if job.State == "failed" {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("failed terminal persistence was not reported")
}

func TestHTTPRestoreCompletionLogFailureReturnsPriorWritableDataset(t *testing.T) {
	source := coreApp(t)
	sourceKB := coreCreateKB(t, source)
	sourceAttachment := transferAttachment(t, source)
	coreCreateDoc(t, source, sourceKB, nil, "备份文档", "备份里的数据\n[资源](/api/v1/attachments/"+sourceAttachment.ID+"/content)")
	_, archive := backupArchive(t, source)
	target := coreApp(t)
	targetKB := coreCreateKB(t, target)
	targetAttachment := transferAttachment(t, target)
	original := coreCreateDoc(t, target, targetKB, nil, "已有文档", "原数据\n[资源](/api/v1/attachments/"+targetAttachment.ID+"/content)")
	oldIdentity := target.identity.Load().(identity)
	validated := coreStatus(t, transferUpload(target, "/api/v1/backups/validate", "full.zip", archive), 200)
	var failures int
	target.restoreStateWriter = func(dir string, s *restoreState) error {
		if s.Phase == "complete" {
			failures++
			return errors.New("injected completion state write failure")
		}
		return writeRestoreState(dir, s)
	}
	request := map[string]any{"validated_backup_id": validated["validated_backup_id"], "confirmation_token": validated["confirmation_token"]}
	id := coreStatus(t, securityRequest(target, "POST", "/api/v1/backups/restore", request, map[string]string{"Idempotency-Key": "fail-final-state-once"}), 202)["job_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	var terminal map[string]any
	for time.Now().Before(deadline) {
		w := securityRequest(target, "GET", "/api/v1/jobs/"+id, nil, nil)
		job := coreStatus(t, w, 200)
		if job["state"] == "succeeded" {
			t.Fatal("completion write failure claimed success")
		}
		if job["state"] == "failed" {
			terminal = job
			break
		}
		time.Sleep(time.Millisecond)
	}
	if terminal == nil {
		t.Fatal("restore failure was not queryable")
	}
	target.Mu.RLock()
	restored, err := LoadDocument(context.Background(), target.DB, original.ID, true)
	identityAfter := target.identity.Load().(identity)
	target.Mu.RUnlock()
	if err != nil || restored.Markdown != original.Markdown || identityAfter != oldIdentity || failures != 1 {
		t.Fatal("completion failure changed existing data/epoch", restored, identityAfter, oldIdentity, failures, err)
	}
	for time.Now().Before(deadline) && target.maintenance.Load() {
		time.Sleep(time.Millisecond)
	}
	health := coreStatus(t, securityRequest(target, "GET", "/api/v1/health", nil, nil), 200)
	if health["writable"] != true {
		t.Fatal("rollback with durable failure result left service stopped", health)
	}
	asset := securityRequest(target, "GET", "/api/v1/attachments/"+targetAttachment.ID+"/content", nil, nil)
	if asset.Code != 200 || asset.Body.String() != "test local attachment\n" {
		t.Fatal("rollback lost original upload", asset.Code, asset.Body.String())
	}
	saved := securityRequest(target, "PUT", "/api/v1/documents/"+original.ID, map[string]any{"title": original.Title, "markdown": "回退后继续编辑", "expected_revision": original.Revision}, map[string]string{"X-Data-Epoch": oldIdentity.DataEpoch})
	if saved.Code != 200 {
		t.Fatal("prior epoch could not resume after safe rollback", saved.Code, saved.Body.String())
	}
	state, err := readRestoreState(target.DataDir)
	if err != nil || state.State != "failed" || state.Phase != "rolled_back" {
		t.Fatal("failed result not durably recorded", state, err)
	}
}

func TestJobQueryOnlyTerminalFailureSurvivesRestartAsCancelled(t *testing.T) {
	a := coreApp(t)
	id, err := a.createJob(context.Background(), "export")
	if err != nil {
		t.Fatal(err)
	}
	a.startJob(id, false, func(ctx context.Context) (string, any, error) {
		_, err := a.DB.Exec("PRAGMA query_only=ON")
		return "tmp/exports/" + id + ".zip", map[string]any{"would_have_succeeded": true}, err
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cached, ok := a.liveJobs.Load(id)
		if !ok {
			t.Fatal("queued job cache missing")
		}
		job := cached.(operationJob)
		if job.State == "succeeded" {
			t.Fatal("job claimed success with terminal write disabled")
		}
		if job.State == "failed" {
			if job.DownloadURL != "" || job.ResultPath != nil {
				t.Fatal("failed job exposed a download", job)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	w := securityRequest(a, "GET", "/api/v1/jobs/"+id, nil, nil)
	if w.Code != 200 || responseData(t, w)["state"] != "failed" {
		t.Fatal("readonly terminal failure was not reported", w.Code, w.Body.String())
	}
	if w = securityRequest(a, "GET", "/api/v1/jobs/"+id+"/download", nil, nil); w.Code == 200 {
		t.Fatal("uncommitted job result was downloadable")
	}
	dir := a.DataDir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	w = securityRequest(reopened, "GET", "/api/v1/jobs/"+id, nil, nil)
	if w.Code != 200 || responseData(t, w)["state"] != "cancelled" {
		t.Fatal("interrupted uncommitted job remained running after restart", w.Code, w.Body.String())
	}
}
