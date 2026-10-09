/**
 * 文档自动保存控制器（04 文档第 5 节状态机）。
 *
 * - 输入停止 3 秒防抖保存；持续输入时，从首个未保存修改起最长 30 秒触发一次保存。
 * - 每篇文档同一时刻只有一个保存请求在途；请求期间的新修改进入下一轮。
 * - 成功响应只确认该请求携带的快照；请求期间继续输入时仍显示有修改待保存。
 * - 临时故障按 2/4/8/16/30 秒有界退避，最多连续自动重试 5 次后暂停；不以新的输入作为重试前提。
 * - 409 修订冲突暂停自动写入并保留两份内容；DATA_EPOCH_CHANGED 阻止一切写入，不自动换世代重发。
 * - 草稿写入 IndexedDB 独立于网络保存，较短节流，写入失败如实上报。
 *
 * 本类不依赖 Vue / DOM，所有外部能力通过 deps 注入，便于单元测试。
 */
import { ApiError, isApiError } from '@/api/http'
import type { DocumentDetail, SaveDocumentBody, SaveResult } from '@/api/types'

export type SaveState = 'clean' | 'dirty' | 'saving' | 'retry_wait' | 'paused' | 'conflict' | 'blocked'
export type BlockedReason = 'epoch_changed' | 'deleted'
export type DraftStatus = 'idle' | 'pending' | 'saved' | 'error'
export type ServiceState = 'ok' | 'maintenance' | 'readonly'

export interface SaveError {
  code: string
  message: string
  retryable: boolean
  status: number
  requestId: string | null
  /** 本地校验错误（未发送请求） */
  local: boolean
}

export interface ConflictState {
  /** 服务端当前完整内容；获取失败时为 null */
  server: DocumentDetail | null
  serverRevision: number | null
  detectedAt: number
}

export interface SaveSnapshotView {
  state: SaveState
  dirty: boolean
  revision: number
  lastSavedAt: number | null
  retryAttempt: number
  maxRetries: number
  nextRetryAt: number | null
  error: SaveError | null
  conflict: ConflictState | null
  blocked: BlockedReason | null
  waitingForService: Exclude<ServiceState, 'ok'> | null
  autosaveSuspended: boolean
  draftStatus: DraftStatus
  draftSavedAt: number | null
  draftError: string | null
  draftUpToDate: boolean
}

export interface DraftPayload {
  title: string
  markdown: string
  baseRevision: number
  seq: number
}

export interface SaveControllerDeps {
  save(body: SaveDocumentBody): Promise<SaveResult>
  fetchDocument(): Promise<DocumentDetail>
  /** 从编辑器读取最新 Markdown；编辑器未就绪时返回 null */
  readEditor(): string | null
  writeDraft(draft: DraftPayload): Promise<void>
  /** 删除本会话草稿（仅当其编辑序号不大于 maxSeq） */
  deleteDraft(maxSeq: number): Promise<void>
  serviceState(): ServiceState
  /** 本地校验（标题与正文长度）；返回错误文案或 null */
  validate(title: string, markdown: string): string | null
  keepaliveSave?(body: SaveDocumentBody): void
  onSaved?(result: { revision: number; updatedAt: number; title: string; markdown: string }): void
  onEpochChanged?(): void
  now?(): number
}

export interface SaveControllerOptions {
  debounceMs: number
  maxIntervalMs: number
  retryDelaysMs: number[]
  draftDelayMs: number
}

export const DEFAULT_SAVE_OPTIONS: SaveControllerOptions = {
  debounceMs: 3000,
  maxIntervalMs: 30_000,
  retryDelaysMs: [2000, 4000, 8000, 16_000, 30_000],
  draftDelayMs: 800,
}

interface Snapshot {
  title: string
  markdown: string
  seq: number
}

type Trigger = 'auto' | 'manual' | 'retry' | 'flush'

type Timer = ReturnType<typeof setTimeout>

