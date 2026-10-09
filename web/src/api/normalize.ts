/**
 * 接口响应归一化。
 *
 * 后端由其他开发者并行实现，03 文档只约定了关键字段。这里按文档字段名读取，
 * 同时兼容少量常见别名，并为缺失字段提供安全默认值，避免页面因字段差异整体失效。
 */
import type {
  AppSettings,
  Attachment,
  AttachmentReferences,
  BackupInfo,
  BackupValidation,
  CleanupPreview,
  CleanupResult,
  CreatedDocument,
  DatasetSummary,
  DeleteResult,
  DocumentDetail,
  Health,
  HighlightRange,
  HistoryDetail,
  HistoryItem,
  ImportPreview,
  ImportPreviewDocument,
  Issue,
  Job,
  JobState,
  KnowledgeBase,
  Limits,
  MaintenanceState,
  PurgePreview,
  PurgeResult,
  RestoreAdjustment,
  RestoreResult,
  RevisionConflictInfo,
  RuntimeInfo,
  SaveResult,
  SearchHit,
  TrashBatch,
  TrashBatchDetail,
  TrashNode,
  TreeData,
  TreeNode,
  UploadedAttachment,
} from './types'

type Raw = Record<string, unknown>

export function asObject(value: unknown): Raw {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Raw) : {}
}

export function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function pick(raw: Raw, ...keys: string[]): unknown {
  for (const key of keys) {
    if (raw[key] !== undefined && raw[key] !== null) return raw[key]
  }
  return undefined
}

export function str(raw: Raw, ...keys: string[]): string | null {
  const value = pick(raw, ...keys)
  if (typeof value === 'string') return value
  if (typeof value === 'number') return String(value)
  return null
}

export function num(raw: Raw, ...keys: string[]): number | null {
  const value = pick(raw, ...keys)
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '' && Number.isFinite(Number(value))) return Number(value)
  return null
}

function bool(raw: Raw, ...keys: string[]): boolean | null {
  const value = pick(raw, ...keys)
  if (typeof value === 'boolean') return value
  if (value === 1 || value === 'true') return true
  if (value === 0 || value === 'false') return false
  return null
}

/** 时间字段约定为毫秒整数；兼容 ISO 字符串和秒级时间戳 */
export function time(raw: Raw, ...keys: string[]): number | null {
  const value = pick(raw, ...keys)
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value < 100_000_000_000 ? value * 1000 : value
  }
  if (typeof value === 'string' && value) {
    if (/^\d+$/.test(value)) return time({ v: Number(value) }, 'v')
    const parsed = Date.parse(value)
    return Number.isNaN(parsed) ? null : parsed
  }
  return null
}

function ranges(value: unknown): HighlightRange[] {
  const result: HighlightRange[] = []
  for (const item of asArray(value)) {
    if (Array.isArray(item) && item.length >= 2 && typeof item[0] === 'number' && typeof item[1] === 'number') {
      result.push([item[0], item[1]])
    } else if (item && typeof item === 'object') {
      const obj = item as Raw
      const start = num(obj, 'start')
      const end = num(obj, 'end')
      if (start !== null && end !== null) result.push([start, end])
    }
  }
  return result
}

export function normalizeLimits(value: unknown): Partial<Limits> {
  const raw = asObject(value)
  const out: Partial<Limits> = {}
  const set = (key: keyof Limits, ...aliases: string[]) => {
    const v = num(raw, key, ...aliases)
    if (v !== null && v > 0) out[key] = v
  }
  set('title_max_chars', 'title_max_length', 'max_title_chars')
  set('markdown_max_bytes', 'document_max_bytes', 'max_markdown_bytes')
  set('upload_max_bytes', 'attachment_max_bytes', 'max_upload_bytes')
  set('zip_max_bytes', 'import_max_bytes', 'max_zip_bytes')
  set('zip_max_expanded_bytes', 'zip_expanded_max_bytes')
  set('zip_max_entries', 'zip_entries_max')
  set('tree_max_depth', 'max_tree_depth', 'max_depth')
  set('search_query_max_chars', 'search_max_chars', 'max_query_chars')
  set('page_size_max', 'max_page_size')
  return out
}

