<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '@/components/base/AppIcon.vue'
import DropdownMenu, { type MenuItem } from '@/components/base/DropdownMenu.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import HighlightedText from '@/components/base/HighlightedText.vue'
import ExportDialog from '@/components/ExportDialog.vue'
import MarkdownEditor from './MarkdownEditor.vue'
import MarkdownViewer from './MarkdownViewer.vue'
import OutlinePanel from './OutlinePanel.vue'
import SaveStatus from './SaveStatus.vue'
import DraftRecoveryDialog from './DraftRecoveryDialog.vue'
import MergeDialog from './MergeDialog.vue'
import HistoryDialog from './HistoryDialog.vue'
import LinkPickerDialog from './LinkPickerDialog.vue'
import UploadTray from './UploadTray.vue'
import { api } from '@/api'
import { ApiError, isApiError } from '@/api/http'
import type { DocumentDetail, HistoryDetail, TreeNode } from '@/api/types'
import { SaveController, type SaveSnapshotView } from '@/services/saveController'
import { draftKey, draftStore, type DraftRecord } from '@/services/drafts'
import { editorSessionId, newEditSessionId } from '@/services/session'
import { tabBus } from '@/services/broadcast'
import { addLeaveGuard } from '@/router/leaveGuard'
import { choose, confirmDialog } from '@/composables/useDialogs'
import { activeHeadingIndex, collectOutline, focusHeading, type OutlineItem } from '@/editor/outline'
import { countSourceMatches } from '@/editor/searchHighlight'
import { useUiStore } from '@/stores/ui'
import { useHealthStore } from '@/stores/health'
import { useTreeStore } from '@/stores/tree'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useDraftsStore } from '@/stores/drafts'
import { useDocStatusStore } from '@/stores/docStatus'
import { useUploadStore, type UploadTask } from '@/stores/uploads'
import { exportDraftMarkdown } from '@/utils/download'
import { formatBytes, formatDateTime, formatFullTime } from '@/utils/format'
import { documentPath, markdownDocLink } from '@/utils/links'
import { codePoints, findTermRanges, splitSearchTerms, utf8Bytes, validateTitle } from '@/utils/text'

const props = defineProps<{
  docId: string
  searchQuery: string | null
  initialMode: 'read' | 'edit'
}>()

const emit = defineEmits<{
  (e: 'loaded', doc: DocumentDetail): void
  (e: 'missing'): void
  (e: 'create', parentId: string | null): void
  (e: 'delete'): void
}>()

const router = useRouter()
const ui = useUiStore()
const health = useHealthStore()
const tree = useTreeStore()
const kbs = useKnowledgeBaseStore()
const draftsStore = useDraftsStore()
const docStatus = useDocStatusStore()
const uploads = useUploadStore()

type LeaveResult = 'clean' | 'saved' | 'kept-draft' | false

// ------------------------------------------------------------------ 文档状态

const doc = ref<DocumentDetail | null>(null)
const loading = ref(true)
const loadError = ref<unknown>(null)
const missing = ref<{ trashed: boolean; batchId: string | null; drafts: number } | null>(null)
const mode = ref<'read' | 'edit'>(props.initialMode)
const switching = ref(false)

const editSessionId = ref<string | null>(null)
const controller = shallowRef<SaveController | null>(null)
const saveView = ref<SaveSnapshotView | null>(null)
const editorRef = ref<InstanceType<typeof MarkdownEditor> | null>(null)
const viewerRef = ref<InstanceType<typeof MarkdownViewer> | null>(null)
const scrollEl = ref<HTMLElement | null>(null)
const titleEl = ref<HTMLInputElement | null>(null)
const editorReady = ref(false)
const editorInitial = ref('')
const titleInput = ref('')
let unsubscribe: (() => void) | null = null
let skipBaseline = false
let focusTitleOnReady = false
let draftAnnounced = false

const pendingDrafts = ref<DraftRecord[]>([])
const draftDialogOpen = ref(false)
const draftBusyKey = ref<string | null>(null)

const merge = reactive({
  open: false,
  mode: 'conflict' as 'conflict' | 'draft',
  local: { title: '', markdown: '', label: '本地内容', time: null as number | null },
  server: null as DocumentDetail | null,
  serverError: null as string | null,
  draft: null as DraftRecord | null,
  busy: false,
})

const historyOpen = ref(false)
const restoringHistory = ref(false)
const linkPickerOpen = ref(false)
const exportOpen = ref(false)
const sourceOpen = ref(false)

const outline = shallowRef<OutlineItem[]>([])
const activeOutline = ref(-1)
const hitCount = ref(0)
const hitIndex = ref(0)
const rendered = ref(false)

const othersEditing = ref(0)
const remoteNewer = ref<{ revision: number; title: string } | null>(null)

const kbId = computed(() => doc.value?.knowledge_base_id ?? null)
const kb = computed(() => kbs.byId(kbId.value))
const theme = computed(() => ui.resolvedTheme)
const terms = computed(() => (props.searchQuery ? splitSearchTerms(props.searchQuery) : []))
const ancestors = computed(() => (kbId.value ? tree.ancestors(kbId.value, props.docId) : []))
const editorLocked = computed(
  () => health.epochChanged || saveView.value?.state === 'blocked' || health.maintenance?.kind === 'restore',
)
const titleError = computed(() => (saveView.value?.error?.local ? saveView.value.error.message : ''))
const sourceMatches = computed(() =>
  doc.value && terms.value.length ? countSourceMatches(`${doc.value.title}\n${doc.value.markdown}`, terms.value) : 0,
)

// ------------------------------------------------------------------ 加载

async function listDrafts(): Promise<DraftRecord[]> {
  if (draftsStore.available === null) await draftsStore.probe()
  if (!health.instanceId) await health.refresh()
  if (!draftsStore.available || !health.instanceId) return []
  try {
    return await draftStore.listForDocument(health.instanceId, props.docId)
  } catch {
    return []
  }
}

/** 过滤出需要用户处理的草稿：内容与服务端一致的冗余草稿直接清理；仍在其他标签页编辑的不打扰 */
async function classifyDrafts(server: DocumentDetail, list?: DraftRecord[]) {
  if (!list) {
    await tabBus.ping(health.instanceId)
    list = await listDrafts()
  }
  const pending: DraftRecord[] = []
  let removed = false
  for (const draft of list) {
    if (draft.editor_session_id === editSessionId.value) continue
    if (draft.tab_session_id !== editorSessionId && tabBus.isSessionAlive(draft.tab_session_id)) continue
    if (draft.title === server.title && draft.markdown === server.markdown) {
      await draftStore.delete(draft.key).catch(() => undefined)
      removed = true
      continue
    }
    pending.push(draft)
  }
  pendingDrafts.value = pending
  if (removed) draftsStore.notifyChanged()
  if (pending.length > 0 && !draftAnnounced) {
    draftAnnounced = true
    draftDialogOpen.value = true
  }
}

