<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import KbFormDialog from '@/components/KbFormDialog.vue'
import { api } from '@/api'
import { isApiError } from '@/api/http'
import { asArray, asObject, num, str } from '@/api/normalize'
import type { ImportPreview, Issue, Job, KnowledgeBase } from '@/api/types'
import { waitForJob } from '@/services/jobs'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useTreeStore } from '@/stores/tree'
import { useHealthStore } from '@/stores/health'
import { useSettingsStore } from '@/stores/settings'
import { formatBytes, formatCount } from '@/utils/format'
import { documentPath } from '@/utils/links'
import { readSession, writeSession, removeSession } from '@/utils/storage'
import { uuid } from '@/utils/uuid'

/**
 * 导入：预检 → Markdown 逐篇选择位置 / ZIP 整包选择位置 → 整批新增。
 * 所有导入文档分配新 ID，同名标题允许存在，不覆盖已有文档；提交失败整批回滚。
 */
const route = useRoute()
const kbs = useKnowledgeBaseStore()
const tree = useTreeStore()
const health = useHealthStore()
const preferences = useSettingsStore()

type Step = 'select' | 'preflight' | 'target' | 'committing' | 'done' | 'failed'
const step = ref<Step>('select')
const files = ref<File[]>([])
const selectionName = computed(() => files.value.length === 1 ? files.value[0]!.name : `${files.value.length} 篇 Markdown`)
const fileError = ref('')
const dragOver = ref(false)
const uploadProgress = ref(0)
const preview = ref<ImportPreview | null>(null)
const preflightError = ref<unknown>(null)
const preflightIssues = ref<Issue[]>([])
const targetKb = ref(typeof route.query.kb === 'string' ? route.query.kb : '')
const targetParent = ref<string>('')
const job = ref<Job | null>(null)
const commitError = ref<unknown>(null)
const unknownResult = ref(false)
const result = ref<Record<string, unknown> | null>(null)
const kbFormOpen = ref(false)
let abort: AbortController | null = null

type DocumentDestination = {
  sourceId: string
  title: string
  path: string
  kbId: string
  parentId: string
  status: 'manual' | 'pending' | 'classifying' | 'classified' | 'failed'
  probability: number | null
  error: string
}
const destinations = ref<DocumentDestination[]>([])
const classifying = ref(false)
const classificationNotice = ref('')
let classificationAbort: AbortController | null = null
const perDocument = computed(() => preview.value?.source_kind === 'markdown')
const classifiedCount = computed(() => destinations.value.filter((d) => d.status === 'classified').length)
const classificationCompleted = computed(() => destinations.value.filter((d) => d.status === 'classified' || d.status === 'failed').length)
const affectedKbIds = computed(() => perDocument.value ? [...new Set(destinations.value.map((d) => d.kbId))] : [targetKb.value])

function stopClassification() {
  classificationAbort?.abort()
  classificationAbort = null
  classifying.value = false
  for (const d of destinations.value) {
    if (d.status === 'pending' || d.status === 'classifying') d.status = 'manual'
  }
}

onBeforeUnmount(() => {
  abort?.abort()
  stopClassification()
})

async function classifyDocuments() {
  if (!preview.value || !perDocument.value || classifying.value) return
  classificationNotice.value = ''
  if (!preferences.settings?.import_classifier_configured) {
    classificationNotice.value = '分类服务尚未配置，请先手动选择位置。配置完成后可重新导入。'
    return
  }
  const controller = new AbortController()
  classificationAbort = controller
  classifying.value = true
  const previewId = preview.value.preview_id
  const queue = [...destinations.value]
  for (const d of queue) {
    d.status = 'pending'
    d.error = ''
    d.probability = null
  }
  let cursor = 0
  try {
    await Promise.all(Array.from({ length: Math.min(3, queue.length) }, async () => {
      while (!controller.signal.aborted && cursor < queue.length) {
        const d = queue[cursor++]!
        d.status = 'classifying'
        try {
          const suggestion = await api.imports.classify(previewId, d.sourceId, controller.signal)
          if (controller.signal.aborted) return
          if (suggestion.source_id !== d.sourceId || !kbs.byId(suggestion.target_knowledge_base_id)) {
            throw new Error('建议的知识库已不存在，请手动选择位置')
          }
          d.kbId = suggestion.target_knowledge_base_id
          d.parentId = ''
          d.probability = suggestion.probability
          d.status = 'classified'
          void tree.load(d.kbId).catch(() => undefined)
        } catch (e) {
          if (controller.signal.aborted) return
          d.status = 'failed'
          d.error = e instanceof Error ? e.message : '自动分类失败，请手动选择位置'
        }
      }
    }))
  } finally {
    if (classificationAbort === controller) {
      classificationAbort = null
      classifying.value = false
    }
  }
}

