/**
 * 前端使用的接口数据类型。
 *
 * 依据 docs/03-数据模型与接口设计.md：JSON 字段为 snake_case，ID 为 UUID 字符串，
 * 时间为 UTC Unix 毫秒整数。文档未逐字段约定的部分，前端在 normalize.ts 中做了容错，
 * 实际期望的字段见 web/README.md「接口约定」一节。
 */

export type UUID = string
/** UTC Unix 毫秒 */
export type Millis = number
/** `[start, end)`，按 Unicode 码点计数 */
export type HighlightRange = [number, number]

export interface ApiMeta {
  total?: number
  page?: number
  page_size?: number
  search_mode?: string
  [key: string]: unknown
}

export interface ApiErrorPayload {
  code: string
  message: string
  details?: Record<string, unknown>
}

export interface MaintenanceState {
  active: boolean
  kind: string | null
  message: string | null
  job_id: UUID | null
}

export interface Health {
  instance_id: UUID
  data_epoch: UUID
  version: string
  schema_version: number | null
  database: string
  writable: boolean
  maintenance: MaintenanceState
  limits: Partial<Limits>
  runtime?: RuntimeInfo
}

export interface Limits {
  title_max_chars: number
  markdown_max_bytes: number
  upload_max_bytes: number
  zip_max_bytes: number
  zip_max_expanded_bytes: number
  zip_max_entries: number
  tree_max_depth: number
  search_query_max_chars: number
  page_size_max: number
}

export interface KnowledgeBase {
  id: UUID
  name: string
  description: string
  document_count: number
  tree_revision: number | null
  created_at: Millis
  updated_at: Millis
}

export interface TreeNode {
  id: UUID
  parent_id: UUID | null
  title: string
  sort_order: number
  created_at: Millis | null
  updated_at: Millis | null
}

export interface TreeData {
  knowledge_base_id: UUID
  tree_revision: number
  nodes: TreeNode[]
}

export interface DocumentDetail {
  id: UUID
  knowledge_base_id: UUID
  parent_id: UUID | null
  title: string
  markdown: string
  html: string
  render_version: string
  revision: number
  created_at: Millis
  updated_at: Millis
}

export interface CreatedDocument extends DocumentDetail {
  tree_revision: number | null
}

export interface SaveDocumentBody {
  title: string
  markdown: string
  expected_revision: number
  snapshot?: boolean
}

export interface SaveResult {
  id: UUID
  revision: number
  updated_at: Millis
  title: string | null
}

export interface RevisionConflictInfo {
  revision: number | null
  title: string | null
  updated_at: Millis | null
  excerpt: string | null
}

export interface DeleteResult {
  batch_id: UUID | null
  document_count: number
  tree_revision: number | null
}

export type HistoryReason = 'automatic' | 'manual' | 'before_restore' | string

export interface HistoryItem {
  id: UUID
  revision: number
  title: string
  reason: HistoryReason
  created_at: Millis
  size_bytes: number | null
}

export interface HistoryDetail extends HistoryItem {
  markdown: string
}

export type TrashKind = 'document_subtree' | 'knowledge_base'

export interface TrashBatch {
  id: UUID
  kind: TrashKind
  knowledge_base_id: UUID
  knowledge_base_name: string
  root_document_id: UUID | null
  root_title: string
  document_count: number
  created_at: Millis
}

export interface TrashNode {
  id: UUID
  parent_id: UUID | null
  title: string
}

export interface PurgePreview {
  confirmation_token: string
  expires_at: Millis | null
  document_count: number
  attachment_count: number | null
  history_count: number | null
  batch_count: number | null
}

export interface TrashBatchDetail extends TrashBatch {
  nodes: TrashNode[]
  tree_revision: number | null
  knowledge_base_deleted: boolean
  delete_preview: PurgePreview | null
}

export interface RestoreAdjustment {
  document_id: UUID | null
  title: string
  reason: string
  message: string
}

export interface RestoreResult {
  restored_document_count: number
  knowledge_base_restored: boolean
  root_document_id: UUID | null
  knowledge_base_id: UUID | null
  adjustments: RestoreAdjustment[]
}

export interface EmptyTrashPreview {
  delete_preview: {
    batch_count: number
    knowledge_base_count: number
    document_count: number
    history_count: number
    content_digest: string
  }
  confirmation_token: string
}

export interface PurgeResult {
  purged_document_count: number | null
  cleanup_candidate_count: number | null
  message: string | null
}