export class SaveController {
  private readonly deps: SaveControllerDeps
  private readonly opts: SaveControllerOptions

  private confirmed: { title: string; markdown: string; editorForm: string | null; revision: number }
  private current: { title: string; markdown: string }
  private seq = 0
  private editorPending = false
  private lastEditAt = 0
  private firstDirtyAt: number | null = null

  private debounceTimer: Timer | null = null
  private maxTimer: Timer | null = null
  private retryTimer: Timer | null = null
  private draftTimer: Timer | null = null

  private inFlight: Promise<void> | null = null
  private queuedManual: boolean | null = null
  /** 已发送但尚未得到确认的快照，用于识别“响应丢失但服务端已保存”的情形 */
  private unacked: Snapshot[] = []

  private state: SaveState = 'clean'
  private retryAttempt = 0
  private nextRetryAt: number | null = null
  private error: SaveError | null = null
  private conflict: ConflictState | null = null
  private blocked: BlockedReason | null = null
  private waitingForService: Exclude<ServiceState, 'ok'> | null = null
  private autosaveSuspended = false
  private lastSavedAt: number | null = null

  private draftChain: Promise<boolean> = Promise.resolve(true)
  private draftStatus: DraftStatus = 'idle'
  private draftSavedAt: number | null = null
  private draftSavedSeq = -1
  private draftError: string | null = null
  private readCostMs = 0

  private disposed = false
  private listeners = new Set<(view: SaveSnapshotView) => void>()

  constructor(
    deps: SaveControllerDeps,
    initial: { title: string; markdown: string; revision: number; updatedAt?: number | null },
    options: Partial<SaveControllerOptions> = {},
  ) {
    this.deps = deps
    this.opts = { ...DEFAULT_SAVE_OPTIONS, ...options }
    this.confirmed = { title: initial.title, markdown: initial.markdown, editorForm: null, revision: initial.revision }
    this.current = { title: initial.title, markdown: initial.markdown }
    this.lastSavedAt = null
  }

  // ---------------------------------------------------------------- 查询

  private now(): number {
    return this.deps.now ? this.deps.now() : Date.now()
  }

  get revision(): number {
    return this.confirmed.revision
  }

  getConfirmed(): { title: string; markdown: string; revision: number } {
    return { title: this.confirmed.title, markdown: this.confirmed.markdown, revision: this.confirmed.revision }
  }

  /** 当前本地内容（先从编辑器拉取最新值） */
  getLocalContent(): { title: string; markdown: string } {
    this.pullEditor(true)
    return { title: this.current.title, markdown: this.current.markdown }
  }

  private contentDirty(): boolean {
    if (this.current.title !== this.confirmed.title) return true
    const md = this.current.markdown
    return md !== this.confirmed.markdown && md !== this.confirmed.editorForm
  }

  isDirty(): boolean {
    return this.editorPending || this.contentDirty()
  }

  view(): SaveSnapshotView {
    return {
      state: this.state,
      dirty: this.isDirty(),
      revision: this.confirmed.revision,
      lastSavedAt: this.lastSavedAt,
      retryAttempt: this.retryAttempt,
      maxRetries: this.opts.retryDelaysMs.length,
      nextRetryAt: this.nextRetryAt,
      error: this.error,
      conflict: this.conflict,
      blocked: this.blocked,
      waitingForService: this.waitingForService,
      autosaveSuspended: this.autosaveSuspended,
      draftStatus: this.draftStatus,
      draftSavedAt: this.draftSavedAt,
      draftError: this.draftError,
      draftUpToDate: this.draftStatus === 'saved' && this.draftSavedSeq === this.seq && !this.editorPending,
    }
  }

  subscribe(listener: (view: SaveSnapshotView) => void): () => void {
    this.listeners.add(listener)
    listener(this.view())
    return () => this.listeners.delete(listener)
  }

  private emit(): void {
    if (this.disposed) return
    const view = this.view()
    for (const listener of this.listeners) listener(view)
  }