function normalizeMaintenance(raw: Raw): MaintenanceState {
  const value = pick(raw, 'maintenance', 'maintenance_mode', 'maintenance_state')
  if (typeof value === 'boolean') {
    return { active: value, kind: null, message: null, job_id: null }
  }
  if (typeof value === 'string') {
    const active = value !== '' && value !== 'none' && value !== 'off' && value !== 'normal'
    return { active, kind: active ? value : null, message: null, job_id: null }
  }
  const obj = asObject(value)
  return {
    active: bool(obj, 'active', 'enabled', 'on') ?? false,
    kind: str(obj, 'kind', 'reason', 'type'),
    message: str(obj, 'message', 'description'),
    job_id: str(obj, 'job_id'),
  }
}

export function normalizeHealth(value: unknown): Health {
  const raw = asObject(value)
  const db = pick(raw, 'database', 'database_status', 'db_status', 'db')
  const database = typeof db === 'string' ? db : (str(asObject(db), 'status', 'state') ?? 'unknown')
  return {
    instance_id: str(raw, 'instance_id') ?? '',
    data_epoch: str(raw, 'data_epoch') ?? '',
    version: str(raw, 'version', 'app_version') ?? '',
    schema_version: num(raw, 'schema_version'),
    database,
    writable: bool(raw, 'writable', 'data_writable') ?? true,
    maintenance: normalizeMaintenance(raw),
    limits: normalizeLimits(raw.limits),
  }
}

export function normalizeKnowledgeBase(value: unknown): KnowledgeBase {
  const raw = asObject(value)
  return {
    id: str(raw, 'id') ?? '',
    name: str(raw, 'name') ?? '',
    description: str(raw, 'description') ?? '',
    document_count: num(raw, 'document_count', 'documents_count', 'doc_count') ?? 0,
    tree_revision: num(raw, 'tree_revision'),
    created_at: time(raw, 'created_at') ?? 0,
    updated_at: time(raw, 'updated_at', 'last_activity_at') ?? 0,
  }
}

function flattenTreeNodes(list: unknown[], parentId: string | null, out: TreeNode[]): void {
  list.forEach((item, index) => {
    const raw = asObject(item)
    const id = str(raw, 'id')
    if (!id) return
    const explicitParent = raw.parent_id === undefined ? parentId : (str(raw, 'parent_id') ?? null)
    out.push({
      id,
      parent_id: explicitParent,
      title: str(raw, 'title') ?? '无标题',
      sort_order: num(raw, 'sort_order', 'position') ?? index,
      created_at: time(raw, 'created_at'),
      updated_at: time(raw, 'updated_at'),
    })
    const children = asArray(raw.children)
    if (children.length > 0) flattenTreeNodes(children, id, out)
  })
}

export function normalizeTree(value: unknown, knowledgeBaseId: string): TreeData {
  const raw = asObject(value)
  const list = Array.isArray(value) ? value : asArray(pick(raw, 'nodes', 'documents', 'items', 'tree'))
  const nodes: TreeNode[] = []
  flattenTreeNodes(list, null, nodes)
  return {
    knowledge_base_id: str(raw, 'knowledge_base_id') ?? knowledgeBaseId,
    tree_revision: num(raw, 'tree_revision', 'revision') ?? 0,
    nodes,
  }
}

export function normalizeDocument(value: unknown): DocumentDetail {
  const raw = asObject(value)
  return {
    id: str(raw, 'id', 'document_id') ?? '',
    knowledge_base_id: str(raw, 'knowledge_base_id') ?? '',
    parent_id: str(raw, 'parent_id'),
    title: str(raw, 'title') ?? '',
    markdown: str(raw, 'markdown', 'content') ?? '',
    html: str(raw, 'html') ?? '',
    render_version: str(raw, 'render_version') ?? '',
    revision: num(raw, 'revision') ?? 1,
    created_at: time(raw, 'created_at') ?? 0,
    updated_at: time(raw, 'updated_at') ?? 0,
  }
}

