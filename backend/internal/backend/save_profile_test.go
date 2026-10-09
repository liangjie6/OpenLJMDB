package backend

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in diagnosis for real edits: changing Markdown is rendered once, then
// written with the production FTS triggers and FULL/WAL durability settings.
func TestSaveStageProfile(t *testing.T) {
	if os.Getenv("LJMDB_PROFILE_SAVE") != "1" {
		t.Skip("set LJMDB_PROFILE_SAVE=1 to measure render and durable SQLite save stages")
	}
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	d := coreCreateDoc(t, a, kb, nil, "性能样本", "")
	paragraph := "中文知识库用于记录研究过程和实验结果。Markdown keeps source text, code, formulas and URLs.\n"
	base := strings.Repeat(paragraph, (100<<10)/len(paragraph))
	ctx := context.Background()
	for sample := 1; sample <= 3; sample++ {
		markdown := base + fmt.Sprintf("\nedit-token-%d\n", sample)
		start := time.Now()
		rendered := RenderMarkdown(markdown)
		renderTime := time.Since(start)
		start = time.Now()
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		current, err := LoadDocument(ctx, tx, d.ID, true)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if sample == 1 {
			if _, err = snapshotDocument(ctx, tx, current, "automatic", Now()); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		if err = SyncDocumentAttachments(ctx, tx, d.ID, rendered.AttachmentIDs); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, "UPDATE documents SET title=?,markdown=?,html=?,search_text=?,render_version=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND deleted_at IS NULL", d.Title, markdown, rendered.HTML, rendered.SearchText, rendered.Version, Now(), d.ID, current.Revision)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET updated_at=? WHERE id=?", Now(), kb); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = pruneHistory(ctx, tx, d.ID, ""); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		transactionTime := time.Since(start)
		start = time.Now()
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		commitTime := time.Since(start)
		t.Logf("bytes=%d changed_sample=%d render=%s SQL_and_FTS=%s durable_commit=%s", len(markdown), sample, renderTime, transactionTime, commitTime)
	}
}
