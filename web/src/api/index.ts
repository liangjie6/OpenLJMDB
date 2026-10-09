import { request, uploadForm, type UploadOptions } from './http'
import {
  asArray,
  asObject,
  normalizeAttachment,
  normalizeBackup,
  normalizeBackupValidation,
  normalizeCleanupPreview,
  normalizeCleanupResult,
  normalizeCreatedDocument,
  normalizeDeleteResult,
  normalizeDocument,
  normalizeHealth,
  normalizeHistoryDetail,
  normalizeHistoryItem,
  normalizeImportPreview,
  normalizeJob,
  normalizeKnowledgeBase,
  normalizePurgeResult,
  normalizeRestoreResult,
  normalizeSaveResult,
  normalizeSearchHit,
  normalizeSettings,
  normalizeTrashBatch,
  normalizeTrashDetail,
  normalizeTree,
  normalizeUploaded,
  num,
  str,
} from './normalize'
import type {
  ApiMeta,
  Attachment,
  BackupInfo,
  ExportRequest,
  EmptyTrashPreview,
  ImportClassification,
  ImportCommitBody,
  Job,
  KnowledgeBase,
  SaveDocumentBody,
  SearchPage,
  SettingsUpdateResult,
  TrashBatch,
} from './types'

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  meta: ApiMeta
}

function toPage<T>(data: unknown, meta: ApiMeta, map: (v: unknown) => T, page: number, pageSize: number): PageResult<T> {
  const obj = asObject(data)
  const list = Array.isArray(data) ? data : asArray(obj.items ?? obj.results ?? obj.list)
  const items = list.map(map)
  return {
    items,
    total: typeof meta.total === 'number' ? meta.total : (num(obj, 'total') ?? items.length),
    page: typeof meta.page === 'number' ? meta.page : page,
    page_size: typeof meta.page_size === 'number' ? meta.page_size : pageSize,
    meta,
  }
}

/** 逐页读取直到取完（有页数上限，避免无限请求） */
export async function fetchAllPages<T>(
  fetchPage: (page: number, pageSize: number) => Promise<PageResult<T>>,
  pageSize = 100,
  maxPages = 50,
): Promise<T[]> {
  const all: T[] = []
  for (let page = 1; page <= maxPages; page++) {
    const result = await fetchPage(page, pageSize)
    all.push(...result.items)
    if (result.items.length < pageSize || all.length >= result.total) break
  }
  return all
}

/** 异步任务接口返回 202 + 任务；部分实现可能同步返回结果 */
export type JobOrResult = { job: Job; result: null } | { job: null; result: Record<string, unknown> }

function jobOrResult(status: number, data: unknown): JobOrResult {
  const obj = asObject(data)
  const jobObj = asObject(obj.job)
  const looksLikeJob =
    status === 202 || Object.keys(jobObj).length > 0 || typeof obj.job_id === 'string' || (typeof obj.state === 'string' && typeof obj.kind === 'string')
  if (looksLikeJob) {
    const job = normalizeJob(Object.keys(jobObj).length > 0 ? jobObj : { ...obj, id: str(obj, 'job_id', 'id') })
    if (job.id) return { job, result: null }
  }
  return { job: null, result: obj }
}

