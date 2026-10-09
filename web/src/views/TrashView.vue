<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import { api } from '@/api'
import { isApiError } from '@/api/http'
import type { RestoreResult, TrashBatch, TrashBatchDetail } from '@/api/types'
import { choose, confirmDialog } from '@/composables/useDialogs'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useTreeStore } from '@/stores/tree'
import { useHealthStore } from '@/stores/health'
import { useUiStore } from '@/stores/ui'
import { formatDateTime, formatFullTime } from '@/utils/format'
import { documentPath } from '@/utils/links'

/**
 * 回收站：按删除批次展示（知识库整库删除与文档子树删除分开），整批恢复，
 * 永久删除与清空展示不可恢复的对象和数量并要求明确确认。默认不自动清空。
 */
const route = useRoute()
const router = useRouter()
const kbs = useKnowledgeBaseStore()
const tree = useTreeStore()
const health = useHealthStore()
const ui = useUiStore()

const PAGE_SIZE = 50
const batches = ref<TrashBatch[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref<unknown>(null)
const filterKb = ref(typeof route.query.kb === 'string' ? route.query.kb : '')
const kindFilter = ref<'all' | 'knowledge_base' | 'document_subtree'>('all')
const details = reactive<Record<string, TrashBatchDetail | 'loading' | { error: unknown }>>({})
const expanded = ref<Set<string>>(new Set())
const busy = ref<string | null>(null)
const lastRestore = ref<{ batch: TrashBatch; result: RestoreResult } | null>(null)
const emptying = ref(false)

const visible = computed(() => batches.value.filter((b) => kindFilter.value === 'all' || b.kind === kindFilter.value))
const kbOptions = computed(() => {
  const map = new Map<string, string>()
  for (const kb of kbs.sorted) map.set(kb.id, kb.name)
  for (const b of batches.value) if (!map.has(b.knowledge_base_id)) map.set(b.knowledge_base_id, `${b.knowledge_base_name || '已删除的知识库'}（在回收站）`)
  return Array.from(map, ([id, name]) => ({ id, name }))
})

function kbName(batch: TrashBatch): string {
  return batch.knowledge_base_name || kbs.byId(batch.knowledge_base_id)?.name || '未知知识库'
}

function kbActive(batch: TrashBatch): boolean {
  return !!kbs.byId(batch.knowledge_base_id)
}

async function load(reset = true) {
  loading.value = true
  error.value = null
  try {
    const next = reset ? 1 : page.value + 1
    const result = await api.trash.list({ page: next, pageSize: PAGE_SIZE, knowledgeBaseId: filterKb.value || null })
    const sorted = [...result.items].sort((a, b) => b.created_at - a.created_at)
    batches.value = reset ? sorted : [...batches.value, ...sorted]
    total.value = result.total
    page.value = next
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

watch(filterKb, (kb) => {
  void router.replace({ query: { ...route.query, kb: kb || undefined } })
  void load(true)
})

onMounted(() => {
  void kbs.load().catch(() => undefined)
  void load(true)
})

async function loadDetail(batch: TrashBatch, force = false): Promise<TrashBatchDetail | null> {
  const existing = details[batch.id]
  if (!force && existing && existing !== 'loading' && !('error' in existing)) return existing as TrashBatchDetail
  details[batch.id] = 'loading'
  try {
    const detail = await api.trash.get(batch.id)
    details[batch.id] = detail
    return detail
  } catch (e) {
    details[batch.id] = { error: e }
    return null
  }
}

function toggle(batch: TrashBatch) {
  const next = new Set(expanded.value)
  if (next.has(batch.id)) next.delete(batch.id)
  else {
    next.add(batch.id)
    void loadDetail(batch)
  }
  expanded.value = next
}

function detailOf(id: string): TrashBatchDetail | null {
  const d = details[id]
  return d && d !== 'loading' && !('error' in d) ? (d as TrashBatchDetail) : null
}

function detailError(id: string): unknown | null {
  const d = details[id]
  return d && d !== 'loading' && 'error' in d ? (d as { error: unknown }).error : null
}

/** 批次内节点按原父子关系缩进显示 */
function nodeRows(detail: TrashBatchDetail) {
  const ids = new Set(detail.nodes.map((n) => n.id))
  const children = new Map<string, typeof detail.nodes>()
  const roots: typeof detail.nodes = []
  for (const node of detail.nodes) {
    if (node.parent_id && ids.has(node.parent_id)) {
      const list = children.get(node.parent_id) ?? []
      list.push(node)
      children.set(node.parent_id, list)
    } else roots.push(node)
  }
  const rows: { id: string; title: string; depth: number }[] = []
  const walk = (list: typeof detail.nodes, depth: number) => {
    for (const node of list) {
      rows.push({ id: node.id, title: node.title, depth })
      if (rows.length > 500) return
      walk(children.get(node.id) ?? [], depth + 1)
    }
  }
  walk(roots, 0)
  return rows
}

async function restore(batch: TrashBatch) {
  if (batch.kind === 'document_subtree' && !kbActive(batch) && kbs.loaded) {
    const key = await choose({
      title: '需要先恢复知识库',
      message: `这批文档所属的知识库《${kbName(batch)}》仍在回收站中。请先恢复该知识库，再恢复这批文档。`,
      cancelKey: 'cancel',
      actions: [
        { key: 'cancel', label: '取消', autofocus: true },
        { key: 'show', label: '显示知识库批次', kind: 'primary' },
      ],
    })
    if (key === 'show') {
      kindFilter.value = 'knowledge_base'
      filterKb.value = ''
    }
    return
  }
  busy.value = batch.id
  try {
    for (let attempt = 0; attempt < 2; attempt++) {
      const detail = await loadDetail(batch, true)
      let treeRevision = detail?.tree_revision ?? null
      if (treeRevision === null && batch.kind === 'document_subtree') {
        treeRevision = (await api.knowledgeBases.tree(batch.knowledge_base_id).catch(() => null))?.tree_revision ?? null
      }
      try {
        const result = await api.trash.restore(batch.id, treeRevision)
        lastRestore.value = { batch, result }
        batches.value = batches.value.filter((b) => b.id !== batch.id)
        total.value = Math.max(0, total.value - 1)
        tree.invalidate(batch.knowledge_base_id)
        void kbs.load().catch(() => undefined)
        ui.toast({ kind: 'success', message: `已恢复 ${result.restored_document_count || batch.document_count} 篇文档` })
        return
      } catch (e) {
        if (attempt === 0 && isApiError(e) && e.code === 'TREE_REVISION_CONFLICT') continue
        throw e
      }
    }
  } catch (e) {
    ui.toast({ kind: 'error', message: '恢复失败，回收站内容保持不变', detail: e instanceof Error ? e.message : String(e) })
  } finally {
    busy.value = null
  }
}

function purgeDetails(batch: TrashBatch, detail: TrashBatchDetail): string[] {
  const p = detail.delete_preview
  const lines = [
    batch.kind === 'knowledge_base'
      ? `知识库《${kbName(batch)}》及其全部 ${p?.document_count ?? batch.document_count} 篇文档（包括此前单独删除、仍在回收站中的子树）`
      : `文档《${batch.root_title || '无标题'}》及其子文档，共 ${p?.document_count ?? batch.document_count} 篇`,
  ]
  if (p?.history_count !== null && p?.history_count !== undefined) lines.push(`${p.history_count} 个历史版本`)
  if (p?.attachment_count !== null && p?.attachment_count !== undefined) {
    lines.push(`${p.attachment_count} 个附件引用；不再被任何文档、历史或回收站引用的附件会列为清理候选，需在“附件管理”中另行确认清理`)
  }
  if (batch.kind === 'knowledge_base' && p?.batch_count) lines.push(`同时移除该知识库下 ${p.batch_count} 个回收批次`)
  lines.push('永久删除后无法通过回收站恢复，也不能迁移到其他知识库恢复，只能依靠此前创建的完整备份。')
  return lines
}

async function purge(batch: TrashBatch) {
  busy.value = batch.id
  try {
    for (let attempt = 0; attempt < 2; attempt++) {
      const detail = await loadDetail(batch, true)
      if (!detail) throw (details[batch.id] as { error: unknown }).error
      if (!detail.delete_preview) throw new Error('服务端未提供删除确认信息，无法安全执行永久删除')
      const ok = await confirmDialog({
        title: '永久删除',
        message: '以下内容将被永久删除：',
        details: purgeDetails(batch, detail),
        confirmText: '永久删除',
        danger: true,
        acknowledge: '我了解此操作不可恢复',
      })
      if (!ok) return
      try {
        const result = await api.trash.purge(batch.id, detail.delete_preview.confirmation_token)
        batches.value = batches.value.filter((b) => b.id !== batch.id && !(batch.kind === 'knowledge_base' && b.knowledge_base_id === batch.knowledge_base_id))
        ui.toast({
          kind: 'success',
          message: `已永久删除${result.purged_document_count !== null ? ` ${result.purged_document_count} 篇文档` : ''}`,
          detail:
            result.cleanup_candidate_count !== null
              ? `${result.cleanup_candidate_count} 个不再被引用的附件已列为清理候选，文件尚未删除。`
              : '不再被引用的附件会列为清理候选，文件尚未删除。',
          action: { label: '附件管理', handler: () => void router.push('/attachments') },
        })
        return
      } catch (e) {
        // 确认令牌过期或批次内容已变化：重新预览后再确认
        if (attempt === 0 && isApiError(e) && (e.status === 409 || e.status === 422)) {
          ui.toast({ kind: 'warning', message: '删除确认已过期或批次内容已变化，请重新确认' })
          continue
        }
        throw e
      }
    }
  } catch (e) {
    ui.toast({ kind: 'error', message: '永久删除失败，内容仍在回收站中', detail: e instanceof Error ? e.message : String(e) })
  } finally {
    busy.value = null
  }
}

async function emptyTrash() {
  busy.value = '*'
  try {
    const { delete_preview: preview, confirmation_token: token } = await api.trash.emptyPreview()
    if (preview.batch_count === 0) return
    const ok = await confirmDialog({
      title: '清空回收站',
      message: `将永久删除回收站中的全部 ${preview.batch_count} 个删除批次：`,
      details: [
        `${preview.knowledge_base_count} 个知识库（含其中全部文档），${preview.batch_count - preview.knowledge_base_count} 个文档子树`,
        `合计 ${preview.document_count} 篇文档、${preview.history_count} 个历史版本`,
        '不再被引用的附件会列为清理候选，需另行确认清理。',
        '此操作不可恢复，只能依靠此前创建的完整备份。',
      ],
      confirmText: '清空回收站',
      danger: true,
      acknowledge: '我了解清空后所有内容都无法通过回收站恢复',
    })
    if (!ok) return
    emptying.value = true
    await api.trash.empty(token)
    ui.toast({ kind: 'success', message: '回收站已清空', detail: '不再被引用的附件已列为清理候选，文件尚未删除。' })
  } catch (e) {
    ui.toast({ kind: 'error', message: '清空回收站失败', detail: e instanceof Error ? e.message : String(e) })
  } finally {
    emptying.value = false
    busy.value = null
    await load(true)
  }
}
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page">
      <header class="page-header">
        <div>
          <h1>回收站</h1>
        </div>
        <span class="spacer" />
        <button type="button" class="btn btn-danger-text" :disabled="!!busy || batches.length === 0 || !health.canWrite" @click="emptyTrash">
          <AppIcon name="trash" :size="15" />清空回收站
        </button>
      </header>

      <div class="row wrap filters">
        <label for="trash-kb" class="sr-only">按知识库筛选</label>
        <select id="trash-kb" v-model="filterKb" class="select filter-select">
          <option value="">全部知识库</option>
          <option v-for="opt in kbOptions" :key="opt.id" :value="opt.id">{{ opt.name }}</option>
        </select>
        <div class="btn-group" role="group" aria-label="批次类型">
          <button type="button" class="btn btn-sm" :aria-pressed="kindFilter === 'all'" @click="kindFilter = 'all'">全部</button>
          <button type="button" class="btn btn-sm" :aria-pressed="kindFilter === 'knowledge_base'" @click="kindFilter = 'knowledge_base'">知识库</button>
          <button type="button" class="btn btn-sm" :aria-pressed="kindFilter === 'document_subtree'" @click="kindFilter = 'document_subtree'">文档</button>
        </div>
      </div>

      <div v-if="emptying" class="notice notice-warning" role="status">
        <span class="spinner" aria-hidden="true" />
        <div class="notice-body">正在清空回收站…</div>
      </div>

      <div v-if="lastRestore" class="notice notice-success restore-result" role="status">
        <AppIcon name="check" class="notice-icon" :size="18" />
        <div class="notice-body">
          <div class="notice-title">
            已恢复{{ lastRestore.batch.kind === 'knowledge_base' ? `知识库《${kbName(lastRestore.batch)}》` : `《${lastRestore.batch.root_title}》` }}，共
            {{ lastRestore.result.restored_document_count || lastRestore.batch.document_count }} 篇文档
          </div>
          <ul v-if="lastRestore.result.adjustments.length" class="adjustments">
            <li v-for="(adj, i) in lastRestore.result.adjustments" :key="i">
              {{ adj.message || `《${adj.title || '文档'}》的原父文档已不可用，已恢复到知识库根层级` }}
            </li>
          </ul>
          <div class="row wrap">
            <RouterLink
              v-if="lastRestore.result.root_document_id || lastRestore.batch.root_document_id"
              class="btn btn-sm"
              :to="documentPath((lastRestore.result.root_document_id || lastRestore.batch.root_document_id)!)"
            >
              打开文档
            </RouterLink>
            <RouterLink class="btn btn-sm" :to="`/knowledge-bases/${lastRestore.result.knowledge_base_id || lastRestore.batch.knowledge_base_id}`">
              打开知识库
            </RouterLink>
            <button type="button" class="btn btn-sm btn-ghost" @click="lastRestore = null">关闭</button>
          </div>
        </div>
      </div>

      <ErrorBlock v-if="error" :error="error" action="加载回收站" :on-retry="() => load(true)" />
      <p v-else-if="loading && batches.length === 0" class="muted" role="status">正在加载回收站…</p>
      <EmptyState
        v-else-if="!loading && visible.length === 0"
        icon="trash"
        title="回收站是空的"
        :description="filterKb || kindFilter !== 'all' ? '当前筛选条件下没有删除批次。' : '删除的文档和知识库会出现在这里，可以随时整批恢复。'"
      />

      <ul v-else class="list-plain batches">
        <li v-for="batch in visible" :key="batch.id" class="batch">
          <div class="batch-main">
            <span class="badge" :class="batch.kind === 'knowledge_base' ? 'badge-warning' : 'badge-info'">
              {{ batch.kind === 'knowledge_base' ? '知识库' : '文档' }}
            </span>
            <div class="batch-info">
              <div class="batch-title">
                {{ batch.kind === 'knowledge_base' ? kbName(batch) : batch.root_title || '无标题' }}
              </div>
              <div class="batch-meta">
                <span v-if="batch.kind === 'document_subtree'">所属知识库：{{ kbName(batch) }}<template v-if="!kbActive(batch) && kbs.loaded">（在回收站）</template></span>
                <span>{{ batch.document_count }} 篇文档</span>
                <span :title="formatFullTime(batch.created_at)">删除于 {{ formatDateTime(batch.created_at) }}</span>
              </div>
            </div>
            <div class="row batch-actions">
              <button type="button" class="btn btn-sm btn-ghost" :aria-expanded="expanded.has(batch.id)" @click="toggle(batch)">
                <AppIcon :name="expanded.has(batch.id) ? 'chevron-up' : 'chevron-down'" :size="14" />查看内容
              </button>
              <button type="button" class="btn btn-sm" :disabled="!!busy || !health.canWrite" @click="restore(batch)">
                <AppIcon name="restore" :size="14" />恢复
              </button>
              <button type="button" class="btn btn-sm btn-danger-text" :disabled="!!busy || !health.canWrite" @click="purge(batch)">
                永久删除
              </button>
              <span v-if="busy === batch.id" class="spinner" aria-label="处理中" />
            </div>
          </div>
          <div v-if="expanded.has(batch.id)" class="batch-detail">
            <p v-if="details[batch.id] === 'loading'" class="muted small">正在读取批次内容…</p>
            <ErrorBlock
              v-else-if="detailError(batch.id)"
              :error="detailError(batch.id)"
              action="读取批次内容"
              compact
              :on-retry="() => loadDetail(batch, true)"
            />
            <template v-else-if="detailOf(batch.id)">
              <ul class="list-plain node-list" :aria-label="`批次中的 ${detailOf(batch.id)!.nodes.length} 篇文档`">
                <li v-for="row in nodeRows(detailOf(batch.id)!)" :key="row.id" :style="{ paddingLeft: `${row.depth * 16}px` }">
                  <AppIcon name="file-text" :size="13" /> {{ row.title }}
                </li>
              </ul>
              <p v-if="detailOf(batch.id)!.nodes.length === 0" class="muted small">此批次没有可列出的文档。</p>
            </template>
          </div>
        </li>
      </ul>

      <div v-if="batches.length < total" class="load-more">
        <button type="button" class="btn btn-sm" :disabled="loading" @click="load(false)">加载更多（共 {{ total }} 个批次）</button>
      </div>
    </main>
  </div>
</template>

<style scoped>
.spacer {
  flex: 1;
}

.filters {
  margin-bottom: 16px;
}

.filter-select {
  width: auto;
  min-width: 200px;
}

.restore-result {
  margin-bottom: 16px;
}

.adjustments {
  margin: 6px 0;
  padding-left: 18px;
}

.batches {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.batch {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
}

.batch-main {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  flex-wrap: wrap;
}

.batch-info {
  flex: 1;
  min-width: 200px;
}

.batch-title {
  font-weight: 600;
  word-break: break-word;
}

.batch-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 12px;
  color: var(--text-3);
}

.batch-detail {
  padding: 8px 14px 12px 50px;
  border-top: 1px solid var(--border);
}

.node-list {
  max-height: 260px;
  overflow: auto;
  font-size: 13px;
  color: var(--text-2);
}

.node-list li {
  padding: 2px 0;
}

.load-more {
  margin-top: 16px;
  text-align: center;
}
</style>