function parseMissing(error: ApiError) {
  const d = error.details
  const trashed =
    d.deleted === true || d.in_trash === true || d.state === 'trashed' || d.state === 'deleted' || typeof d.batch_id === 'string' || typeof d.deleted_at === 'number'
  return { trashed, batchId: typeof d.batch_id === 'string' ? d.batch_id : null }
}

async function load() {
  loading.value = true
  loadError.value = null
  missing.value = null
  try {
    await tabBus.ping(health.instanceId, 200)
    const [server, list] = await Promise.all([api.documents.get(props.docId), listDrafts()])
    doc.value = server
    document.title = `${server.title || '无标题'} - OpenLJMDB`
    emit('loaded', server)
    await classifyDrafts(server, list)
    if (mode.value === 'edit') startEdit(null)
  } catch (error) {
    if (isApiError(error) && error.status === 404) {
      const drafts = await listDrafts()
      missing.value = { ...parseMissing(error), drafts: drafts.length }
      document.title = '文档不可用 - OpenLJMDB'
      emit('missing')
    } else {
      loadError.value = error
    }
  } finally {
    loading.value = false
  }
}

// ------------------------------------------------------------------ 编辑会话

function validate(title: string, markdown: string): string | null {
  const limits = health.limits
  const titleProblem = validateTitle(title, limits.title_max_chars)
  if (titleProblem) return titleProblem
  // 粗略判断后再精确计算，避免每次保存都编码大正文
  if (markdown.length * 3 > limits.markdown_max_bytes && utf8Bytes(markdown) > limits.markdown_max_bytes) {
    return `正文超过 ${formatBytes(limits.markdown_max_bytes)} 上限，未保存（正文不会被截断）`
  }
  return null
}

function startEdit(restore: { title: string; markdown: string } | null) {
  const current = doc.value
  if (!current || controller.value) return
  const sessionId = newEditSessionId()
  editSessionId.value = sessionId
  const key = draftKey(health.instanceId, props.docId, sessionId)
  const createdAt = Date.now()
  let draftExists = false

  const ctrl = new SaveController(
    {
      save: (body) => api.documents.save(props.docId, body),
      fetchDocument: () => api.documents.get(props.docId),
      readEditor: () => (editorReady.value && editorRef.value ? editorRef.value.getValue() : null),
      writeDraft: async (draft) => {
        if (draftsStore.available === null) await draftsStore.probe()
        if (!draftsStore.available) throw new Error(draftsStore.errorMessage ?? '浏览器本地存储不可用')
        try {
          await draftStore.put({
            key,
            instance_id: health.instanceId,
            document_id: props.docId,
            editor_session_id: sessionId,
            tab_session_id: editorSessionId,
            knowledge_base_id: kbId.value,
            base_data_epoch: health.pageEpoch ?? '',
            base_revision: draft.baseRevision,
            title: draft.title,
            markdown: draft.markdown,
            local_seq: draft.seq,
            created_at: createdAt,
            updated_at: Date.now(),
          })
          draftsStore.clearWriteError()
        } catch (error) {
          draftsStore.reportWriteError(error instanceof Error ? error.message : String(error))
          throw error
        }
        if (!draftExists) {
          draftExists = true
          draftsStore.notifyChanged()
        }
      },
      deleteDraft: async (maxSeq) => {
        if (!draftsStore.available) return
        await draftStore.deleteIfSeqAtMost(key, maxSeq)
        if (draftExists) {
          draftExists = false
          draftsStore.notifyChanged()
        }
      },
      serviceState: () => health.serviceState,
      validate,
      keepaliveSave: (body) => {
        // 浏览器对 keepalive 请求体有约 64KB 限制；只作为尽力保存
        if (JSON.stringify(body).length > 60_000) return
        void api.documents.save(props.docId, body, { keepalive: true }).catch(() => undefined)
      },
      onSaved: ({ revision, updatedAt, title, markdown }) => {
        if (doc.value) doc.value = { ...doc.value, title, markdown, revision, updated_at: updatedAt }
        if (kbId.value) {
          tree.setTitle(kbId.value, props.docId, title)
          kbs.touch(kbId.value, { updated_at: updatedAt })
        }
        if (remoteNewer.value && remoteNewer.value.revision <= revision) remoteNewer.value = null
        document.title = `${title || '无标题'} - OpenLJMDB`
        tabBus.post({
          type: 'saved',
          instance_id: health.instanceId,
          doc_id: props.docId,
          session_id: editorSessionId,
          revision,
          title,
        })
      },
      onEpochChanged: () => health.markEpochChanged(),
    },
    { title: current.title, markdown: current.markdown, revision: current.revision, updatedAt: current.updated_at },
  )
  controller.value = ctrl
  unsubscribe = ctrl.subscribe((view) => {
    saveView.value = view
    docStatus.set(props.docId, { state: view.state, dirty: view.dirty })
    if (view.state === 'conflict' && merge.open && merge.mode === 'conflict' && view.conflict?.server) {
      merge.server = view.conflict.server
    }
  })
  uploads.registerOwner(sessionId, insertUpload)
  titleInput.value = restore ? restore.title : current.title
  editorInitial.value = restore ? restore.markdown : current.markdown
  editorReady.value = false
  skipBaseline = !!restore
  if (restore) ctrl.restoreDraft(restore)
  mode.value = 'edit'
  if (health.epochChanged) ctrl.blockExternally('epoch_changed')
  postPresence()
}

function teardownEdit() {
  const ctrl = controller.value
  if (ctrl && doc.value) {
    const confirmed = ctrl.getConfirmed()
    doc.value = { ...doc.value, title: confirmed.title, markdown: confirmed.markdown, revision: confirmed.revision }
  }
  ctrl?.dispose()
  unsubscribe?.()
  unsubscribe = null
  controller.value = null
  saveView.value = null
  docStatus.set(props.docId, null)
  if (editSessionId.value) uploads.unregisterOwner(editSessionId.value)
  editSessionId.value = null
  editorReady.value = false
}

function onEditorReady() {
  editorReady.value = true
  const ctrl = controller.value
  const value = editorRef.value?.getValue()
  if (ctrl && value !== null && value !== undefined && !skipBaseline) ctrl.setEditorBaseline(value)
  skipBaseline = false
  refreshEditorOutline()
  if (focusTitleOnReady) {
    focusTitleOnReady = false
    void nextTick(() => {
      titleEl.value?.focus()
      titleEl.value?.select()
    })
  }
}

function onTitleInput() {
  controller.value?.updateTitle(titleInput.value)
}

function onTitleEnter() {
  editorRef.value?.focus()
}

/** 程序化替换编辑器内容（远端版本 / 合并结果），并同步标题 */
function setEditorContent(title: string, markdown: string, clearStack = true) {
  titleInput.value = title
  editorRef.value?.setValue(markdown, clearStack)
}