export function normalizeCreatedDocument(value: unknown): CreatedDocument {
  const raw = asObject(value)
  const doc = asObject(raw.document)
  const base = normalizeDocument(Object.keys(doc).length > 0 ? doc : raw)
  return { ...base, tree_revision: num(raw, 'tree_revision') ?? num(doc, 'tree_revision') }
}

export function normalizeSaveResult(value: unknown, fallbackId: string): SaveResult {
  const raw = asObject(value)
  const doc = asObject(raw.document)
  const src = Object.keys(doc).length > 0 ? { ...raw, ...doc } : raw
  return {
    id: str(src, 'id', 'document_id') ?? fallbackId,
    revision: num(src, 'revision', 'new_revision') ?? 0,
    updated_at: time(src, 'updated_at', 'saved_at') ?? Date.now(),
    title: str(src, 'title'),
  }
}

export function normalizeConflict(details: Record<string, unknown>): RevisionConflictInfo {
  const raw = asObject(details)
  const current = asObject(pick(raw, 'current', 'server'))
  const src = Object.keys(current).length > 0 ? { ...raw, ...current } : raw
  return {
    revision: num(src, 'current_revision', 'server_revision', 'revision'),
    title: str(src, 'title', 'server_title'),
    updated_at: time(src, 'updated_at', 'server_updated_at'),
    excerpt: str(src, 'excerpt', 'markdown_excerpt', 'summary', 'markdown_summary'),
  }
}

export function normalizeDeleteResult(value: unknown): DeleteResult {
  const raw = asObject(value)
  const batch = asObject(pick(raw, 'batch', 'trash_batch'))
  return {
    batch_id: str(raw, 'batch_id', 'trash_batch_id') ?? str(batch, 'id'),
    document_count: num(raw, 'document_count', 'deleted_count') ?? num(batch, 'document_count') ?? 0,
    tree_revision: num(raw, 'tree_revision'),
  }
}

export function normalizeHistoryItem(value: unknown): HistoryItem {
  const raw = asObject(value)
  return {
    id: str(raw, 'id', 'history_id') ?? '',
    revision: num(raw, 'revision') ?? 0,
    title: str(raw, 'title') ?? '',
    reason: str(raw, 'reason') ?? 'automatic',
    created_at: time(raw, 'created_at') ?? 0,
    size_bytes: num(raw, 'size_bytes', 'markdown_bytes', 'size'),
  }
}

export function normalizeHistoryDetail(value: unknown): HistoryDetail {
  const raw = asObject(value)
  return { ...normalizeHistoryItem(raw), markdown: str(raw, 'markdown', 'content') ?? '' }
}

export function normalizeTrashBatch(value: unknown): TrashBatch {
  const raw = asObject(value)
  const kb = asObject(raw.knowledge_base)
  const kindRaw = str(raw, 'kind', 'type')
  const kind = kindRaw === 'knowledge_base' ? 'knowledge_base' : 'document_subtree'
  return {
    id: str(raw, 'id', 'batch_id') ?? '',
    kind,
    knowledge_base_id: str(raw, 'knowledge_base_id') ?? str(kb, 'id') ?? '',
    knowledge_base_name: str(raw, 'knowledge_base_name') ?? str(kb, 'name') ?? '',
    root_document_id: str(raw, 'root_document_id'),
    root_title: str(raw, 'root_title', 'title', 'root_document_title', 'name') ?? '',
    document_count: num(raw, 'document_count', 'documents_count') ?? 0,
    created_at: time(raw, 'created_at', 'deleted_at') ?? 0,
  }
}

function normalizePurgePreview(value: unknown, fallbackToken: string | null): PurgePreview | null {
  const raw = asObject(value)
  const token = str(raw, 'confirmation_token') ?? fallbackToken
  if (!token) return null
  return {
    confirmation_token: token,
    expires_at: time(raw, 'expires_at'),
    document_count: num(raw, 'document_count') ?? 0,
    attachment_count: num(raw, 'attachment_count', 'attachment_reference_count'),
    history_count: num(raw, 'history_count', 'revision_count'),
    batch_count: num(raw, 'batch_count', 'other_batch_count'),
  }
}