  // ---------------------------------------------------------------- 编辑输入

  /** 编辑器载入内容后的规范化形式（Vditor 可能调整 Markdown 书写），不视为用户修改 */
  setEditorBaseline(editorMarkdown: string): void {
    this.confirmed.editorForm = editorMarkdown
    if (!this.editorPending && this.current.markdown === this.confirmed.markdown) {
      this.current.markdown = editorMarkdown
    }
    this.reconcileClean()
    this.emit()
  }

  /** 编辑区原生 input 事件：内容可能已变化，但尚未取得 Markdown */
  notifyEditorInput(): void {
    if (this.disposed) return
    this.editorPending = true
    this.touch()
  }

  /** Vditor input 回调给出的 Markdown（编辑器内部约 800ms 防抖） */
  updateMarkdown(markdown: string): void {
    if (this.disposed) return
    const hadPending = this.editorPending
    this.editorPending = false
    if (markdown === this.current.markdown) {
      this.reconcileClean()
      this.emit()
      return
    }
    this.current.markdown = markdown
    if (hadPending) {
      // 原生输入事件已经计时，这里只更新内容
      this.afterContentChange()
    } else {
      this.touch()
    }
  }

  updateTitle(title: string): void {
    if (this.disposed || title === this.current.title) return
    this.current.title = title
    this.touch()
  }

  private touch(): void {
    const now = this.now()
    this.seq++
    this.lastEditAt = now
    if (this.firstDirtyAt === null) this.firstDirtyAt = now
    this.autosaveSuspended = false
    this.afterContentChange()
  }

  private afterContentChange(): void {
    if (this.state === 'clean' && this.isDirty()) this.state = 'dirty'
    if (this.state === 'dirty' && !this.isDirty()) {
      this.reconcileClean()
    } else {
      this.armAutosave()
      this.scheduleDraft()
    }
    this.emit()
  }

  private reconcileClean(): void {
    if (this.state !== 'dirty' && this.state !== 'clean') return
    if (this.isDirty()) return
    this.markClean()
  }

  private markClean(): void {
    this.state = 'clean'
    this.error = null
    this.nextRetryAt = null
    this.retryAttempt = 0
    this.firstDirtyAt = null
    this.autosaveSuspended = false
    this.clearAutosaveTimers()
    this.clearDraftTimer()
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    const seq = this.seq
    if (this.draftStatus !== 'idle' || this.draftSavedSeq >= 0) {
      this.draftChain = this.draftChain.then(async () => {
        try {
          await this.deps.deleteDraft(seq)
          if (this.seq === seq) {
            this.draftStatus = 'idle'
            this.draftSavedSeq = -1
            this.draftError = null
            this.emit()
          }
        } catch {
          // 删除冗余草稿失败不影响编辑；重新打开时会再次比较
        }
        return true
      })
    }
  }

  // ---------------------------------------------------------------- 计时器