// ------------------------------------------------------------------ 模式切换与离开

function leaveMessage(view: SaveSnapshotView): string {
  if (view.state === 'conflict') return '此文档存在尚未处理的版本冲突，你的修改还没有写入数据库。'
  if (view.state === 'blocked') {
    return view.blocked === 'epoch_changed'
      ? '数据已被恢复或替换，你的修改无法再写入当前数据。'
      : '此文档已被删除，你的修改无法保存到原文档。'
  }
  if (view.waitingForService) return '服务正在维护或不可写，你的修改还没有写入数据库。'
  return `最新修改尚未保存到服务${view.error ? `（${view.error.message}）` : ''}。`
}

/**
 * 离开当前编辑上下文前：检查上传任务、完成最新保存；
 * 失败时提供“保持编辑 / 导出草稿 / 保留本地草稿后切换”，只有草稿确实写入成功才允许切换。
 */
async function prepareLeave(): Promise<LeaveResult> {
  const owner = editSessionId.value
  if (owner) {
    const active = uploads.activeCount(props.docId)
    if (active > 0) {
      const key = await choose({
        title: '附件仍在上传',
        message: `有 ${active} 个附件正在上传。离开后这些上传会被取消，正文中不会插入它们的链接。`,
        cancelKey: 'stay',
        tone: 'warning',
        actions: [
          { key: 'stay', label: '继续等待上传', autofocus: true },
          { key: 'leave', label: '取消上传并离开', kind: 'danger' },
        ],
      })
      if (key !== 'leave') return false
      uploads.cancelForOwner(owner)
    }
  }
  const ctrl = controller.value
  if (!ctrl) return 'clean'
  let view = ctrl.view()
  if (view.state === 'clean' && !view.dirty) return 'clean'
  if (view.state !== 'conflict' && view.state !== 'blocked') {
    ui.announce('正在保存最新修改')
    if (await ctrl.flush(15_000)) return 'saved'
    view = ctrl.view()
  }
  for (;;) {
    const actions = [
      { key: 'stay', label: '保持编辑', autofocus: true },
      { key: 'export', label: '导出草稿' },
      { key: 'keep', label: '保留本地草稿后切换', kind: 'primary' as const },
    ]
    if (view.state === 'conflict') actions.splice(1, 0, { key: 'resolve', label: '处理冲突' })
    const key = await choose({
      title: '修改尚未保存到服务',
      message: `${leaveMessage(view)}\n你可以留在当前页面继续编辑，或先把内容保留为浏览器本地草稿再离开（重新打开文档时可恢复）。`,
      cancelKey: 'stay',
      tone: 'warning',
      actions,
    })
    if (key === 'stay') return false
    if (key === 'resolve') {
      void openConflict()
      return false
    }
    if (key === 'export') {
      exportLocal()
      continue
    }
    const persisted = await ctrl.persistDraftNow()
    if (persisted) {
      draftsStore.notifyChanged()
      return 'kept-draft'
    }
    ui.toast({
      kind: 'error',
      message: '本地草稿写入失败，为避免丢失内容已保持编辑',
      detail: ctrl.view().draftError ?? undefined,
    })
    return false
  }
}

async function enterEdit() {
  if (mode.value === 'edit' || !doc.value || switching.value) return
  if (health.epochChanged) {
    ui.toast({ kind: 'warning', message: '数据已被恢复或替换，请重新加载页面后再编辑' })
    return
  }
  switching.value = true
  try {
    await classifyDrafts(doc.value)
    startEdit(null)
  } finally {
    switching.value = false
  }
}

async function enterRead() {
  if (mode.value === 'read' || switching.value) return
  switching.value = true
  try {
    const result = await prepareLeave()
    if (result === false) return
    teardownEdit()
    mode.value = 'read'
    if (result === 'kept-draft' && doc.value) {
      draftAnnounced = true
      await classifyDrafts(doc.value)
    }
    postPresence()
  } finally {
    switching.value = false
  }
}

// ------------------------------------------------------------------ 草稿处理

function draftLocal(draft: DraftRecord) {
  return { title: draft.title, markdown: draft.markdown, label: '本地草稿', time: draft.updated_at }
}

async function removeDraftRecord(draft: DraftRecord) {
  await draftStore.delete(draft.key).catch(() => undefined)
  pendingDrafts.value = pendingDrafts.value.filter((d) => d.key !== draft.key)
  draftsStore.notifyChanged()
  if (pendingDrafts.value.length === 0) draftDialogOpen.value = false
}

async function restoreDraft(draft: DraftRecord) {
  draftBusyKey.value = draft.key
  try {
    if (mode.value === 'edit' && controller.value && editorReady.value) {
      setEditorContent(draft.title, draft.markdown, false)
      controller.value.restoreDraft({ title: draft.title, markdown: editorRef.value?.getValue() ?? draft.markdown })
    } else {
      teardownEdit()
      startEdit({ title: draft.title, markdown: draft.markdown })
    }
    const persisted = (await controller.value?.persistDraftNow()) ?? false
    // 新会话已写入同样内容后再删除旧草稿；写入失败则保留旧草稿
    if (persisted) await removeDraftRecord(draft)
    else pendingDrafts.value = pendingDrafts.value.filter((d) => d.key !== draft.key)
    draftDialogOpen.value = false
    ui.toast({ kind: 'info', message: '已恢复本地草稿到编辑器，尚未保存到服务。继续编辑会自动保存，或点击“保存”。' })
  } finally {
    draftBusyKey.value = null
  }
}

async function compareDraft(draft: DraftRecord) {
  draftBusyKey.value = draft.key
  try {
    let server: DocumentDetail | null = null
    let serverError: string | null = null
    try {
      server = await api.documents.get(props.docId)
      doc.value = mode.value === 'read' ? server : doc.value
    } catch (e) {
      serverError = e instanceof Error ? e.message : String(e)
    }
    Object.assign(merge, { open: true, mode: 'draft', local: draftLocal(draft), server, serverError, draft, busy: false })
    draftDialogOpen.value = false
  } finally {
    draftBusyKey.value = null
  }
}

async function discardDraft(draft: DraftRecord) {
  const ok = await confirmDialog({
    title: '放弃本地草稿',
    message: `将删除保存于 ${formatFullTime(draft.updated_at)} 的本地草稿（约 ${codePoints(draft.markdown).length} 字）。服务端版本不受影响，此操作无法撤销。`,
    confirmText: '放弃草稿',
    danger: true,
  })
  if (ok) await removeDraftRecord(draft)
}

function siblingParent(): string | null {
  const current = doc.value
  if (!current?.parent_id || !kbId.value) return null
  return tree.node(kbId.value, current.parent_id) ? current.parent_id : null
}