function changeDestination(d: DocumentDestination, knowledgeBaseChanged = false) {
  if (knowledgeBaseChanged) {
    d.parentId = ''
    void tree.load(d.kbId).catch(() => undefined)
  }
  d.status = 'manual'
  d.probability = null
  d.error = ''
}

function applyToAll() {
  for (const d of destinations.value) {
    d.kbId = targetKb.value
    d.parentId = targetParent.value
    changeDestination(d)
  }
}

const limits = computed(() => health.limits)
const maxDepth = computed(() => limits.value.tree_max_depth)

onMounted(() => {
  void kbs.load().catch(() => undefined)
})

watch(
  () => kbs.sorted,
  (list) => {
    if (!targetKb.value && list.length) targetKb.value = list[0]!.id
  },
  { immediate: true },
)

watch(targetKb, (id) => {
  targetParent.value = ''
  if (id) void tree.load(id).catch(() => undefined)
}, { immediate: true })

function pickFiles(next: File[]) {
  fileError.value = ''
  files.value = []
  if (next.length > 1 && next.some((file) => file.name.toLowerCase().endsWith('.zip'))) {
    fileError.value = '请选择多篇 Markdown 文件，或单个 ZIP 资料包'
    return
  }
  for (const file of next) {
    const lower = file.name.toLowerCase()
    const isZip = lower.endsWith('.zip')
    const isMd = lower.endsWith('.md') || lower.endsWith('.markdown')
    if (!isZip && !isMd) {
      fileError.value = '只支持 .md / .markdown 文件或 .zip 资料包'
      return
    }
    const limit = isZip ? limits.value.zip_max_bytes : limits.value.markdown_max_bytes
    if (file.size > limit) {
      fileError.value = `${file.name}（${formatBytes(file.size)}）超过${isZip ? ' ZIP 包' : '单篇 Markdown'}上限 ${formatBytes(limit)}`
      return
    }
    if (file.size === 0) {
      fileError.value = `${file.name} 为空`
      return
    }
  }
  if (next.length > limits.value.zip_max_entries || next.reduce((sum, file) => sum + file.size, 0) > limits.value.zip_max_bytes) {
    fileError.value = '所选文件数量或合计大小超过导入上限'
    return
  }
  files.value = next
}

function onInput(event: Event) {
  const input = event.target as HTMLInputElement
  pickFiles(Array.from(input.files!))
  input.value = ''
}

function onDrop(event: DragEvent) {
  dragOver.value = false
  pickFiles(Array.from(event.dataTransfer!.files))
}

async function runPreflight() {
  if (!files.value.length) return
  step.value = 'preflight'
  preflightError.value = null
  preflightIssues.value = []
  preview.value = null
  uploadProgress.value = 0
  abort = new AbortController()
  const preflightController = abort
  try {
    preview.value = await api.imports.preflight(files.value, {
      signal: abort.signal,
      onProgress: (loaded, total) => (uploadProgress.value = total ? Math.round((loaded / total) * 100) : 0),
    })
    await kbs.load()
    if (preflightController.signal.aborted) return
    if (!targetKb.value && kbs.sorted.length) targetKb.value = kbs.sorted[0]!.id
    destinations.value = preview.value.documents.map((d) => ({
      sourceId: d.source_id, title: d.title, path: d.path,
      kbId: targetKb.value, parentId: targetParent.value,
      status: 'manual', probability: null, error: '',
    }))
    if (perDocument.value) {
      let loaded = false
      try {
        await preferences.load()
        loaded = true
      } catch {
        classificationNotice.value = '无法读取自动分类设置，请手动选择位置。'
      }
      if (preflightController.signal.aborted) return
      step.value = 'target'
      if (loaded && preferences.settings?.import_auto_classify) void classifyDocuments()
    } else {
      step.value = 'target'
    }
  } catch (e) {
    if (isApiError(e) && e.kind === 'aborted') {
      step.value = 'select'
      return
    }
    preflightError.value = e
    if (isApiError(e)) {
      const d = e.details
      preflightIssues.value = asArray(d.issues ?? d.errors ?? d.problems).map((v) => {
        const o = asObject(v)
        return {
          code: str(o, 'code'),
          path: str(o, 'path', 'file', 'entry'),
          message: typeof v === 'string' ? v : (str(o, 'message', 'reason') ?? '未知问题'),
          severity: 'error' as const,
        }
      })
    }
  }
}