  private clearAutosaveTimers(): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer)
    if (this.maxTimer) clearTimeout(this.maxTimer)
    this.debounceTimer = null
    this.maxTimer = null
  }

  private clearDraftTimer(): void {
    if (this.draftTimer) clearTimeout(this.draftTimer)
    this.draftTimer = null
  }

  private autosaveAllowed(): boolean {
    if (this.autosaveSuspended || this.disposed) return false
    if (this.state === 'dirty') return true
    // 本地校验或不可重试的服务端错误：用户修改内容后可再次尝试
    return this.state === 'paused' && this.error !== null && !this.error.retryable
  }

  private armAutosave(): void {
    if (!this.autosaveAllowed() || this.waitingForService) return
    const now = this.now()
    if (this.debounceTimer) clearTimeout(this.debounceTimer)
    this.debounceTimer = setTimeout(() => {
      this.debounceTimer = null
      void this.trigger('auto')
    }, Math.max(0, this.lastEditAt + this.opts.debounceMs - now))
    if (!this.maxTimer && this.firstDirtyAt !== null) {
      this.maxTimer = setTimeout(() => {
        this.maxTimer = null
        void this.trigger('auto')
      }, Math.max(0, this.firstDirtyAt + this.opts.maxIntervalMs - now))
    }
  }

  // ---------------------------------------------------------------- 保存

  private pullEditor(force = false): void {
    if (!this.editorPending && !force) return
    const start = typeof performance !== 'undefined' ? performance.now() : 0
    const markdown = this.deps.readEditor()
    if (typeof performance !== 'undefined') this.readCostMs = performance.now() - start
    if (markdown === null) return
    this.editorPending = false
    this.current.markdown = markdown
  }

  /** 手动保存（Ctrl+S）：立即请求，并要求服务端保存显式历史快照 */
  saveNow(): Promise<void> {
    this.autosaveSuspended = false
    return this.trigger('manual')
  }

  /** 手动重试：重置退避计数后立即保存 */
  retryNow(): Promise<void> {
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.retryAttempt = 0
    this.nextRetryAt = null
    this.waitingForService = null
    return this.trigger('manual')
  }

  /** 服务恢复（维护结束、重新连接）：暂停或等待中的保存继续 */
  resume(): void {
    if (this.disposed) return
    const waiting = this.waitingForService !== null
    this.waitingForService = null
    const resumable =
      waiting ||
      this.state === 'retry_wait' ||
      (this.state === 'paused' && (this.error === null || this.error.retryable))
    if (!resumable || !this.isDirty()) {
      this.emit()
      return
    }
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.retryAttempt = 0
    this.nextRetryAt = null
    void this.trigger('retry')
  }

  private async trigger(kind: Trigger): Promise<void> {
    if (this.disposed && kind !== 'flush') return
    if (this.state === 'blocked' || this.state === 'conflict') return
    if (this.inFlight) {
      this.queuedManual = (this.queuedManual ?? false) || kind === 'manual'
      return
    }
    if (kind === 'auto' && !this.autosaveAllowed()) return

    const service = this.deps.serviceState()
    if (service !== 'ok') {
      this.waitingForService = service
      this.clearAutosaveTimers()
      this.scheduleDraft()
      this.emit()
      return
    }
    this.waitingForService = null

    this.pullEditor(kind !== 'auto' || this.editorPending)
    if (!this.contentDirty()) {
      this.markClean()
      this.emit()
      return
    }

    const invalid = this.deps.validate(this.current.title, this.current.markdown)
    if (invalid) {
      this.clearAutosaveTimers()
      this.state = 'paused'
      this.error = { code: 'LOCAL_VALIDATION', message: invalid, retryable: false, status: 0, requestId: null, local: true }
      this.scheduleDraft()
      this.emit()
      return
    }

    const snapshot: Snapshot = { title: this.current.title, markdown: this.current.markdown, seq: this.seq }
    const manual = kind === 'manual' || this.queuedManual === true
    this.queuedManual = null
    this.clearAutosaveTimers()
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.nextRetryAt = null
    this.firstDirtyAt = null
    this.state = 'saving'
    this.unacked.push(snapshot)
    if (this.unacked.length > 8) this.unacked.shift()
    this.emit()

    const run = this.send(snapshot, manual)
    this.inFlight = run
    try {
      await run
    } finally {
      this.inFlight = null
    }
    if (this.queuedManual !== null && !this.disposed) {
      const queued = this.queuedManual
      this.queuedManual = null
      if (queued) await this.trigger('manual')
      else this.armAutosave()
    }
  }

  private async send(snapshot: Snapshot, manual: boolean): Promise<void> {
    const body: SaveDocumentBody = {
      title: snapshot.title,
      markdown: snapshot.markdown,
      expected_revision: this.confirmed.revision,
    }
    if (manual) body.snapshot = true
    try {
      const result = await this.deps.save(body)
      this.onSuccess(snapshot, result.revision || this.confirmed.revision + 1, result.updated_at)
    } catch (error) {
      await this.onFailure(snapshot, error)
    }
  }

  private onSuccess(snapshot: Snapshot, revision: number, updatedAt: number | null): void {
    this.unacked = this.unacked.filter((s) => s.seq > snapshot.seq)
    this.confirmed = { title: snapshot.title, markdown: snapshot.markdown, editorForm: snapshot.markdown, revision }
    this.lastSavedAt = updatedAt ?? this.now()
    this.retryAttempt = 0
    this.nextRetryAt = null
    this.error = null
    this.waitingForService = null
    this.deps.onSaved?.({ revision, updatedAt: this.lastSavedAt, title: snapshot.title, markdown: snapshot.markdown })

    if (this.disposed) {
      // 页面已切换：仅在没有更新的编辑时清理已提交的草稿
      if (this.seq === snapshot.seq && !this.editorPending) void this.deps.deleteDraft(snapshot.seq).catch(() => undefined)
      return
    }
    if (!this.isDirty()) {
      this.markClean()
    } else {
      this.state = 'dirty'
      // 剩余修改改以新 revision 为基线写入草稿
      void this.writeDraftNow()
      if (this.firstDirtyAt === null) this.firstDirtyAt = this.lastEditAt || this.now()
      if (this.queuedManual === null) this.armAutosave()
    }
    this.emit()
  }

  private async onFailure(snapshot: Snapshot, raw: unknown): Promise<void> {
    const error = isApiError(raw)
      ? raw
      : new ApiError({ status: 0, code: 'UNKNOWN_ERROR', message: raw instanceof Error ? raw.message : String(raw), kind: 'network' })

    if (error.code === 'DATA_EPOCH_CHANGED') {
      this.enterBlocked('epoch_changed', error)
      this.deps.onEpochChanged?.()
      return
    }

    if (error.status === 409 && (error.code === 'REVISION_CONFLICT' || error.code.startsWith('HTTP_409'))) {
      let server: DocumentDetail | null = null
      try {
        server = await this.deps.fetchDocument()
      } catch (fetchError) {
        if (isApiError(fetchError) && fetchError.status === 404) {
          this.enterBlocked('deleted', fetchError)
          return
        }
      }
      if (server) {
        const match = [...this.unacked].reverse().find((s) => s.title === server!.title && s.markdown === server!.markdown)
        if (match) {
          // 之前某次请求实际已提交，只是响应丢失
          this.onSuccess(match, server.revision, server.updated_at)
          return
        }
      }
      this.clearAutosaveTimers()
      this.state = 'conflict'
      this.conflict = {
        server,
        serverRevision: server?.revision ?? (typeof error.details.current_revision === 'number' ? error.details.current_revision : null),
        detectedAt: this.now(),
      }
      this.error = this.toSaveError(error)
      void this.writeDraftNow()
      this.emit()
      return
    }

    if (error.status === 404) {
      this.enterBlocked('deleted', error)
      return
    }

    if (error.retryable) {
      this.retryAttempt++
      const delays = this.opts.retryDelaysMs
      this.error = this.toSaveError(error)
      void this.writeDraftNow()
      if (this.retryAttempt > delays.length) {
        this.state = 'paused'
        this.nextRetryAt = null
        this.emit()
        return
      }
      const delay = delays[this.retryAttempt - 1]!
      this.state = 'retry_wait'
      this.nextRetryAt = this.now() + delay
      if (this.retryTimer) clearTimeout(this.retryTimer)
      this.retryTimer = setTimeout(() => {
        this.retryTimer = null
        void this.trigger('retry')
      }, delay)
      this.emit()
      return
    }

    this.state = 'paused'
    this.error = this.toSaveError(error)
    this.nextRetryAt = null
    void this.writeDraftNow()
    this.emit()
    void snapshot
  }

  private enterBlocked(reason: BlockedReason, error: ApiError): void {
    this.clearAutosaveTimers()
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.state = 'blocked'
    this.blocked = reason
    this.error = this.toSaveError(error)
    this.nextRetryAt = null
    void this.writeDraftNow()
    this.emit()
  }

  /** 外部检测到数据世代变化等情况：停止一切写入，本地内容写入草稿 */
  blockExternally(reason: BlockedReason): void {
    if (this.disposed || this.state === 'blocked') return
    this.clearAutosaveTimers()
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.state = 'blocked'
    this.blocked = reason
    this.nextRetryAt = null
    if (this.isDirty()) void this.writeDraftNow()
    this.emit()
  }

  private toSaveError(error: ApiError): SaveError {
    return {
      code: error.code,
      message: error.message,
      retryable: error.retryable,
      status: error.status,
      requestId: error.requestId,
      local: false,
    }
  }

  /**
   * 切换文档、阅读状态或历史恢复前调用：尽量完成保存。
   * 返回 true 表示服务端已确认最新内容；false 时由调用方提供“保持编辑 / 保留草稿后切换 / 导出草稿”。
   */
  async flush(timeoutMs = 15_000): Promise<boolean> {
    const deadline = this.now() + timeoutMs
    for (let round = 0; round < 4; round++) {
      if (this.state === 'blocked' || this.state === 'conflict') return false
      if (this.inFlight) {
        const finished = await Promise.race([
          this.inFlight.then(() => true),
          new Promise<boolean>((resolve) => setTimeout(() => resolve(false), Math.max(0, deadline - this.now()))),
        ])
        if (!finished) return false
        continue
      }
      this.pullEditor(true)
      if (!this.contentDirty()) {
        this.markClean()
        this.emit()
        return true
      }
      if (this.deps.serviceState() !== 'ok') {
        this.waitingForService = this.deps.serviceState() as Exclude<ServiceState, 'ok'>
        this.emit()
        return false
      }
      if (this.retryTimer) clearTimeout(this.retryTimer)
      this.retryTimer = null
      await this.trigger('flush')
      if (this.state !== 'clean' && this.state !== 'dirty') return false
      if (this.now() > deadline) return false
    }
    return !this.isDirty()
  }

  // ---------------------------------------------------------------- 草稿

  private scheduleDraft(): void {
    if (this.draftTimer || this.disposed) return
    // 正文很大时 getValue 较慢，按读取耗时放宽节流
    const delay = Math.min(5000, Math.max(this.opts.draftDelayMs, this.readCostMs * 20))
    this.draftTimer = setTimeout(() => {
      this.draftTimer = null
      void this.writeDraftNow()
    }, delay)
  }

  /** 立即写入草稿；返回 IndexedDB 是否已持久化最新内容 */
  writeDraftNow(): Promise<boolean> {
    this.clearDraftTimer()
    this.draftChain = this.draftChain.then(() => this.doWriteDraft())
    return this.draftChain
  }

  private async doWriteDraft(): Promise<boolean> {
    if (!this.disposed) this.pullEditor()
    if (!this.isDirty() && this.state !== 'conflict' && this.state !== 'blocked') return true
    const payload: DraftPayload = {
      title: this.current.title,
      markdown: this.current.markdown,
      baseRevision: this.confirmed.revision,
      seq: this.seq,
    }
    this.draftStatus = 'pending'
    this.emit()
    try {
      await this.deps.writeDraft(payload)
      this.draftStatus = 'saved'
      this.draftSavedSeq = payload.seq
      this.draftSavedAt = this.now()
      this.draftError = null
      this.emit()
      return true
    } catch (error) {
      this.draftStatus = 'error'
      this.draftError = error instanceof Error ? error.message : String(error)
      this.emit()
      return false
    }
  }

  /** “保留本地草稿后切换”：仅在草稿确实写入成功时返回 true */
  async persistDraftNow(): Promise<boolean> {
    this.pullEditor(true)
    if (!this.isDirty() && this.state !== 'conflict' && this.state !== 'blocked') return true
    const ok = await this.writeDraftNow()
    return ok && this.draftSavedSeq === this.seq
  }

  // ---------------------------------------------------------------- 冲突、草稿恢复、历史恢复

  /** 使用服务端版本（丢弃本地修改）；调用方负责同步编辑器内容 */
  acceptServer(server: { title: string; markdown: string; revision: number; updated_at?: number | null }): void {
    this.clearAutosaveTimers()
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.confirmed = { title: server.title, markdown: server.markdown, editorForm: null, revision: server.revision }
    this.current = { title: server.title, markdown: server.markdown }
    this.editorPending = false
    this.unacked = []
    this.conflict = null
    this.blocked = null
    this.error = null
    this.retryAttempt = 0
    this.nextRetryAt = null
    this.lastSavedAt = server.updated_at ?? this.lastSavedAt
    this.seq++
    this.state = 'dirty'
    this.markClean()
    this.emit()
  }

  /**
   * 提交人工合并结果：先写入本地草稿，再以最新远端修订为基线保存；再次冲突会重新进入冲突状态。
   * 调用方负责先把合并后的正文写入编辑器。
   */
  async applyMerged(merged: { title: string; markdown: string }, server: { title: string; markdown: string; revision: number }): Promise<void> {
    this.confirmed = { title: server.title, markdown: server.markdown, editorForm: null, revision: server.revision }
    this.current = { title: merged.title, markdown: merged.markdown }
    this.editorPending = false
    this.unacked = []
    this.conflict = null
    this.error = null
    this.retryAttempt = 0
    this.seq++
    this.lastEditAt = this.now()
    this.state = this.contentDirty() ? 'dirty' : 'clean'
    this.emit()
    if (this.state === 'clean') {
      this.markClean()
      this.emit()
      return
    }
    await this.writeDraftNow()
    await this.trigger('manual')
  }

  /** 草稿恢复到编辑器：标记为有修改，但在用户继续编辑或手动保存前不自动覆盖服务端 */
  restoreDraft(draft: { title: string; markdown: string }): void {
    this.current = { title: draft.title, markdown: draft.markdown }
    this.editorPending = false
    this.seq++
    this.lastEditAt = this.now()
    this.state = this.contentDirty() ? 'dirty' : 'clean'
    this.autosaveSuspended = this.state === 'dirty'
    this.clearAutosaveTimers()
    if (this.state === 'dirty') void this.writeDraftNow()
    this.emit()
  }

  /** 基于新的服务端修订继续（例如草稿基础修订不匹配、比较后选择保留本地内容） */
  rebase(server: { title: string; markdown: string; revision: number }): void {
    this.confirmed = { title: server.title, markdown: server.markdown, editorForm: null, revision: server.revision }
    this.unacked = []
    this.state = this.isDirty() ? 'dirty' : 'clean'
    if (this.state === 'clean') this.markClean()
    this.emit()
  }

  /** 历史恢复成功后，服务端内容成为新的已确认版本 */
  afterExternalWrite(doc: { title: string; markdown: string; revision: number; updated_at?: number | null }): void {
    this.acceptServer(doc)
  }

  /** 页面卸载时尽力保存；不改变状态，不清理草稿，不显示成功 */
  keepalive(): void {
    if (this.disposed || !this.deps.keepaliveSave) return
    if (this.state === 'blocked' || this.state === 'conflict') return
    if (this.deps.serviceState() !== 'ok') return
    this.pullEditor(true)
    if (!this.contentDirty() || this.deps.validate(this.current.title, this.current.markdown)) return
    this.deps.keepaliveSave({
      title: this.current.title,
      markdown: this.current.markdown,
      expected_revision: this.confirmed.revision,
    })
  }

  /** 停止计时与界面通知；在途请求继续完成，成功时清理已确认的草稿 */
  dispose(): void {
    if (this.disposed) return
    if (this.isDirty() && this.draftTimer) void this.writeDraftNow()
    this.disposed = true
    this.clearAutosaveTimers()
    this.clearDraftTimer()
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.listeners.clear()
  }
}