/** 另存为新文档：在同一知识库中与原文档同级新建 */
async function saveAsNew(content: { title: string; markdown: string }): Promise<string | null> {
  if (!kbId.value) return null
  const max = health.limits.title_max_chars
  const suffix = '（本地副本）'
  const base = codePoints(content.title.trim() || '无标题').slice(0, Math.max(1, max - suffix.length)).join('')
  try {
    const created = await tree.createDocument(kbId.value, siblingParent(), `${base}${suffix}`, content.markdown)
    ui.toast({
      kind: 'success',
      message: `已另存为新文档《${created.title}》`,
      action: { label: '打开', handler: () => void router.push(documentPath(created.id)) },
    })
    return created.id
  } catch (error) {
    ui.toast({ kind: 'error', message: '另存为新文档失败，本地内容仍保留', detail: error instanceof Error ? error.message : String(error) })
    return null
  }
}

async function saveDraftAsNew(draft: DraftRecord) {
  draftBusyKey.value = draft.key
  try {
    if (await saveAsNew(draft)) await removeDraftRecord(draft)
  } finally {
    draftBusyKey.value = null
  }
}

// ------------------------------------------------------------------ 冲突处理

async function openConflict() {
  const ctrl = controller.value
  if (!ctrl) return
  const local = ctrl.getLocalContent()
  let server = ctrl.view().conflict?.server ?? null
  let serverError: string | null = null
  if (!server) {
    try {
      server = await api.documents.get(props.docId)
    } catch (e) {
      serverError = e instanceof Error ? e.message : String(e)
    }
  }
  Object.assign(merge, {
    open: true,
    mode: 'conflict',
    local: { ...local, label: '本地内容', time: Date.now() },
    server,
    serverError,
    draft: null,
    busy: false,
  })
}

async function refetchMergeServer() {
  try {
    merge.server = await api.documents.get(props.docId)
    merge.serverError = null
  } catch (e) {
    merge.serverError = e instanceof Error ? e.message : String(e)
  }
}

function applyServerToEditor(server: DocumentDetail) {
  const ctrl = controller.value
  if (!ctrl) return
  ctrl.acceptServer(server)
  setEditorContent(server.title, server.markdown, true)
  const value = editorRef.value?.getValue()
  if (value !== null && value !== undefined) ctrl.setEditorBaseline(value)
  doc.value = server
  remoteNewer.value = null
}

async function mergeUseServer() {
  const server = merge.server
  if (!server) return
  const ok = await confirmDialog({
    title: '使用服务端版本',
    message:
      merge.mode === 'draft'
        ? '将删除这份本地草稿，保留服务端当前内容。此操作无法撤销。'
        : '将丢弃编辑器中尚未保存的本地修改（包括对应的本地草稿），改为显示服务端当前内容。此操作无法撤销。',
    confirmText: '使用服务端版本',
    danger: true,
  })
  if (!ok) return
  if (merge.mode === 'draft' && merge.draft) {
    await removeDraftRecord(merge.draft)
    if (mode.value === 'read') doc.value = server
  } else {
    applyServerToEditor(server)
  }
  merge.open = false
}

async function mergeSaveAsNew() {
  merge.busy = true
  try {
    const id = await saveAsNew({ title: merge.local.title, markdown: merge.local.markdown })
    if (!id) return
    if (merge.mode === 'draft' && merge.draft) await removeDraftRecord(merge.draft)
    else if (merge.server) applyServerToEditor(merge.server)
    merge.open = false
  } finally {
    merge.busy = false
  }
}

async function mergeSubmit(merged: { title: string; markdown: string }) {
  let server = merge.server
  if (!server) return
  merge.busy = true
  try {
    // 以最新远端修订为基线（期间若又有新的保存，会再次进入冲突）
    try {
      server = await api.documents.get(props.docId)
    } catch {
      // 使用已获取的版本；服务端会再次校验修订
    }
    if (mode.value !== 'edit' || !controller.value) {
      teardownEdit()
      doc.value = server
      startEdit(null)
      await waitForEditor()
    }
    const ctrl = controller.value
    if (!ctrl) return
    setEditorContent(merged.title, merged.markdown, false)
    const oldDraft = merge.mode === 'draft' ? merge.draft : null
    merge.open = false
    await ctrl.applyMerged({ title: merged.title, markdown: editorRef.value?.getValue() ?? merged.markdown }, server)
    if (oldDraft) await removeDraftRecord(oldDraft)
    const view = ctrl.view()
    if (view.state === 'clean') ui.toast({ kind: 'success', message: '合并结果已保存' })
    else if (view.state === 'conflict') {
      ui.toast({ kind: 'warning', message: '保存期间文档又被更新，请再次比较' })
      void openConflict()
    }
  } finally {
    merge.busy = false
  }
}

function waitForEditor(timeoutMs = 10_000): Promise<boolean> {
  if (editorReady.value) return Promise.resolve(true)
  return new Promise((resolve) => {
    const stop = watch(editorReady, (ready) => {
      if (ready) {
        stop()
        resolve(true)
      }
    })
    setTimeout(() => {
      stop()
      resolve(editorReady.value)
    }, timeoutMs)
  })
}

// ------------------------------------------------------------------ 其他操作

function reloadPage() {
  window.location.reload()
}

async function saveDeletedAsNew() {
  const ctrl = controller.value
  if (!ctrl) return
  const id = await saveAsNew(ctrl.getLocalContent())
  if (id) {
    leaveApproved = true
    await router.push(documentPath(id))
  }
}

function exportLocal() {
  const local = controller.value?.getLocalContent() ?? { title: titleInput.value, markdown: doc.value?.markdown ?? '' }
  exportDraftMarkdown(local.title, local.markdown)
}

async function reloadFromServer() {
  try {
    const server = await api.documents.get(props.docId)
    if (mode.value === 'edit' && controller.value) {
      if (controller.value.isDirty()) {
        merge.local = { ...controller.value.getLocalContent(), label: '本地内容', time: Date.now() }
        merge.server = server
        merge.mode = 'conflict'
        merge.draft = null
        merge.serverError = null
        merge.open = true
        return
      }
      applyServerToEditor(server)
    } else {
      doc.value = server
    }
    remoteNewer.value = null
  } catch (error) {
    ui.toast({ kind: 'error', message: '载入最新版本失败', detail: error instanceof Error ? error.message : String(error) })
  }
}