function cancelPreflight() {
  abort?.abort()
  step.value = 'select'
}

function restart() {
  abort?.abort()
  stopClassification()
  destinations.value = []
  classificationNotice.value = ''
  pendingCommit = null
  step.value = 'select'
  files.value = []
  preview.value = null
  preflightError.value = null
  preflightIssues.value = []
  job.value = null
  result.value = null
  commitError.value = null
}

function optionsFor(kbId: string, pkgDepth = 1) {
  if (!kbId) return []
  const out: { id: string; label: string; depth: number; disabled: boolean }[] = []
  const walk = (parentId: string | null, depth: number) => {
    for (const node of tree.childrenOf(kbId, parentId)) {
      out.push({
        id: node.id,
        label: `${'　'.repeat(depth - 1)}${node.title}`,
        depth,
        disabled: depth + pkgDepth > maxDepth.value,
      })
      if (out.length < 3000) walk(node.id, depth + 1)
    }
  }
  walk(null, 1)
  return out
}
const parentOptions = computed(() => optionsFor(targetKb.value, perDocument.value ? 1 : preview.value?.max_depth ?? 1))

const parentDepth = computed(() => (targetParent.value && targetKb.value ? tree.depthOf(targetKb.value, targetParent.value) : 0))
const depthExceeded = computed(() => !!preview.value && parentDepth.value + preview.value.max_depth > maxDepth.value)
const targetPath = computed(() => {
  const kb = kbs.byId(targetKb.value)
  const parts = [kb?.name ?? '知识库']
  if (targetParent.value && targetKb.value) parts.push(...tree.pathTitles(targetKb.value, targetParent.value))
  return parts.join(' / ')
})
const canCommit = computed(
  () => !!preview.value && preview.value.errors.length === 0 && health.canWrite && !classifying.value && (
    perDocument.value
      ? destinations.value.length === preview.value.documents.length && destinations.value.every((d) =>
        !!d.sourceId && !!kbs.byId(d.kbId) && (!d.parentId || (!!tree.node(d.kbId, d.parentId) && tree.depthOf(d.kbId, d.parentId) < maxDepth.value)))
      : !!targetKb.value && !depthExceeded.value
  ),
)
const duplicates = computed(() => preview.value?.documents.filter((d) => d.duplicate_title).length ?? 0)

function idempotencyKeyFor(previewId: string): string {
  const key = `import-key:${previewId}`
  const existing = readSession<string | null>(key, null)
  if (existing) return existing
  const created = uuid()
  writeSession(key, created)
  return created
}

/** 结果未知的提交：重试时必须沿用同一请求体与幂等键，避免第一次实际成功后重复导入 */
let pendingCommit: { body: Parameters<typeof api.imports.commit>[0]; key: string } | null = null