export type AttachmentState = 'pending' | 'ready' | 'pending_delete' | 'deleted' | 'missing' | string

export interface AttachmentReferences {
  current: number
  history: number
  trash: number
}

export interface Attachment {
  id: UUID
  original_name: string
  media_type: string
  size_bytes: number
  state: AttachmentState
  created_at: Millis
  url: string
  references: AttachmentReferences | null
}

export interface AttachmentInfo extends Attachment {
  referrer_count: number
}


export interface UploadedAttachment {
  id: UUID
  url: string
  original_name: string
  media_type: string
  size_bytes: number
  state: AttachmentState
}

export interface CleanupPreviewItem {
  id: UUID
  original_name: string
  deletable: boolean
  reason: string | null
  references: AttachmentReferences | null
}

export interface CleanupPreview {
  confirmation_token: string
  expires_at: Millis | null
  items: CleanupPreviewItem[]
}

export interface CleanupResult {
  deleted_count?: number
  queued: UUID[]
  skipped: { id: UUID; reason: string }[]
}

export interface SearchHit {
  document_id: UUID
  knowledge_base_id: UUID
  knowledge_base_name: string
  title: string
  title_highlights: HighlightRange[]
  path: string[]
  snippet: string
  highlights: HighlightRange[]
  updated_at: Millis | null
}

export interface SearchPage {
  items: SearchHit[]
  total: number
  page: number
  page_size: number
  search_mode: string | null
}

export interface Issue {
  code: string | null
  path: string | null
  message: string
  severity: 'error' | 'warning'
}

export interface ImportPreviewDocument {
  source_id: UUID
  path: string
  title: string
  parent_path: string | null
  depth: number
  duplicate_title: boolean
}

export interface ImportPreview {
  preview_id: UUID
  source_kind: 'markdown' | 'zip' | null
  expires_at: Millis | null
  source_name: string | null
  document_count: number
  attachment_count: number
  total_bytes: number
  max_depth: number
  documents: ImportPreviewDocument[]
  warnings: Issue[]
  errors: Issue[]
}

export interface ImportTarget {
  source_id: UUID
  target_knowledge_base_id: UUID
  target_parent_id: UUID | null
  expected_tree_revision: number
}

export interface ImportCommitBody {
  preview_id: UUID
  target_knowledge_base_id?: UUID
  target_parent_id?: UUID | null
  expected_tree_revision?: number
  targets?: ImportTarget[]
}

export interface ImportClassification {
  source_id: UUID
  target_knowledge_base_id: UUID
  target_parent_id: UUID | null
  probability: number
  confidence: number
}

export type JobState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface Job {
  id: UUID
  kind: string
  state: JobState
  progress: number
  phase: string | null
  result: Record<string, unknown> | null
  download_url: string | null
  error_code: string | null
  error_summary: string | null
  created_at: Millis | null
  updated_at: Millis | null
}

export interface ExportRequest {
  scope: 'document' | 'knowledge_base'
  id: UUID
  format: 'markdown' | 'html'
  include_attachments: boolean
}

export interface BackupInfo {
  id: UUID
  file_name: string | null
  created_at: Millis
  size_bytes: number | null
  app_version: string | null
  schema_version: number | null
  document_count: number | null
  attachment_count: number | null
  sha256: string | null
  state: string | null
  download_url: string | null
}

export interface DatasetSummary {
  created_at: Millis | null
  knowledge_base_count: number | null
  document_count: number | null
  attachment_count: number | null
  history_count: number | null
  trash_batch_count: number | null
  schema_version: number | null
  app_version: string | null
  instance_id: string | null
}

export interface BackupValidation {
  validated_backup_id: UUID
  valid: boolean
  issues: Issue[]
  backup: DatasetSummary
  current: DatasetSummary | null
  confirmation_token: string | null
  expires_at: Millis | null
}

export interface RuntimeInfo {
  data_dir: string | null
  base_url: string | null
  host: string | null
  port: number | null
  portable: boolean | null
  version: string | null
  schema_version: number | null
  database: string | null
  log_dir: string | null
}

export interface AppSettings {
  runtime: RuntimeInfo
  locale: string | null
  timezone: string | null
  theme: 'light' | 'dark' | 'auto' | null
  history_limit: number | null
  import_auto_classify: boolean
  import_classifier_configured: boolean
  limits: Partial<Limits>
  raw: Record<string, unknown>
}

export interface SettingsUpdateResult {
  settings: AppSettings
  restart_required: boolean
}

// 兼容别名：Settings = AppSettings
export type Settings = AppSettings