async function restoreHistory(item: HistoryDetail) {
  const ok = await confirmDialog({
    title: '恢复历史版本',
    message: `将以 ${formatFullTime(item.created_at)} 的版本（修订 r${item.revision}）创建新的当前修订。恢复前会先保存当前内容，恢复前的版本会保留在历史中；文档 ID、所在知识库和树位置不变。`,
    confirmText: '恢复此版本',
  })
  if (!ok) return
  const wasEditing = mode.value === 'edit'
  const leave = await prepareLeave()
  if (leave === false) return
  if (leave === 'kept-draft') teardownEdit()
  restoringHistory.value = true
  try {
    const expected = controller.value?.revision ?? doc.value?.revision ?? 1
    await api.documents.restoreHistory(props.docId, item.id, expected)
    const fresh = await api.documents.get(props.docId)
    if (controller.value) applyServerToEditor(fresh)
    else {
      doc.value = fresh
      if (wasEditing && leave === 'kept-draft') {
        startEdit(null)
        await classifyDrafts(fresh)
      }
    }
    if (kbId.value) tree.setTitle(kbId.value, props.docId, fresh.title)
    historyOpen.value = false
    tabBus.post({ type: 'saved', instance_id: health.instanceId, doc_id: props.docId, session_id: editorSessionId, revision: fresh.revision, title: fresh.title })
    ui.toast({
      kind: 'success',
      message: `已恢复到 ${formatFullTime(item.created_at)} 的版本，当前修订 r${fresh.revision}`,
      detail: '恢复前的内容已保留在历史中',
    })
  } catch (error) {
    const conflict = isApiError(error) && error.code === 'REVISION_CONFLICT'
    ui.toast({
      kind: 'error',
      message: conflict ? '文档已在其他页面更新，请载入最新版本后再恢复' : '恢复历史版本失败，当前内容未改变',
      detail: error instanceof Error ? error.message : String(error),
    })
  } finally {
    restoringHistory.value = false
  }
}

async function copyText(text: string, label: string) {
  try {
    await navigator.clipboard.writeText(text)
    ui.toast({ kind: 'success', message: `已复制${label}` })
  } catch {
    ui.toast({ kind: 'error', message: `复制失败，请手动复制：${text}` })
  }
}

function insertDocLink(markdown: string) {
  if (!editorRef.value?.insertValue(markdown)) {
    void copyText(markdown, '链接（编辑器未就绪，请手动粘贴）')
  }
}

function insertUpload(task: UploadTask): boolean {
  if (task.docId !== props.docId || task.ownerToken !== editSessionId.value) return false
  if (mode.value !== 'edit' || !editorReady.value || !task.markdown) return false
  return editorRef.value?.insertValue(task.markdown.startsWith('!') ? `${task.markdown}\n` : task.markdown) ?? false
}

function onUpload(files: File[]) {
  const owner = editSessionId.value
  if (!owner) return
  if (!health.canWrite) {
    ui.toast({ kind: 'warning', message: '当前不可写入（维护中或数据目录不可写），暂不能上传附件' })
    return
  }
  const problems = uploads.enqueue(files, { docId: props.docId, ownerToken: owner })
  if (problems.length) ui.toast({ kind: 'error', message: '部分文件未上传', detail: problems.join('；') })
}

const menuItems = computed<MenuItem[]>(() => {
  const writable = health.canWrite
  const depth = kbId.value ? tree.depthOf(kbId.value, props.docId) : 1
  const atLimit = depth >= health.limits.tree_max_depth
  return [
    { key: 'history', label: '历史版本', icon: 'history' },
    { key: 'copy-link', label: '复制内部链接', icon: 'link' },
    { key: 'copy-url', label: '复制页面地址', icon: 'copy' },
    ...(mode.value === 'edit' ? [{ key: 'insert-link', label: '插入文档链接', icon: 'file-text' }] : []),
    { key: 'export', label: '导出…', icon: 'export' },
    ...(mode.value === 'edit' ? [{ key: 'export-draft', label: '导出当前编辑内容', icon: 'download' }] : []),
    {
      key: 'child',
      label: '新建子文档',
      icon: 'file-plus',
      disabled: !writable || atLimit,
      hint: atLimit ? `已达到最大层级 ${health.limits.tree_max_depth}` : undefined,
      separatorBefore: true,
    },
    { key: 'sibling', label: '新建同级文档', icon: 'plus', disabled: !writable },
    { key: 'delete', label: '删除（移入回收站）', icon: 'trash', danger: true, disabled: !writable, separatorBefore: true },
  ]
})

function onMenu(key: string) {
  const current = doc.value
  if (!current) return
  switch (key) {
    case 'history':
      historyOpen.value = true
      break
    case 'copy-link':
      void copyText(markdownDocLink(current.title, current.id), '内部链接')
      break
    case 'copy-url':
      void copyText(`${window.location.origin}${documentPath(current.id)}`, '页面地址')
      break
    case 'insert-link':
      linkPickerOpen.value = true
      break
    case 'export':
      exportOpen.value = true
      break
    case 'export-draft':
      exportLocal()
      break
    case 'child':
      emit('create', current.id)
      break
    case 'sibling':
      emit('create', current.parent_id && kbId.value && tree.node(kbId.value, current.parent_id) ? current.parent_id : null)
      break
    case 'delete':
      emit('delete')
      break
  }
}

// ------------------------------------------------------------------ 大纲与搜索命中

let outlineTimer: ReturnType<typeof setTimeout> | null = null
function refreshEditorOutline() {
  if (outlineTimer) clearTimeout(outlineTimer)
  outlineTimer = setTimeout(() => {
    if (mode.value === 'edit') outline.value = collectOutline(editorRef.value?.getEditElement(), false)
  }, 400)
}

function onViewerOutline(items: OutlineItem[]) {
  outline.value = items
}

function onHits(count: number) {
  hitCount.value = count
  hitIndex.value = 0
}

function onViewerRendered() {
  rendered.value = true
  if (hitCount.value > 0) void nextTick(() => viewerRef.value?.focusHit(0))
}

function moveHit(delta: number) {
  if (hitCount.value === 0) return
  hitIndex.value = (hitIndex.value + delta + hitCount.value) % hitCount.value
  viewerRef.value?.focusHit(hitIndex.value)
}

function clearSearch() {
  void router.replace({ path: documentPath(props.docId) })
}

function onScroll() {
  activeOutline.value = activeHeadingIndex(outline.value, scrollEl.value)
}

function navigateOutline(item: OutlineItem) {
  focusHeading(item, mode.value === 'edit')
  ui.outlineDrawerOpen = false
}

const sourceRanges = computed(() => (doc.value ? findTermRanges(doc.value.markdown, terms.value) : []))

// ------------------------------------------------------------------ 多标签页提示

let presenceTimer: ReturnType<typeof setInterval> | null = null
function postPresence() {
  if (!health.instanceId) return
  tabBus.post({
    type: 'presence',
    instance_id: health.instanceId,
    doc_id: props.docId,
    session_id: editorSessionId,
    mode: mode.value,
    dirty: !!saveView.value?.dirty,
  })
}

function updateOthers() {
  othersEditing.value = tabBus.othersOn(props.docId).filter((o) => o.mode === 'edit').length
}