async function commit() {
  if (!preview.value || !canCommit.value) return
  step.value = 'committing'
  commitError.value = null
  unknownResult.value = false
  const previewId = preview.value.preview_id
  abort = new AbortController()
  try {
    if (!pendingCommit || pendingCommit.body.preview_id !== previewId) {
      const revisions = new Map<string, number>()
      for (const id of affectedKbIds.value) revisions.set(id, (await tree.load(id, true)).treeRevision)
      const body: Parameters<typeof api.imports.commit>[0] = perDocument.value ? {
        preview_id: previewId,
        targets: destinations.value.map((d) => ({
          source_id: d.sourceId, target_knowledge_base_id: d.kbId,
          target_parent_id: d.parentId || null, expected_tree_revision: revisions.get(d.kbId)!,
        })),
      } : {
        preview_id: previewId, target_knowledge_base_id: targetKb.value,
        target_parent_id: targetParent.value || null, expected_tree_revision: revisions.get(targetKb.value)!,
      }
      pendingCommit = { body, key: idempotencyKeyFor(JSON.stringify(body)) }
    }
    const { body, key } = pendingCommit
    let response = null
    for (let attempt = 0; attempt < 4 && !response; attempt++) {
      try {
        response = await api.imports.commit(body, key)
      } catch (e) {
        if (isApiError(e) && (e.kind === 'network' || e.kind === 'timeout' || e.code === 'DATABASE_BUSY')) {
          unknownResult.value = true
          await new Promise((r) => setTimeout(r, 1500 * (attempt + 1)))
          continue
        }
        throw e
      }
    }
    if (!response) throw new Error('无法确认导入是否已提交。重试会查询同一导入任务，不会重复导入。')
    unknownResult.value = false
    if (response.job) {
      const final = await waitForJob(response.job, { signal: abort.signal, onUpdate: (j) => (job.value = j) })
      job.value = final
      if (final.state !== 'succeeded') {
        // 任务明确失败：已整体回滚，之后的重试可以作为新的提交
        pendingCommit = null
        throw new Error(final.error_summary || '导入失败')
      }
      result.value = final.result
    } else {
      result.value = response.result
    }
    pendingCommit = null
    removeSession(`import-key:${JSON.stringify(body)}`)
    step.value = 'done'
    for (const id of affectedKbIds.value) {
      tree.invalidate(id)
      void tree.load(id, true).catch(() => undefined)
    }
    void kbs.load().catch(() => undefined)
  } catch (e) {
    if (isApiError(e) && e.kind === 'aborted') return
    // Keep the original body/key when a gateway or server failure leaves acceptance unknown.
    if (isApiError(e) && e.kind === 'http') {
      if (e.status < 500) pendingCommit = null
      else if (pendingCommit) unknownResult.value = true
    }
    commitError.value = e
    step.value = 'failed'
  }
}

const resultInfo = computed(() => {
  const r = asObject(result.value)
  const roots = asArray(r.root_documents ?? r.documents ?? r.created_documents)
    .map((v) => {
      const o = asObject(v)
      return { id: str(o, 'id', 'document_id') ?? '', title: str(o, 'title') ?? '无标题' }
    })
    .filter((d) => d.id)
  const rootId = str(r, 'root_document_id')
  if (rootId && !roots.some((d) => d.id === rootId)) roots.unshift({ id: rootId, title: '导入的文档' })
  const toText = (v: unknown) => (typeof v === 'string' ? v : [str(asObject(v), 'path'), str(asObject(v), 'message', 'url')].filter(Boolean).join('：'))
  return {
    created: num(r, 'created_document_count', 'document_count', 'created_count') ?? preview.value?.document_count ?? 0,
    attachments: num(r, 'attachment_count', 'created_attachment_count'),
    roots: roots.slice(0, 20),
    warnings: asArray(r.warnings).map(toText),
    unresolved: asArray(r.unresolved_links).map(toText),
    external: asArray(r.external_urls ?? r.external_resources).map(toText),
  }
})

function onKbCreated(kb: KnowledgeBase) {
  targetKb.value = kb.id
}