export function normalizeTrashDetail(value: unknown): TrashBatchDetail {
  const raw = asObject(value)
  const batch = asObject(raw.batch)
  const base = normalizeTrashBatch(Object.keys(batch).length > 0 ? { ...raw, ...batch } : raw)
  const nodes: TrashNode[] = asArray(pick(raw, 'nodes', 'documents', 'items')).map((item) => {
    const node = asObject(item)
    return {
      id: str(node, 'id') ?? '',
      parent_id: str(node, 'parent_id'),
      title: str(node, 'title') ?? '无标题',
    }
  })
  const kb = asObject(raw.knowledge_base)
  const previewRaw = pick(raw, 'delete_preview', 'purge_preview', 'preview')
  const preview = normalizePurgePreview(previewRaw ?? raw, str(raw, 'confirmation_token'))
  if (preview && preview.document_count === 0) preview.document_count = base.document_count
  return {
    ...base,
    nodes,
    tree_revision: num(raw, 'tree_revision', 'expected_tree_revision') ?? num(kb, 'tree_revision'),
    knowledge_base_deleted: bool(raw, 'knowledge_base_deleted') ?? bool(kb, 'deleted') ?? base.kind === 'knowledge_base',
    delete_preview: preview,
  }
}

export function normalizeRestoreResult(value: unknown): RestoreResult {
  const raw = asObject(value)
  const adjustments: RestoreAdjustment[] = asArray(pick(raw, 'adjustments', 'parent_adjustments', 'relocated')).map((item) => {
    const adj = asObject(item)
    return {
      document_id: str(adj, 'document_id', 'id'),
      title: str(adj, 'title') ?? '',
      reason: str(adj, 'reason', 'code') ?? '',
      message: str(adj, 'message') ?? '',
    }
  })
  return {
    restored_document_count: num(raw, 'restored_document_count', 'restored_count', 'document_count') ?? 0,
    knowledge_base_restored: bool(raw, 'knowledge_base_restored') ?? false,
    root_document_id: str(raw, 'root_document_id'),
    knowledge_base_id: str(raw, 'knowledge_base_id'),
    adjustments,
  }
}

export function normalizePurgeResult(value: unknown): PurgeResult {
  const raw = asObject(value)
  return {
    purged_document_count: num(raw, 'purged_document_count', 'deleted_document_count', 'document_count'),
    cleanup_candidate_count: num(raw, 'cleanup_candidate_count', 'attachment_candidate_count'),
    message: str(raw, 'message'),
  }
}

function normalizeReferences(value: unknown): AttachmentReferences | null {
  const raw = asObject(value)
  if (Object.keys(raw).length === 0) return null
  return {
    current: num(raw, 'current', 'documents', 'current_count') ?? 0,
    history: num(raw, 'history', 'revisions', 'history_count') ?? 0,
    trash: num(raw, 'trash', 'trash_count', 'deleted') ?? 0,
  }
}

export function attachmentUrl(id: string): string {
  return `/api/v1/attachments/${encodeURIComponent(id)}/content`
}

export function normalizeAttachment(value: unknown): Attachment {
  const raw = asObject(value)
  const id = str(raw, 'id') ?? ''
  const refs =
    normalizeReferences(pick(raw, 'references', 'reference_counts')) ??
    (num(raw, 'current_reference_count') !== null
      ? {
          current: num(raw, 'current_reference_count') ?? 0,
          history: num(raw, 'history_reference_count') ?? 0,
          trash: num(raw, 'trash_reference_count') ?? 0,
        }
      : null)
  return {
    id,
    original_name: str(raw, 'original_name', 'name', 'filename') ?? id,
    media_type: str(raw, 'media_type', 'content_type', 'mime_type') ?? 'application/octet-stream',
    size_bytes: num(raw, 'size_bytes', 'size') ?? 0,
    state: str(raw, 'state', 'status') ?? 'ready',
    created_at: time(raw, 'created_at') ?? 0,
    url: str(raw, 'url') ?? attachmentUrl(id),
    references: refs,
  }
}

export function normalizeUploaded(value: unknown): UploadedAttachment {
  const att = normalizeAttachment(value)
  return {
    id: att.id,
    url: att.url,
    original_name: att.original_name,
    media_type: att.media_type,
    size_bytes: att.size_bytes,
    state: att.state,
  }
}