const offBus = tabBus.subscribe((message) => {
  if (message.type === 'ping') postPresence()
  if (message.type === 'presence' || message.type === 'left') {
    if (message.doc_id === props.docId) updateOthers()
  }
  if (message.type === 'saved' && message.doc_id === props.docId && message.instance_id === health.instanceId) {
    const mine = controller.value?.revision ?? doc.value?.revision ?? 0
    if (message.revision <= mine) return
    if (mode.value === 'read' && !merge.open && !historyOpen.value) {
      void reloadFromServer()
      ui.toast({ kind: 'info', message: '文档已在另一页面更新，阅读内容已刷新' })
    } else {
      remoteNewer.value = { revision: message.revision, title: message.title }
    }
  }
})

// ------------------------------------------------------------------ 键盘、卸载与生命周期

function onKeydown(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 's') {
    // 拦截浏览器“保存网页”
    event.preventDefault()
    if (mode.value === 'edit' && controller.value) {
      void controller.value.saveNow()
      ui.announce('正在保存')
    } else {
      ui.toast({ kind: 'info', message: '阅读模式下没有需要保存的修改' })
    }
  }
}

function onBeforeUnload(event: BeforeUnloadEvent) {
  const view = controller.value?.view()
  const risky = view && (view.dirty || view.state === 'saving' || view.state === 'conflict')
  if (risky || uploads.activeCount(props.docId) > 0) {
    event.preventDefault()
    event.returnValue = ''
  }
}

function onPageHide() {
  // 尽力而为：草稿立即写入，keepalive 补充提交；均不能视为已保存
  const ctrl = controller.value
  if (!ctrl) return
  void ctrl.writeDraftNow()
  ctrl.keepalive()
}

/** 已由调用方完成离开检查（例如删除当前文档前）时跳过重复确认 */
let leaveApproved = false

const removeGuard = addLeaveGuard(async (to) => {
  if (to.name === 'doc' && to.params.docId === props.docId) return true
  if (leaveApproved) {
    leaveApproved = false
    return true
  }
  return (await prepareLeave()) !== false
})

const offRecovered = health.onRecovered(() => controller.value?.resume())

watch(
  () => health.epochChanged,
  (changed) => {
    if (changed) controller.value?.blockExternally('epoch_changed')
  },
)

watch(
  () => health.serviceState,
  (state) => {
    if (state === 'ok') controller.value?.resume()
  },
)

watch(
  () => props.initialMode,
  (next) => {
    if (next === 'edit' && mode.value === 'read' && doc.value) {
      focusTitleOnReady = true
      void enterEdit()
    }
  },
)

onMounted(() => {
  focusTitleOnReady = props.initialMode === 'edit'
  window.addEventListener('keydown', onKeydown, true)
  window.addEventListener('beforeunload', onBeforeUnload)
  window.addEventListener('pagehide', onPageHide)
  presenceTimer = setInterval(() => {
    postPresence()
    updateOthers()
  }, 4000)
  void load()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown, true)
  window.removeEventListener('beforeunload', onBeforeUnload)
  window.removeEventListener('pagehide', onPageHide)
  if (presenceTimer) clearInterval(presenceTimer)
  if (outlineTimer) clearTimeout(outlineTimer)
  tabBus.post({ type: 'left', instance_id: health.instanceId, doc_id: props.docId, session_id: editorSessionId })
  offBus()
  offRecovered()
  removeGuard()
  teardownEdit()
})

defineExpose({
  prepareLeave,
  approveLeave() {
    leaveApproved = true
  },
  /** 文档树中重命名当前文档：编辑中走同一保存协调流程 */
  async renameFromTree(node: TreeNode, title: string) {
    const ctrl = controller.value
    if (mode.value === 'edit' && ctrl) {
      titleInput.value = title
      ctrl.updateTitle(title)
      const ok = await ctrl.flush(15_000)
      if (!ok) throw new Error(ctrl.view().error?.message ?? '保存尚未完成')
      return
    }
    const latest = await api.documents.get(node.id)
    const result = await api.documents.save(node.id, { title, markdown: latest.markdown, expected_revision: latest.revision })
    doc.value = { ...latest, title, revision: result.revision, updated_at: result.updated_at }
    document.title = `${title} - OpenLJMDB`
  },
  getDoc: () => doc.value,
  isEditing: () => mode.value === 'edit',
})
</script>