const STEPS: { key: Step[]; label: string }[] = [
  { key: ['select'], label: '选择文件' },
  { key: ['preflight'], label: '预检' },
  { key: ['target'], label: '选择位置并确认' },
  { key: ['committing', 'done', 'failed'], label: '导入结果' },
]
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page page-narrow">
      <header class="page-header">
        <div>
          <h1>导入 Markdown</h1>
        </div>
      </header>

      <ol class="steps" aria-label="导入步骤">
        <li v-for="(s, i) in STEPS" :key="i" :class="{ current: s.key.includes(step) }" :aria-current="s.key.includes(step) ? 'step' : undefined">
          <span class="step-no">{{ i + 1 }}</span>{{ s.label }}
        </li>
      </ol>

      <!-- 1. 选择文件 -->
      <section v-if="step === 'select'" class="card step-card">
        <label
          class="dropzone"
          :class="{ over: dragOver }"
          @dragover.prevent="dragOver = true"
          @dragleave="dragOver = false"
          @drop.prevent="onDrop"
        >
          <AppIcon name="import" :size="28" />
          <span>点击选择或拖入多篇 Markdown 文件 / 单个 ZIP 包</span>
          <input type="file" multiple accept=".md,.markdown,.zip,text/markdown,application/zip" class="sr-only" @change="onInput" />
        </label>
        <p v-if="fileError" class="field-error" role="alert">{{ fileError }}</p>
        <p v-for="(file, index) in files" :key="index" class="chosen-file"><AppIcon name="file" :size="15" /> {{ file.name }} · {{ formatBytes(file.size) }}</p>
        <ul class="limits muted small">
          <li>单篇 Markdown 最大 {{ formatBytes(limits.markdown_max_bytes) }}；ZIP 包最大 {{ formatBytes(limits.zip_max_bytes) }}，解压后合计不超过 {{ formatBytes(limits.zip_max_expanded_bytes) }}，最多 {{ formatCount(limits.zip_max_entries) }} 个条目。</li>
          <li>文档树最多 {{ maxDepth }} 层；ZIP 中的目录结构会生成父子文档，目录内的 index.md 作为该节点的正文。单一外层打包目录没有 index.md 时会自动省略。</li>
          <li>标题依据文件名推导，正文中的首个 H1 会保留；本产品导出的资料包按清单还原标题、树结构和内部链接。</li>
          <li>包含路径穿越、绝对路径、符号链接、超限内容或缺失本地资源的包会整包拒绝，不写入任何文档。外部图片链接保留，不会自动下载。</li>
        </ul>
        <div class="row actions">
          <button type="button" class="btn btn-primary" :disabled="!files.length || !health.canWrite" @click="runPreflight">开始预检</button>
        </div>
      </section>

      <!-- 2. 预检 -->
      <section v-else-if="step === 'preflight'" class="card step-card">
        <template v-if="!preflightError">
          <p role="status"><span class="spinner" aria-hidden="true" /> 正在上传并预检 {{ selectionName }}（只写入隔离暂存区）…</p>
          <div class="progress" role="progressbar" :aria-valuenow="uploadProgress" aria-valuemin="0" aria-valuemax="100" aria-label="上传进度">
            <span :style="{ width: `${uploadProgress}%` }" />
          </div>
          <div class="row actions"><button type="button" class="btn" @click="cancelPreflight">取消</button></div>
        </template>
        <template v-else>
          <ErrorBlock :error="preflightError" action="预检" safety="预检未通过，整包已拒绝，没有写入任何文档。" />
          <div v-if="preflightIssues.length" class="issues">
            <h2>问题文件</h2>
            <ul class="small">
              <li v-for="(issue, i) in preflightIssues" :key="i"><code v-if="issue.path">{{ issue.path }}</code> {{ issue.message }}</li>
            </ul>
          </div>
          <div class="row actions"><button type="button" class="btn btn-primary" @click="restart">重新选择文件</button></div>
        </template>
      </section>

      <!-- 3. 预检结果与目标 -->
      <section v-else-if="step === 'target' && preview" class="stack">
        <div class="card step-card">
          <h2>预检结果：{{ preview.source_name ?? selectionName }}</h2>
          <dl class="summary">
            <div><dt>文档</dt><dd>{{ preview.document_count }} 篇</dd></div>
            <div><dt>资源</dt><dd>{{ preview.attachment_count }} 个</dd></div>
            <div><dt>总大小</dt><dd>{{ formatBytes(preview.total_bytes) }}</dd></div>
            <div><dt>结构深度</dt><dd>{{ preview.max_depth }} 层</dd></div>
          </dl>
          <div v-if="preview.errors.length" class="notice notice-danger">
            <AppIcon name="alert" class="notice-icon" :size="18" />
            <div class="notice-body">
              <div class="notice-title">预检发现 {{ preview.errors.length }} 个错误，整包已拒绝，不会写入任何文档</div>
              <ul class="small">
                <li v-for="(issue, i) in preview.errors" :key="i"><code v-if="issue.path">{{ issue.path }}</code> {{ issue.message }}</li>
              </ul>
            </div>
          </div>
          <div v-if="preview.warnings.length" class="notice notice-warning">
            <AppIcon name="alert" class="notice-icon" :size="18" />
            <details class="notice-body notice-details">
              <summary class="notice-title">{{ preview.warnings.length }} 条提示<span class="fold-label when-closed">展开</span><span class="fold-label when-open">收起</span></summary>
              <ul class="small">
                <li v-for="(issue, i) in preview.warnings" :key="i"><code v-if="issue.path">{{ issue.path }}</code> {{ issue.message }}</li>
              </ul>
            </details>
          </div>
          <details v-if="preview.documents.length" class="tree-preview" open>
            <summary>将形成的文档树（{{ preview.documents.length }} 篇<template v-if="duplicates">，{{ duplicates }} 篇与已有标题同名</template>）</summary>
            <ul class="list-plain">
              <li v-for="(d, i) in preview.documents.slice(0, 300)" :key="i" :style="{ paddingLeft: `${(d.depth - 1) * 16}px` }">
                <AppIcon name="file-text" :size="13" /> {{ d.title }}
                <span v-if="d.duplicate_title" class="badge badge-warning">同名</span>
                <span class="muted small">{{ d.path }}</span>
              </li>
              <li v-if="preview.documents.length > 300" class="muted small">……其余 {{ preview.documents.length - 300 }} 篇未列出</li>
            </ul>
          </details>
        </div>

        <div v-if="preview.errors.length === 0" class="card step-card">
          <h2>{{ perDocument ? '逐篇选择导入位置' : '选择导入位置' }}</h2>
          <p v-if="perDocument" class="muted small">每篇笔记只导入到一个位置。可在下方分别选择，也可先统一设置。</p>
          <p v-else class="muted small">ZIP 保留父子结构，不进行自动分类。</p>
          <p v-if="classificationNotice" class="notice notice-warning" role="status">{{ classificationNotice }}</p>
          <div v-if="perDocument && preferences.settings?.import_auto_classify" class="row classification-actions">
            <span v-if="classifying" role="status"><span class="spinner" aria-hidden="true" /> 正在自动分类：{{ classificationCompleted }} / {{ destinations.length }} 篇</span>
            <span v-else-if="classifiedCount" class="muted small">已自动分类 {{ classifiedCount }} 篇，可逐篇修改。</span>
            <button v-if="classifying" type="button" class="btn" @click="stopClassification">停止分类，手动选择</button>
            <button v-else type="button" class="btn" :disabled="!preferences.settings.import_classifier_configured" @click="classifyDocuments">重新自动分类</button>
          </div>
          <div class="field">
            <label for="import-kb">{{ perDocument ? '统一设置：知识库' : '目标知识库' }}</label>
            <div class="row">
              <select id="import-kb" v-model="targetKb" class="select" :disabled="classifying">
                <option v-for="kb in kbs.sorted" :key="kb.id" :value="kb.id">{{ kb.name }}</option>
              </select>
              <button type="button" class="btn" :disabled="classifying" @click="kbFormOpen = true">新建知识库</button>
            </div>
          </div>
          <div class="field">
            <label for="import-parent">父文档（可选）</label>
            <select id="import-parent" v-model="targetParent" class="select" :disabled="classifying">
              <option value="">知识库根目录</option>
              <option v-for="opt in parentOptions" :key="opt.id" :value="opt.id" :disabled="opt.disabled">
                {{ opt.label }}{{ opt.disabled ? '（层级将超过上限）' : '' }}
              </option>
            </select>
            <span class="field-hint">{{ perDocument ? '统一设置位置' : '导入内容将放在' }}：{{ targetPath }}</span>
            <span v-if="!perDocument && depthExceeded" class="field-error" role="alert">
              放在这里会使文档树超过 {{ maxDepth }} 层（父文档第 {{ parentDepth }} 层 + 资料包 {{ preview.max_depth }} 层），请选择较浅的位置。
            </span>
          </div>
          <template v-if="perDocument">
            <button type="button" class="btn" :disabled="classifying || !targetKb || parentDepth >= maxDepth" @click="applyToAll">应用到全部文档</button>
            <ol class="list-plain destination-list">
              <li v-for="(d, i) in destinations" :key="d.sourceId" class="destination-card">
                <div class="destination-title"><AppIcon name="file-text" :size="15" /><strong>{{ d.title }}</strong></div>
                <p class="muted small">{{ d.path }}</p>
                <p v-if="d.status === 'classified'" class="field-hint">自动分类 · 匹配概率 {{ Math.round((d.probability ?? 0) * 100) }}%</p>
                <p v-else-if="d.status === 'pending' || d.status === 'classifying'" class="field-hint">{{ d.status === 'pending' ? '等待分类…' : '正在分类…' }}</p>
                <p v-if="d.error" class="field-error" role="alert">{{ d.error }}</p>
                <div class="destination-fields">
                  <div class="field">
                    <label :for="`destination-kb-${i}`">知识库</label>
                    <select :id="`destination-kb-${i}`" v-model="d.kbId" class="select" :disabled="classifying" @change="changeDestination(d, true)">
                      <option v-if="!d.kbId" value="" disabled>请选择知识库</option>
                      <option v-for="kb in kbs.sorted" :key="kb.id" :value="kb.id">{{ kb.name }}</option>
                    </select>
                  </div>
                  <div class="field">
                    <label :for="`destination-parent-${i}`">父文档（可选）</label>
                    <select :id="`destination-parent-${i}`" v-model="d.parentId" class="select" :disabled="classifying || !d.kbId || tree.trees[d.kbId]?.loading" @change="changeDestination(d)">
                      <option value="">知识库根目录</option>
                      <option v-for="opt in optionsFor(d.kbId)" :key="opt.id" :value="opt.id" :disabled="opt.disabled">{{ opt.label }}{{ opt.disabled ? '（层级将超过上限）' : '' }}</option>
                    </select>
                    <span v-if="tree.trees[d.kbId]?.loading" class="field-hint">正在加载父文档…</span>
                    <button v-if="tree.trees[d.kbId]?.error" type="button" class="btn" @click="tree.load(d.kbId, true).catch(() => undefined)">重新加载父文档</button>
                    <span v-if="d.parentId && tree.depthOf(d.kbId, d.parentId) >= maxDepth" class="field-error">当前位置层级过深，请重新选择。</span>
                  </div>
                </div>
              </li>
            </ol>
          </template>
          <div class="notice notice-info">
            <AppIcon name="info" class="notice-icon" :size="18" />
            <div class="notice-body">
              将新建 {{ preview.document_count }} 篇文档{{ preview.attachment_count ? `和 ${preview.attachment_count} 个附件` : '' }}，全部分配新的文档 ID；
              同名标题允许存在，已有文档及其历史不受本次导入影响。提交失败会整批回滚，不会留下部分文档。
            </div>
          </div>
          <div class="row actions">
            <button type="button" class="btn" @click="restart">取消</button>
            <button type="button" class="btn btn-primary" :disabled="!canCommit" @click="commit">确认导入</button>
          </div>
        </div>
        <div v-else class="row actions"><button type="button" class="btn btn-primary" @click="restart">重新选择文件</button></div>
      </section>

      <!-- 4. 提交与结果 -->
      <section v-else-if="step === 'committing'" class="card step-card">
        <p role="status">
          <span class="spinner" aria-hidden="true" />
          {{ unknownResult ? '提交结果未知，正在查询导入状态（不会重复导入）…' : `正在导入：${job?.phase ?? '处理中'}` }}
        </p>
        <div class="progress" :class="{ indeterminate: !job?.progress }" role="progressbar" :aria-valuenow="job?.progress ?? 0" aria-valuemin="0" aria-valuemax="100" aria-label="导入进度">
          <span :style="{ width: `${job?.progress ?? 0}%` }" />
        </div>
      </section>

      <section v-else-if="step === 'done'" class="card step-card">
        <div class="notice notice-success">
          <AppIcon name="check" class="notice-icon" :size="18" />
          <div class="notice-body">
            <div class="notice-title">导入完成：新增 {{ resultInfo.created }} 篇文档<template v-if="resultInfo.attachments !== null">、{{ resultInfo.attachments }} 个附件</template></div>
            <div>位置：{{ perDocument ? '已按每篇文档选择的位置导入' : targetPath }}</div>
          </div>
        </div>
        <ul v-if="resultInfo.roots.length" class="list-plain created">
          <li v-for="d in resultInfo.roots" :key="d.id"><RouterLink :to="documentPath(d.id)"><AppIcon name="file-text" :size="14" /> {{ d.title }}</RouterLink></li>
        </ul>
        <div v-if="resultInfo.warnings.length || resultInfo.unresolved.length || resultInfo.external.length" class="notice notice-warning">
          <AppIcon name="alert" class="notice-icon" :size="18" />
          <details class="notice-body notice-details">
            <summary class="notice-title">导入报告（{{ resultInfo.warnings.length + resultInfo.unresolved.length + resultInfo.external.length }} 条提示）<span class="fold-label when-closed">展开</span><span class="fold-label when-open">收起</span></summary>
            <ul class="small">
              <li v-for="(w, i) in resultInfo.warnings" :key="`w${i}`">{{ w }}</li>
              <li v-for="(u, i) in resultInfo.unresolved" :key="`u${i}`">无法识别的内部链接：{{ u }}</li>
              <li v-for="(e, i) in resultInfo.external" :key="`e${i}`">外部资源（需联网，未下载）：{{ e }}</li>
            </ul>
          </details>
        </div>
        <div class="row actions">
          <RouterLink v-for="id in affectedKbIds" :key="id" class="btn" :to="`/knowledge-bases/${id}`">打开{{ kbs.byId(id)?.name ?? '知识库' }}</RouterLink>
          <button type="button" class="btn btn-primary" @click="restart">继续导入</button>
        </div>
      </section>

      <section v-else-if="step === 'failed'" class="card step-card">
        <ErrorBlock :error="commitError" action="导入" :safety="unknownResult ? '结果尚未确认，请重试查询同一批次，避免重复导入。' : '本批次未导入或已整体回滚，不会出现部分导入的文档。'" :on-retry="commit" />
        <div class="row actions">
          <button type="button" class="btn" :disabled="unknownResult" @click="step = 'target'">返回确认页</button>
          <button type="button" class="btn" :disabled="unknownResult" @click="restart">重新选择文件</button>
        </div>
      </section>
    </main>
    <KbFormDialog v-model:open="kbFormOpen" @saved="onKbCreated" />
  </div>