export function normalizeCleanupPreview(value: unknown): CleanupPreview {
  const raw = asObject(value)
  return {
    confirmation_token: str(raw, 'confirmation_token') ?? '',
    expires_at: time(raw, 'expires_at'),
    items: asArray(pick(raw, 'items', 'attachments', 'results')).map((item) => {
      const obj = asObject(item)
      const deletable = bool(obj, 'deletable', 'can_delete', 'eligible') ?? !(str(obj, 'reason', 'skip_reason') ?? '')
      return {
        id: str(obj, 'id', 'attachment_id') ?? '',
        original_name: str(obj, 'original_name', 'name') ?? '',
        deletable,
        reason: str(obj, 'reason', 'skip_reason'),
        references: normalizeReferences(pick(obj, 'references', 'reference_counts')),
      }
    }),
  }
}

export function normalizeCleanupResult(value: unknown): CleanupResult {
  const raw = asObject(value)
  return {
    queued: asArray(pick(raw, 'queued', 'accepted', 'queued_ids')).map((v) =>
      typeof v === 'string' ? v : (str(asObject(v), 'id', 'attachment_id') ?? ''),
    ),
    skipped: asArray(raw.skipped).map((v) => {
      const obj = asObject(v)
      return { id: str(obj, 'id', 'attachment_id') ?? '', reason: str(obj, 'reason', 'message') ?? '' }
    }),
  }
}

export function normalizeSearchHit(value: unknown): SearchHit {
  const raw = asObject(value)
  const kb = asObject(raw.knowledge_base)
  const snippets = asArray(raw.snippets)
  const firstSnippet = asObject(snippets[0])
  const snippetObj = asObject(raw.snippet)
  const snippetText =
    typeof raw.snippet === 'string'
      ? raw.snippet
      : (str(snippetObj, 'text') ?? str(firstSnippet, 'text') ?? str(raw, 'excerpt') ?? '')
  const snippetRanges = ranges(
    pick(raw, 'highlights', 'snippet_highlights', 'ranges') ?? snippetObj.highlights ?? firstSnippet.highlights ?? firstSnippet.ranges,
  )
  const pathRaw = pick(raw, 'path', 'ancestors', 'breadcrumbs')
  const path = Array.isArray(pathRaw)
    ? pathRaw.map((p) => (typeof p === 'string' ? p : (str(asObject(p), 'title') ?? ''))).filter(Boolean)
    : typeof pathRaw === 'string'
      ? pathRaw.split('/').filter(Boolean)
      : []
  return {
    document_id: str(raw, 'document_id', 'id') ?? '',
    knowledge_base_id: str(raw, 'knowledge_base_id') ?? str(kb, 'id') ?? '',
    knowledge_base_name: str(raw, 'knowledge_base_name') ?? str(kb, 'name') ?? '',
    title: str(raw, 'title') ?? '',
    title_highlights: ranges(pick(raw, 'title_highlights', 'title_ranges')),
    path,
    snippet: snippetText,
    highlights: snippetRanges,
    updated_at: time(raw, 'updated_at'),
  }
}

function normalizeIssue(value: unknown, severity: 'error' | 'warning'): Issue {
  if (typeof value === 'string') return { code: null, path: null, message: value, severity }
  const raw = asObject(value)
  const sev = str(raw, 'severity', 'level')
  return {
    code: str(raw, 'code', 'type'),
    path: str(raw, 'path', 'file', 'entry'),
    message: str(raw, 'message', 'detail', 'reason') ?? str(raw, 'code') ?? '未知问题',
    severity: sev === 'error' || sev === 'warning' ? sev : severity,
  }
}

export function normalizeIssues(value: unknown, severity: 'error' | 'warning'): Issue[] {
  return asArray(value).map((v) => normalizeIssue(v, severity))
}