<template>
  <div class="document-pane" :class="{ 'has-outline': ui.outlineVisible && !missing && !loadError }">
    <div ref="scrollEl" class="doc-scroll" @scroll.passive="onScroll">
      <!-- 加载：先显示骨架，正文就绪后再启用编辑 -->
      <div v-if="loading" class="doc-inner" aria-busy="true">
        <div class="skeleton" style="height: 34px; width: 46%; margin: 18px 0 24px" />
        <div class="skeleton" style="height: 14px; width: 92%; margin-bottom: 12px" />
        <div class="skeleton" style="height: 14px; width: 78%; margin-bottom: 12px" />
        <div class="skeleton" style="height: 14px; width: 84%" />
        <span class="sr-only" role="status">正在加载文档…</span>
      </div>

      <div v-else-if="missing" class="doc-inner">
        <EmptyState
          :icon="missing.trashed ? 'trash' : 'alert'"
          :title="missing.trashed ? '此文档已移入回收站' : '文档不存在或已被永久删除'"
          :description="
            missing.trashed
              ? '可以在回收站中按删除批次恢复它（连同删除时的子文档）。'
              : '链接指向的目标不可用。内部链接使用稳定文档 ID，不会跳转到同名的其他文档。'
          "
        >
          <RouterLink v-if="missing.trashed" class="btn btn-primary" :to="{ path: '/trash' }">前往回收站</RouterLink>
          <RouterLink v-else class="btn" to="/trash">在回收站中查找</RouterLink>
          <RouterLink class="btn" to="/">返回首页</RouterLink>
        </EmptyState>
        <div v-if="missing.drafts" class="notice notice-info">
          <AppIcon name="draft" class="notice-icon" :size="18" />
          <div class="notice-body">
            本浏览器中仍保留此文档的 {{ missing.drafts }} 份本地草稿，可以在
            <RouterLink to="/drafts">本地草稿</RouterLink> 中查看或导出。
          </div>
        </div>
      </div>

      <div v-else-if="loadError" class="doc-inner">
        <ErrorBlock :error="loadError" action="加载文档" safety="服务端内容不会因此改变。" :on-retry="load" />
      </div>

      <div v-else-if="doc" class="doc-inner">
        <header class="doc-header">
          <nav class="breadcrumb" aria-label="文档位置">
            <RouterLink v-if="kbId" :to="`/knowledge-bases/${kbId}`" class="crumb">{{ kb?.name ?? '知识库' }}</RouterLink>
            <template v-for="a in ancestors" :key="a.id">
              <span class="crumb-sep" aria-hidden="true">/</span>
              <RouterLink :to="documentPath(a.id)" class="crumb">{{ a.title }}</RouterLink>
            </template>
          </nav>
          <div class="doc-toolbar">
            <SaveStatus
              :view="saveView"
              :mode="mode"
              :server-updated-at="doc.updated_at"
              @retry="controller?.retryNow()"
              @save="controller?.saveNow()"
              @resolve-conflict="openConflict"
              @export-draft="exportLocal"
              @reload="reloadPage"
            />
            <span class="toolbar-spacer" />
            <div class="btn-group" role="group" aria-label="阅读或编辑">
              <button type="button" class="btn btn-sm" :aria-pressed="mode === 'read'" :disabled="switching" @click="enterRead">
                <AppIcon name="eye" :size="14" />阅读
              </button>
              <button
                type="button"
                class="btn btn-sm"
                :aria-pressed="mode === 'edit'"
                :disabled="switching || health.epochChanged"
                @click="enterEdit"
              >
                <AppIcon name="edit" :size="14" />编辑
              </button>
            </div>
            <button
              type="button"
              class="btn btn-icon outline-toggle"
              :aria-pressed="ui.outlineVisible"
              :aria-label="ui.outlineVisible ? '隐藏大纲' : '显示大纲'"
              :title="ui.outlineVisible ? '隐藏大纲' : '显示大纲'"
              @click="ui.outlineVisible = !ui.outlineVisible"
            >
              <AppIcon name="outline" :size="18" />
            </button>
            <button
              type="button"
              class="btn btn-icon outline-drawer-toggle"
              aria-label="打开大纲"
              :aria-expanded="ui.outlineDrawerOpen"
              @click="ui.outlineDrawerOpen = true"
            >
              <AppIcon name="outline" :size="18" />
            </button>
            <DropdownMenu :items="menuItems" label="文档操作" @select="onMenu" />
          </div>
        </header>

        <!-- 状态提示 -->
        <div v-if="pendingDrafts.length && !draftDialogOpen" class="notice notice-info doc-notice">
          <AppIcon name="draft" class="notice-icon" :size="18" />
          <div class="notice-body">
            此文档有 {{ pendingDrafts.length }} 份未处理的本地草稿（最近一份保存于 {{ formatDateTime(pendingDrafts[0]!.updated_at) }}）。
          </div>
          <button type="button" class="btn btn-sm" @click="draftDialogOpen = true">查看并处理</button>
        </div>
        <div v-if="saveView?.state === 'conflict'" class="notice notice-danger doc-notice" role="alert">
          <AppIcon name="alert" class="notice-icon" :size="18" />
          <div class="notice-body">另一页面已保存了此文档的新版本，自动保存已暂停。你的修改仍保留在编辑器和本地草稿中。</div>
          <button type="button" class="btn btn-sm btn-primary" @click="openConflict">比较并处理</button>
        </div>
        <div v-if="saveView?.state === 'blocked' && saveView.blocked === 'deleted'" class="notice notice-danger doc-notice" role="alert">
          <AppIcon name="trash" class="notice-icon" :size="18" />
          <div class="notice-body">此文档已被删除（可能在另一页面移入了回收站），无法继续保存。你的修改保留在本地草稿中。</div>
          <button type="button" class="btn btn-sm" @click="saveDeletedAsNew">另存为新文档</button>
          <button type="button" class="btn btn-sm" @click="exportLocal">导出草稿</button>
          <RouterLink class="btn btn-sm" to="/trash">前往回收站</RouterLink>
        </div>
        <div v-if="remoteNewer && saveView?.state !== 'conflict'" class="notice notice-warning doc-notice">
          <AppIcon name="refresh" class="notice-icon" :size="18" />
          <div class="notice-body">
            此文档已在另一页面保存为新版本 r{{ remoteNewer.revision }}。
            <template v-if="saveView?.dirty">继续保存将产生冲突，建议先比较。</template>
          </div>
          <button type="button" class="btn btn-sm" @click="reloadFromServer">{{ saveView?.dirty ? '比较' : '载入最新版本' }}</button>
          <button type="button" class="btn btn-icon btn-sm" aria-label="关闭提示" @click="remoteNewer = null">
            <AppIcon name="close" :size="13" />
          </button>
        </div>
        <div v-else-if="othersEditing > 0 && mode === 'edit'" class="notice doc-notice">
          <AppIcon name="info" class="notice-icon" :size="18" />
          <div class="notice-body">此文档也在另一页面编辑。两边同时保存时，后保存的一方会收到冲突提示，不会静默覆盖。</div>
        </div>
        <div v-if="mode === 'read' && terms.length && rendered" class="hit-bar" role="region" aria-label="搜索命中">
          <AppIcon name="search" :size="15" />
          <template v-if="hitCount > 0">
            <span role="status">“{{ searchQuery }}” 第 {{ hitIndex + 1 }} / {{ hitCount }} 处</span>
            <button type="button" class="btn btn-sm" aria-label="上一处" @click="moveHit(-1)"><AppIcon name="arrow-up" :size="14" />上一处</button>
            <button type="button" class="btn btn-sm" aria-label="下一处" @click="moveHit(1)"><AppIcon name="arrow-down" :size="14" />下一处</button>
          </template>
          <template v-else>
            <span role="status">
              已打开文档，未在渲染后的正文中定位到“{{ searchQuery }}”。
              <template v-if="sourceMatches > 0">匹配来自源码（如图表、公式或链接地址）。</template>
              <template v-else>检索可能按词项或标题匹配，未定位到精确片段。</template>
            </span>
            <button v-if="sourceMatches > 0" type="button" class="btn btn-sm" @click="sourceOpen = true">查看源码中的匹配</button>
          </template>
          <span class="toolbar-spacer" />
          <button type="button" class="btn btn-icon btn-sm" aria-label="关闭搜索高亮" @click="clearSearch"><AppIcon name="close" :size="13" /></button>
        </div>

        <!-- 标题 -->
        <div v-if="mode === 'edit'" class="title-edit">
          <label class="sr-only" for="doc-title-input">文档标题</label>
          <input
            id="doc-title-input"
            ref="titleEl"
            v-model="titleInput"
            class="doc-title-input"
            placeholder="无标题"
            :disabled="!editorReady || editorLocked"
            :aria-invalid="titleError ? 'true' : undefined"
            :aria-describedby="titleError ? 'doc-title-error' : undefined"
            @input="onTitleInput"
            @keydown.enter.prevent="onTitleEnter"
          />
          <p v-if="titleError" id="doc-title-error" class="field-error" role="alert">{{ titleError }}</p>
        </div>
        <h1 v-else class="doc-title">{{ doc.title || '无标题' }}</h1>
        <p v-if="mode === 'read'" class="doc-meta">
          更新于 {{ formatDateTime(doc.updated_at) }} · 修订 r{{ doc.revision }}
        </p>

        <!-- 正文 -->
        <template v-if="mode === 'edit'">
          <div v-if="!editorReady" class="editor-loading" aria-busy="true">
            <span class="spinner" aria-hidden="true" /> 正在准备编辑器…
          </div>
          <MarkdownEditor
            ref="editorRef"
            :key="editSessionId ?? 'editor'"
            :value="editorInitial"
            :theme="theme"
            :readonly="editorLocked"
            :max-upload-bytes="health.limits.upload_max_bytes"
            :class="{ 'editor-hidden': !editorReady }"
            @ready="onEditorReady"
            @dom-input="controller?.notifyEditorInput()"
            @input="(md: string) => controller?.updateMarkdown(md)"
            @upload="onUpload"
            @doc-link="linkPickerOpen = true"
            @layout="refreshEditorOutline"
          />
          <UploadTray v-if="editSessionId" :doc-id="docId" :owner-token="editSessionId" />
        </template>
        <MarkdownViewer
          v-else
          ref="viewerRef"
          :markdown="doc.markdown"
          :theme="theme"
          :terms="terms"
          @outline="onViewerOutline"
          @hits="onHits"
          @rendered="onViewerRendered"
        />
      </div>
    </div>

    <aside
      v-if="doc && !missing && !loadError"
      class="outline-aside"
      :class="{ 'drawer-open': ui.outlineDrawerOpen }"
      :aria-hidden="!ui.outlineVisible && !ui.outlineDrawerOpen ? 'true' : undefined"
    >
      <OutlinePanel
        :items="outline"
        :active-index="activeOutline"
        @navigate="navigateOutline"
        @close="ui.outlineDrawerOpen ? (ui.outlineDrawerOpen = false) : (ui.outlineVisible = false)"
      />
    </aside>
    <div v-if="ui.outlineDrawerOpen" class="drawer-backdrop outline-backdrop" @click="ui.outlineDrawerOpen = false" />

    <!-- 对话框 -->
    <DraftRecoveryDialog
      v-if="doc"
      v-model:open="draftDialogOpen"
      :drafts="pendingDrafts"
      :server="doc"
      :busy-key="draftBusyKey"
      @restore="restoreDraft"
      @compare="compareDraft"
      @export="(d: DraftRecord) => exportDraftMarkdown(d.title, d.markdown)"
      @save-as-new="saveDraftAsNew"
      @discard="discardDraft"
    />
    <MergeDialog
      v-model:open="merge.open"
      :mode="merge.mode"
      :local="merge.local"
      :server="merge.server"
      :server-error="merge.serverError"
      :busy="merge.busy"
      @use-server="mergeUseServer"
      @save-as-new="mergeSaveAsNew"
      @export-local="exportDraftMarkdown(merge.local.title, merge.local.markdown)"
      @submit="mergeSubmit"
      @refetch="refetchMergeServer"
    />
    <HistoryDialog
      v-if="doc"
      v-model:open="historyOpen"
      :doc-id="docId"
      :current="{
        title: mode === 'edit' ? titleInput : doc.title,
        markdown: (mode === 'edit' && editorReady ? editorRef?.getValue() : null) ?? doc.markdown,
        revision: controller?.revision ?? doc.revision,
      }"
      :theme="theme"
      :can-restore="health.canWrite"
      :restoring="restoringHistory"
      @restore="restoreHistory"
    />
    <LinkPickerDialog v-model:open="linkPickerOpen" :kb-id="kbId" :exclude-id="docId" @insert="insertDocLink" />
    <ExportDialog
      v-if="doc"
      v-model:open="exportOpen"
      scope="document"
      :target-id="doc.id"
      :name="doc.title"
      :unsaved="!!saveView?.dirty"
    />
    <BaseDialog v-if="doc" v-model:open="sourceOpen" title="源码中的匹配" size="lg">
      <p class="muted small">以下为 Markdown 源码，高亮处为“{{ searchQuery }}”的匹配位置（共 {{ sourceRanges.length }} 处）。</p>
      <pre class="source-view"><HighlightedText :text="doc.markdown" :ranges="sourceRanges" /></pre>
    </BaseDialog>
  </div>
