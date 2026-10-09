CREATE TABLE trash_batches (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('document_subtree','knowledge_base')),
 root_document_id TEXT, original_parent_id TEXT, knowledge_base_id TEXT NOT NULL, created_at INTEGER NOT NULL,
 restored_at INTEGER, purged_at INTEGER
);
CREATE TABLE knowledge_bases (
 id TEXT PRIMARY KEY, name TEXT NOT NULL CHECK(length(trim(name))>0), description TEXT NOT NULL DEFAULT '',
 sort_order INTEGER NOT NULL DEFAULT 0, tree_revision INTEGER NOT NULL DEFAULT 1 CHECK(tree_revision>=1),
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, deleted_at INTEGER,
 delete_batch_id TEXT REFERENCES trash_batches(id) ON DELETE RESTRICT,
 CHECK((deleted_at IS NULL)=(delete_batch_id IS NULL))
);
CREATE TABLE documents (
 row_id INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE RESTRICT,
 parent_id TEXT REFERENCES documents(id) ON DELETE RESTRICT,
 title TEXT NOT NULL CHECK(length(trim(title))>0), markdown TEXT NOT NULL DEFAULT '',
 html TEXT NOT NULL DEFAULT '', search_text TEXT NOT NULL DEFAULT '', render_version TEXT NOT NULL DEFAULT '',
 sort_order INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, deleted_at INTEGER,
 delete_batch_id TEXT REFERENCES trash_batches(id) ON DELETE RESTRICT,
 CHECK(parent_id IS NULL OR parent_id<>id), CHECK((deleted_at IS NULL)=(delete_batch_id IS NULL))
);
CREATE INDEX documents_tree_idx ON documents(knowledge_base_id,parent_id,deleted_at,sort_order);
CREATE INDEX documents_batch_idx ON documents(delete_batch_id);
CREATE TABLE document_revisions (
 id TEXT PRIMARY KEY, document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
 revision INTEGER NOT NULL CHECK(revision>=1), title TEXT NOT NULL, markdown TEXT NOT NULL,
 reason TEXT NOT NULL CHECK(reason IN ('automatic','manual','before_restore')), created_at INTEGER NOT NULL,
 UNIQUE(document_id,revision)
);
CREATE INDEX revisions_document_time_idx ON document_revisions(document_id,created_at DESC);
CREATE TABLE attachments (
 id TEXT PRIMARY KEY, original_name TEXT NOT NULL, storage_path TEXT NOT NULL UNIQUE,
 media_type TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0), sha256 TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','ready','pending_delete','deleted','missing')),
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE document_attachments (
 document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
 attachment_id TEXT NOT NULL REFERENCES attachments(id) ON DELETE RESTRICT, PRIMARY KEY(document_id,attachment_id)
);
CREATE TABLE revision_attachments (
 revision_id TEXT NOT NULL REFERENCES document_revisions(id) ON DELETE RESTRICT,
 attachment_id TEXT NOT NULL REFERENCES attachments(id) ON DELETE RESTRICT, PRIMARY KEY(revision_id,attachment_id)
);
CREATE INDEX document_attachments_attachment_idx ON document_attachments(attachment_id);
CREATE INDEX revision_attachments_attachment_idx ON revision_attachments(attachment_id);
CREATE TABLE cleanup_tasks (
 id TEXT PRIMARY KEY, attachment_id TEXT NOT NULL REFERENCES attachments(id) ON DELETE RESTRICT,
 state TEXT NOT NULL CHECK(state IN ('queued','running','done','failed','cancelled')),
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER, last_error TEXT,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX cleanup_one_active_task_idx ON cleanup_tasks(attachment_id) WHERE state IN ('queued','running','failed');
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL);
CREATE TABLE settings (key TEXT PRIMARY KEY,value_json TEXT NOT NULL CHECK(json_valid(value_json)),updated_at INTEGER NOT NULL);
CREATE TABLE operation_jobs (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('import','export','backup','restore')),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed','cancelled')),
 progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100), result_path TEXT,
 error_code TEXT, error_summary TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE idempotency_records (
 scope TEXT NOT NULL,idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,
 job_id TEXT REFERENCES operation_jobs(id) ON DELETE RESTRICT,response_json TEXT,created_at INTEGER NOT NULL,
 PRIMARY KEY(scope,idempotency_key)
);
CREATE VIEW documents_search_content AS SELECT row_id,title,search_text FROM documents WHERE deleted_at IS NULL;
CREATE VIRTUAL TABLE documents_fts USING fts5(title,search_text,content='documents_search_content',content_rowid='row_id',tokenize='unicode61');
CREATE TRIGGER documents_fts_ai AFTER INSERT ON documents WHEN new.deleted_at IS NULL BEGIN
 INSERT INTO documents_fts(rowid,title,search_text) VALUES(new.row_id,new.title,new.search_text);
END;
CREATE TRIGGER documents_fts_ad AFTER DELETE ON documents WHEN old.deleted_at IS NULL BEGIN
 INSERT INTO documents_fts(documents_fts,rowid,title,search_text) VALUES('delete',old.row_id,old.title,old.search_text);
END;
CREATE TRIGGER documents_fts_au AFTER UPDATE OF title,search_text,deleted_at ON documents BEGIN
 INSERT INTO documents_fts(documents_fts,rowid,title,search_text) SELECT 'delete',old.row_id,old.title,old.search_text WHERE old.deleted_at IS NULL;
 INSERT INTO documents_fts(rowid,title,search_text) SELECT new.row_id,new.title,new.search_text WHERE new.deleted_at IS NULL;
END;
INSERT INTO documents_fts(documents_fts) VALUES('rebuild');