export function normalizeImportPreview(value: unknown): ImportPreview {
  const raw = asObject(value)
  const summary = asObject(raw.summary)
  const src = { ...summary, ...raw }
  const rawDocuments = asArray(pick(raw, 'documents', 'tree', 'items')).map(asObject)
  const bySourceId = new Map(rawDocuments.map((doc) => [str(doc, 'source_id'), doc]))
  const treeDepth = (doc: Raw): number => {
    let depth = 1
    const seen = new Set<Raw>([doc])
    let parentId = str(doc, 'parent_source_id')
    while (parentId) {
      const parent = bySourceId.get(parentId)
      if (!parent || seen.has(parent)) break
      seen.add(parent)
      depth++
      parentId = str(parent, 'parent_source_id')
    }
    return depth
  }
  const documents: ImportPreviewDocument[] = rawDocuments.map((obj) => {
    const path = str(obj, 'path', 'source_path') ?? ''
    const parent = bySourceId.get(str(obj, 'parent_source_id'))
    return {
      source_id: str(obj, 'source_id') ?? '',
      path,
      title: str(obj, 'title') ?? path,
      parent_path: str(obj, 'parent_path') ?? (parent ? str(parent, 'path', 'source_path') : null),
      depth: num(obj, 'depth', 'level') ?? (str(obj, 'source_id') ? treeDepth(obj) : Math.max(1, path.split('/').filter(Boolean).length)),
      duplicate_title: bool(obj, 'duplicate_title', 'duplicate', 'name_conflict') ?? false,
    }
  })
  const allIssues = asArray(raw.issues)
  const errors = [
    ...normalizeIssues(raw.errors, 'error'),
    ...allIssues.map((i) => normalizeIssue(i, 'warning')).filter((i) => i.severity === 'error'),
  ]
  const warnings = [
    ...normalizeIssues(raw.warnings, 'warning'),
    ...allIssues.map((i) => normalizeIssue(i, 'warning')).filter((i) => i.severity === 'warning'),
  ]
  return {
    preview_id: str(src, 'preview_id', 'id') ?? '',
    source_kind: src.source_kind === 'markdown' || src.source_kind === 'zip' ? src.source_kind : null,
    expires_at: time(src, 'expires_at'),
    source_name: str(src, 'source_name', 'file_name', 'filename'),
    document_count: num(src, 'document_count', 'documents_count') ?? documents.length,
    attachment_count: num(src, 'attachment_count', 'asset_count', 'resource_count') ?? asArray(raw.attachments).length,
    total_bytes: num(src, 'total_bytes', 'total_size', 'size_bytes') ?? 0,
    max_depth: num(src, 'max_depth', 'depth') ?? documents.reduce((m, d) => Math.max(m, d.depth), 0),
    documents,
    warnings,
    errors,
  }
}

const JOB_STATES: JobState[] = ['queued', 'running', 'succeeded', 'failed', 'cancelled']

export function normalizeJob(value: unknown): Job {
  const raw0 = asObject(value)
  const raw = Object.keys(asObject(raw0.job)).length > 0 ? { ...raw0, ...asObject(raw0.job) } : raw0
  const stateRaw = str(raw, 'state', 'status') ?? 'queued'
  const state = (
    JOB_STATES.includes(stateRaw as JobState)
      ? stateRaw
      : stateRaw === 'completed' || stateRaw === 'done' || stateRaw === 'success'
        ? 'succeeded'
        : stateRaw === 'error'
          ? 'failed'
          : 'running'
  ) as JobState
  const result = raw.result && typeof raw.result === 'object' ? (raw.result as Record<string, unknown>) : null
  return {
    id: str(raw, 'id', 'job_id') ?? '',
    kind: str(raw, 'kind', 'type') ?? '',
    state,
    progress: Math.max(0, Math.min(100, num(raw, 'progress') ?? (state === 'succeeded' ? 100 : 0))),
    phase: str(raw, 'phase', 'stage', 'step'),
    result,
    download_url: str(raw, 'download_url', 'result_url') ?? (result ? str(result, 'download_url') : null),
    error_code: str(raw, 'error_code'),
    error_summary: str(raw, 'error_summary', 'error_message', 'error'),
    created_at: time(raw, 'created_at'),
    updated_at: time(raw, 'updated_at'),
  }
}