</template>

<style scoped>
.document-pane {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.doc-scroll {
  flex: 1;
  min-width: 0;
  overflow: auto;
}

.doc-inner {
  max-width: 880px;
  margin: 0 auto;
  padding: 8px 40px 80px;
}

.doc-header {
  position: sticky;
  top: 0;
  z-index: 8;
  padding: 10px 0 8px;
  background: var(--bg);
}

.breadcrumb {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  font-size: 12.5px;
  color: var(--text-3);
  min-width: 0;
}

.crumb {
  color: var(--text-3);
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.crumb:hover {
  color: var(--text);
}

.doc-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 6px;
}

.toolbar-spacer {
  flex: 1;
}

.outline-drawer-toggle {
  display: none;
}

.doc-notice {
  margin: 8px 0;
}

.hit-bar {
  position: sticky;
  top: 74px;
  z-index: 7;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin: 8px 0;
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-sm);
  font-size: 13px;
}

.doc-title {
  margin: 18px 0 6px;
  font-size: 30px;
  line-height: 1.3;
  word-break: break-word;
}

.doc-meta {
  margin-bottom: 18px;
  font-size: 12.5px;
  color: var(--text-3);
}

.title-edit {
  margin: 14px 0 4px;
}

.doc-title-input {
  width: 100%;
  padding: 4px 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--text);
  font: inherit;
  font-size: 30px;
  font-weight: 600;
  line-height: 1.3;
}

.doc-title-input::placeholder {
  color: var(--text-3);
}

.doc-title-input:focus-visible {
  box-shadow: 0 2px 0 var(--focus);
}

.editor-loading {
  padding: 24px 0;
  color: var(--text-3);
}

.editor-hidden {
  visibility: hidden;
  height: 0;
  overflow: hidden;
}

.outline-aside {
  display: none;
  width: var(--outline-width);
  flex: none;
  border-left: 1px solid var(--border);
  background: var(--bg);
  overflow: hidden;
}

.has-outline .outline-aside {
  display: flex;
  flex-direction: column;
}

.source-view {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13px;
  max-height: 60vh;
  overflow: auto;
  padding: 12px;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.drawer-backdrop {
  display: none;
}

@media (max-width: 1180px) {
  .doc-inner {
    padding: 8px 24px 80px;
  }
  .has-outline .outline-aside {
    display: none;
  }
  .outline-toggle {
    display: none;
  }
  .outline-drawer-toggle {
    display: inline-flex;
  }
  .outline-aside.drawer-open {
    display: flex;
    flex-direction: column;
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 80;
    width: min(300px, 86vw);
    box-shadow: var(--shadow-lg);
  }
  .drawer-backdrop.outline-backdrop {
    display: block;
    position: fixed;
    inset: 0;
    z-index: 79;
    background: var(--bg-overlay);
  }
}

@media (max-width: 640px) {
  .doc-inner {
    padding: 4px 14px 80px;
  }
  .doc-title,
  .doc-title-input {
    font-size: 24px;
  }
}
</style>