export const api = {
  async health(signal?: AbortSignal) {
    const res = await request('GET', '/health', { signal, timeoutMs: 8000 })
    return normalizeHealth(res.data)
  },

  knowledgeBases: {
    async list(page = 1, pageSize = 100): Promise<PageResult<KnowledgeBase>> {
      const res = await request('GET', '/knowledge-bases', { query: { page, page_size: pageSize } })
      return toPage(res.data, res.meta, normalizeKnowledgeBase, page, pageSize)
    },
    listAll(): Promise<KnowledgeBase[]> {
      return fetchAllPages((page, size) => api.knowledgeBases.list(page, size))
    },
    async get(id: string) {
      const res = await request('GET', `/knowledge-bases/${encodeURIComponent(id)}`)
      return normalizeKnowledgeBase(res.data)
    },
    async create(body: { name: string; description: string }, idempotencyKey: string) {
      const res = await request('POST', '/knowledge-bases', { body, idempotencyKey })
      return normalizeKnowledgeBase(res.data)
    },
    async update(id: string, body: { name: string; description: string }) {
      const res = await request('PUT', `/knowledge-bases/${encodeURIComponent(id)}`, { body })
      return normalizeKnowledgeBase(res.data)
    },
    async remove(id: string, expectedTreeRevision: number | null) {
      const res = await request('DELETE', `/knowledge-bases/${encodeURIComponent(id)}`, {
        body: { expected_tree_revision: expectedTreeRevision },
      })
      return normalizeDeleteResult(res.data)
    },
    async tree(id: string, signal?: AbortSignal) {
      const res = await request('GET', `/knowledge-bases/${encodeURIComponent(id)}/tree`, { signal })
      return normalizeTree(res.data, id)
    },
    async createDocument(
      kbId: string,
      body: { parent_id: string | null; title: string; markdown: string; expected_tree_revision: number | null },
      idempotencyKey: string,
    ) {
      const res = await request('POST', `/knowledge-bases/${encodeURIComponent(kbId)}/documents`, { body, idempotencyKey })
      return normalizeCreatedDocument(res.data)
    },
  },

  documents: {
    async get(id: string, signal?: AbortSignal) {
      const res = await request('GET', `/documents/${encodeURIComponent(id)}`, { signal })
      return normalizeDocument(res.data)
    },
    async save(id: string, body: SaveDocumentBody, options: { signal?: AbortSignal; keepalive?: boolean; timeoutMs?: number } = {}) {
      const res = await request('PUT', `/documents/${encodeURIComponent(id)}`, {
        body,
        signal: options.signal,
        keepalive: options.keepalive,
        timeoutMs: options.timeoutMs ?? 20_000,
      })
      return normalizeSaveResult(res.data, id)
    },
    async remove(id: string, expectedTreeRevision: number | null) {
      const res = await request('DELETE', `/documents/${encodeURIComponent(id)}`, {
        body: { expected_tree_revision: expectedTreeRevision },
      })
      return normalizeDeleteResult(res.data)
    },
    async history(id: string, page = 1, pageSize = 50) {
      const res = await request('GET', `/documents/${encodeURIComponent(id)}/history`, { query: { page, page_size: pageSize } })
      return toPage(res.data, res.meta, normalizeHistoryItem, page, pageSize)
    },
    async historyDetail(id: string, historyId: string) {
      const res = await request('GET', `/documents/${encodeURIComponent(id)}/history/${encodeURIComponent(historyId)}`)
      return normalizeHistoryDetail(res.data)
    },
    async restoreHistory(id: string, historyId: string, expectedRevision: number) {
      const res = await request(
        'POST',
        `/documents/${encodeURIComponent(id)}/history/${encodeURIComponent(historyId)}/restore`,
        { body: { expected_revision: expectedRevision } },
      )
      const data = asObject(res.data)
      const docRaw = asObject(data.document)
      return normalizeDocument(Object.keys(docRaw).length > 0 ? docRaw : data)
    },
  },

  trash: {
    async list(params: { page?: number; pageSize?: number; knowledgeBaseId?: string | null } = {}) {
      const page = params.page ?? 1
      const pageSize = params.pageSize ?? 50
      const res = await request('GET', '/trash', {
        query: { page, page_size: pageSize, knowledge_base_id: params.knowledgeBaseId ?? undefined },
      })
      return toPage<TrashBatch>(res.data, res.meta, normalizeTrashBatch, page, pageSize)
    },
    async get(batchId: string) {
      const res = await request('GET', `/trash/${encodeURIComponent(batchId)}`)
      return normalizeTrashDetail(res.data)
    },
    async restore(batchId: string, expectedTreeRevision: number | null) {
      const res = await request('POST', `/trash/${encodeURIComponent(batchId)}/restore`, {
        body: { expected_tree_revision: expectedTreeRevision },
      })
      return normalizeRestoreResult(res.data)
    },
    async emptyPreview(): Promise<EmptyTrashPreview> {
      const res = await request('GET', '/trash/empty-preview')
      return res.data as EmptyTrashPreview
    },
    async empty(confirmationToken: string) {
      const res = await request('DELETE', '/trash', { body: { confirmation_token: confirmationToken } })
      return normalizePurgeResult(res.data)
    },
    async purge(batchId: string, confirmationToken: string) {
      const res = await request('DELETE', `/trash/${encodeURIComponent(batchId)}`, {
        body: { confirmation_token: confirmationToken },
      })
      return normalizePurgeResult(res.data)
    },
  },

  attachments: {
    async upload(file: File, options: UploadOptions & { documentId?: string } = {}) {
      const form = new FormData()
      form.append('file', file, file.name)
      if (options.documentId) form.append('document_id', options.documentId)
      const res = await uploadForm('/attachments', form, options)
      return normalizeUploaded(res.data)
    },
    async list(params: { filter?: 'unreferenced' | 'missing' | null; page?: number; pageSize?: number } = {}) {
      const page = params.page ?? 1
      const pageSize = params.pageSize ?? 50
      const res = await request('GET', '/attachments', {
        query: { filter: params.filter ?? undefined, page, page_size: pageSize },
      })
      return toPage<Attachment>(res.data, res.meta, normalizeAttachment, page, pageSize)
    },
    async get(id: string) {
      const res = await request('GET', `/attachments/${encodeURIComponent(id)}`)
      return normalizeAttachment(res.data)
    },
    async cleanupPreview(ids: string[]) {
      const res = await request('POST', '/attachments/cleanup-preview', { body: { attachment_ids: ids } })
      return normalizeCleanupPreview(res.data)
    },
    async cleanup(ids: string[], confirmationToken: string) {
      const res = await request('POST', '/attachments/cleanup', {
        body: { attachment_ids: ids, confirmation_token: confirmationToken },
      })
      return normalizeCleanupResult(res.data)
    },
  },

  async search(
    params: { q: string; knowledgeBaseId?: string | null; page?: number; pageSize?: number },
    signal?: AbortSignal,
  ): Promise<SearchPage> {
    const page = params.page ?? 1
    const pageSize = params.pageSize ?? 20
    const res = await request('GET', '/search', {
      query: { q: params.q, knowledge_base_id: params.knowledgeBaseId ?? undefined, page, page_size: pageSize },
      signal,
    })
    const pageResult = toPage(res.data, res.meta, normalizeSearchHit, page, pageSize)
    const obj = asObject(res.data)
    const mode = typeof res.meta.search_mode === 'string' ? res.meta.search_mode : str(obj, 'search_mode')
    return {
      items: pageResult.items,
      total: pageResult.total,
      page: pageResult.page,
      page_size: pageResult.page_size,
      search_mode: mode,
    }
  },

  imports: {
    async preflight(files: File[], options: UploadOptions = {}) {
      const form = new FormData()
      for (const file of files) form.append('file', file, file.name)
      const res = await uploadForm('/imports/preflight', form, options)
      return normalizeImportPreview(res.data)
    },
    async classify(previewId: string, sourceId: string, signal?: AbortSignal): Promise<ImportClassification> {
      const res = await request<ImportClassification>('POST', '/imports/classify', {
        body: { preview_id: previewId, source_id: sourceId }, signal, timeoutMs: 65_000,
      })
      return res.data
    },
    async commit(
      body: ImportCommitBody,
      idempotencyKey: string,
    ): Promise<JobOrResult> {
      const res = await request('POST', '/imports', { body, idempotencyKey })
      return jobOrResult(res.status, res.data)
    },
  },

  exports: {
    async start(body: ExportRequest, idempotencyKey: string): Promise<JobOrResult> {
      const res = await request('POST', '/exports', { body, idempotencyKey })
      return jobOrResult(res.status, res.data)
    },
  },

  backups: {
    async create(idempotencyKey: string): Promise<JobOrResult> {
      const res = await request('POST', '/backups', { body: {}, idempotencyKey })
      return jobOrResult(res.status, res.data)
    },
    async list(page = 1, pageSize = 50) {
      const res = await request('GET', '/backups', { query: { page, page_size: pageSize } })
      return toPage<BackupInfo>(res.data, res.meta, normalizeBackup, page, pageSize)
    },
    async validateUpload(file: File, options: UploadOptions = {}): Promise<JobOrResult> {
      const form = new FormData()
      form.append('file', file, file.name)
      const res = await uploadForm('/backups/validate', form, options)
      return jobOrResult(res.status, res.data)
    },
    async validateExisting(backupId: string): Promise<JobOrResult> {
      const res = await request('POST', '/backups/validate', { body: { backup_id: backupId }, timeoutMs: 120_000 })
      return jobOrResult(res.status, res.data)
    },
    parseValidation: normalizeBackupValidation,
    async restore(validatedBackupId: string, confirmationToken: string | null, idempotencyKey: string): Promise<JobOrResult> {
      const res = await request('POST', '/backups/restore', {
        body: { validated_backup_id: validatedBackupId, confirmation_token: confirmationToken },
        idempotencyKey,
      })
      return jobOrResult(res.status, res.data)
    },
  },

  jobs: {
    async get(id: string, signal?: AbortSignal) {
      const res = await request('GET', `/jobs/${encodeURIComponent(id)}`, { signal, timeoutMs: 10_000 })
      return normalizeJob(res.data)
    },
    downloadUrl(id: string) {
      return `/api/v1/jobs/${encodeURIComponent(id)}/download`
    },
  },

  settings: {
    async get() {
      const res = await request('GET', '/settings')
      return normalizeSettings(res.data)
    },
    async update(patch: Record<string, unknown>): Promise<SettingsUpdateResult> {
      const res = await request('PUT', '/settings', { body: patch })
      const obj = asObject(res.data)
      const restart = obj.restart_required === true || res.meta.restart_required === true
      return { settings: normalizeSettings(obj), restart_required: restart }
    },
  },
}

export type Api = typeof api