export function normalizeBackup(value: unknown): BackupInfo {
  const raw = asObject(value)
  return {
    id: str(raw, 'id', 'backup_id') ?? '',
    file_name: str(raw, 'file_name', 'filename', 'name'),
    created_at: time(raw, 'created_at') ?? 0,
    size_bytes: num(raw, 'size_bytes', 'size'),
    app_version: str(raw, 'app_version'),
    schema_version: num(raw, 'schema_version'),
    document_count: num(raw, 'document_count'),
    attachment_count: num(raw, 'attachment_count'),
    sha256: str(raw, 'sha256', 'checksum'),
    state: str(raw, 'state', 'status'),
    download_url: str(raw, 'download_url'),
  }
}

function normalizeSummary(value: unknown): DatasetSummary {
  const raw = asObject(value)
  return {
    created_at: time(raw, 'created_at', 'updated_at', 'time'),
    knowledge_base_count: num(raw, 'knowledge_base_count', 'knowledge_bases'),
    document_count: num(raw, 'document_count', 'documents'),
    attachment_count: num(raw, 'attachment_count', 'attachments'),
    history_count: num(raw, 'history_count', 'revisions', 'history'),
    trash_batch_count: num(raw, 'trash_batch_count', 'trash_batches'),
    schema_version: num(raw, 'schema_version'),
    app_version: str(raw, 'app_version', 'version'),
    instance_id: str(raw, 'instance_id'),
  }
}

export function normalizeBackupValidation(value: unknown): BackupValidation {
  const raw = asObject(value)
  const issues = [...normalizeIssues(raw.issues, 'error'), ...normalizeIssues(raw.errors, 'error')]
  const valid = bool(raw, 'valid', 'ok', 'compatible') ?? issues.every((i) => i.severity !== 'error')
  const backupRaw = pick(raw, 'backup', 'backup_summary', 'manifest')
  const currentRaw = pick(raw, 'current', 'current_summary')
  return {
    validated_backup_id: str(raw, 'validated_backup_id', 'backup_id', 'id') ?? '',
    valid,
    issues,
    backup: normalizeSummary(backupRaw),
    current: currentRaw ? normalizeSummary(currentRaw) : null,
    confirmation_token: str(raw, 'confirmation_token'),
    expires_at: time(raw, 'expires_at'),
  }
}

export function normalizeSettings(value: unknown): AppSettings {
  const raw = asObject(value)
  const runtimeRaw = asObject(pick(raw, 'runtime', 'startup', 'server'))
  const settingsRaw = asObject(pick(raw, 'settings', 'product', 'values'))
  const rt = { ...raw, ...runtimeRaw }
  const runtime: RuntimeInfo = {
    data_dir: str(rt, 'data_dir', 'data_path', 'data_directory'),
    base_url: str(rt, 'base_url', 'base_path', 'url'),
    host: str(rt, 'host', 'listen_host', 'bind_host'),
    port: num(rt, 'port', 'listen_port'),
    portable: bool(rt, 'portable', 'portable_mode'),
    version: str(rt, 'version', 'app_version'),
    schema_version: num(rt, 'schema_version'),
    database: (() => {
      const db = pick(rt, 'database', 'database_status', 'db_status')
      return typeof db === 'string' ? db : str(asObject(db), 'status')
    })(),
    log_dir: str(rt, 'log_dir', 'logs_dir'),
  }
  const merged = { ...raw, ...settingsRaw }
  return {
    runtime,
    locale: str(merged, 'locale', 'language'),
    timezone: str(merged, 'timezone', 'tz'),
    theme: (() => {
      const t = str(merged, 'theme', 'ui_theme')
      return t === 'light' || t === 'dark' || t === 'auto' ? t : null
    })(),
    history_limit: num(merged, 'history_limit', 'history_max_versions', 'history_retention', 'max_history'),
    import_auto_classify: bool(merged, 'import_auto_classify') ?? false,
    import_classifier_configured: bool(merged, 'import_classifier_configured') ?? false,
    limits: normalizeLimits(pick(raw, 'limits') ?? pick(runtimeRaw, 'limits')),
    raw,
  }
}

export function normalizeConflictOrNull(details: Record<string, unknown> | undefined): RevisionConflictInfo | null {
  if (!details) return null
  return normalizeConflict(details)
}