</template>

<style scoped>
.steps {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 20px;
  margin: 0 0 18px;
  padding: 0;
  list-style: none;
  color: var(--text-3);
}

.steps li {
  display: flex;
  align-items: center;
  gap: 6px;
}

.steps .current {
  color: var(--primary-text);
  font-weight: 600;
}

.step-no {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  border: 1px solid currentColor;
  font-size: 12px;
}

.step-card {
  padding: 20px;
}

.step-card h2 {
  font-size: 16px;
  margin-bottom: 12px;
}

.classification-actions {
  flex-wrap: wrap;
  margin: 12px 0;
}

.destination-list {
  display: grid;
  gap: 12px;
  margin: 16px 0;
}

.destination-card {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  min-width: 0;
  overflow-wrap: anywhere;
}

.destination-title {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.destination-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 10px;
}

@media (max-width: 600px) {
  .destination-fields { grid-template-columns: 1fr; }
}

.dropzone {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 32px 16px;
  border: 2px dashed var(--border-strong);
  border-radius: var(--radius-lg);
  color: var(--text-2);
  cursor: pointer;
  text-align: center;
}

.dropzone:focus-within {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}

.dropzone.over {
  border-color: var(--primary);
  background: var(--primary-soft);
}

.chosen-file {
  margin-top: 10px;
}

.limits {
  margin: 14px 0 0;
  padding-left: 18px;
}

.limits li + li {
  margin-top: 4px;
}

.actions {
  justify-content: flex-end;
  margin-top: 16px;
}

.summary {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
  margin: 0 0 14px;
}

.summary div {
  padding: 10px 12px;
  border-radius: var(--radius);
  background: var(--bg-soft);
}

.summary dt {
  font-size: 12px;
  color: var(--text-3);
}

.summary dd {
  margin: 2px 0 0;
  font-size: 18px;
  font-weight: 600;
}

.notice + .notice,
.notice {
  margin-top: 10px;
}

.notice ul {
  margin: 4px 0 0;
  padding-left: 18px;
}

.notice-body,
.issues {
  overflow-wrap: anywhere;
}

.notice-details summary {
  cursor: pointer;
}

.fold-label {
  margin-left: 12px;
  font-size: 12px;
  font-weight: 400;
}

.notice-details:not([open]) .when-open,
.notice-details[open] .when-closed {
  display: none;
}

.tree-preview {
  margin-top: 14px;
}

.tree-preview summary {
  cursor: pointer;
  font-weight: 500;
}

.tree-preview ul {
  max-height: 320px;
  overflow: auto;
  margin-top: 8px;
  font-size: 13px;
}

.issues h2 {
  font-size: 14px;
  margin: 12px 0 6px;
}

.created {
  margin: 12px 0;
}

@media (max-width: 640px) {
  .summary {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
