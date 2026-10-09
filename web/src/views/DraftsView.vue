<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import LoadingBlock from '@/components/base/LoadingBlock.vue'
import MarkdownViewer from '@/components/workspace/MarkdownViewer.vue'
import { confirmDialog } from '@/composables/useDialogs'
import { draftStore, type DraftRecord } from '@/services/drafts'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useDraftsStore } from '@/stores/drafts'
import { useUiStore } from '@/stores/ui'
import { exportDraftMarkdown } from '@/utils/download'
import { formatFullTime } from '@/utils/format'
import { documentPath } from '@/utils/links'
import { codePoints } from '@/utils/text'

/**
 * 本地草稿集中查看（每个数据实例一份列表，以实例 ID 隔离）：
 * 显示文档、时间、预览；可以打开对应文档（触发恢复提示）、导出草稿、删除。
 * 草稿另存为新文档需要知识库与父文档，在文档页内完成；这里提供导出与删除。
 */
const router = useRouter()
const kbs = useKnowledgeBaseStore()
const draftsStore = useDraftsStore()
const ui = useUiStore()
const loading = ref(true)

const instanceDrafts = computed(() => draftsStore.instanceDrafts)
const otherDrafts = computed(() => draftsStore.otherDrafts)
const expanded = ref<Set<string>>(new Set())
const theme = computed(() => ui.resolvedTheme)

onMounted(() => {
  void kbs.load().catch(() => undefined)
  void loadDrafts()
})

async function loadDrafts(retry = false) {
  loading.value = true
  try {
    if (retry) await draftsStore.probe()
    await draftsStore.refresh()
  } finally {
    loading.value = false
  }
}

function toggle(key: string) {
  const next = new Set(expanded.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expanded.value = next
}

function kbName(draft: DraftRecord): string {
  return kbs.byId(draft.knowledge_base_id)?.name ?? '未知知识库'
}

function timeLabel(draft: DraftRecord): string {
  return `保存于 ${formatFullTime(draft.updated_at)}`
}

async function removeDraft(draft: DraftRecord) {
  const ok = await confirmDialog({
    title: '删除本地草稿',
    message: `将删除保存于 ${formatFullTime(draft.updated_at)} 的草稿（约 ${codePoints(draft.markdown).length} 字）。`,
    details: ['服务端版本不受影响，此操作无法撤销。'],
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    await draftStore.delete(draft.key)
    draftsStore.notifyChanged()
    ui.toast({ kind: 'success', message: '已删除本地草稿' })
  } catch (e) {
    ui.toast({ kind: 'error', message: '删除失败', detail: e instanceof Error ? e.message : String(e) })
  }
}

function openDoc(draft: DraftRecord) {
  void router.push(documentPath(draft.document_id))
}

const totalChars = computed(() => {
  const all = [...instanceDrafts.value, ...otherDrafts.value]
  return all.reduce((sum, d) => sum + codePoints(d.markdown).length, 0)
})
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page page-narrow">
      <header class="page-header">
        <div>
          <h1>本地草稿</h1>
        </div>
      </header>

      <LoadingBlock v-if="loading || draftsStore.available === null" label="正在检查本地草稿存储…" />
      <EmptyState
        v-else-if="draftsStore.available === false"
        icon="alert"
        title="浏览器本地存储不可用"
        :description="draftsStore.errorMessage ?? '无法访问 IndexedDB，本地草稿功能不可用。'"
      >
        <button type="button" class="btn" @click="loadDrafts(true)">重试</button>
      </EmptyState>
      <EmptyState
        v-else-if="instanceDrafts.length === 0 && otherDrafts.length === 0"
        icon="draft"
        title="没有本地草稿"
        description="编辑文档时，内容会自动保存到本地草稿；成功提交到服务后自动删除。"
      />

      <template v-else>
        <div class="notice notice-info">
          <AppIcon name="info" class="notice-icon" :size="18" />
          <div class="notice-body">
            本浏览器中有 {{ instanceDrafts.length + otherDrafts.length }} 份草稿（约 {{ totalChars.toLocaleString('zh-CN') }} 字）。
            打开对应文档时会提示恢复、比较服务端版本或放弃。也可以在这里导出为 .md 文件。
          </div>
        </div>

        <section v-if="instanceDrafts.length" class="drafts-section">
          <h2>当前数据实例的草稿（{{ instanceDrafts.length }} 份）</h2>
          <ul class="list-plain draft-list">
            <li v-for="draft in instanceDrafts" :key="draft.key" class="draft-item">
              <div class="draft-head">
                <div class="draft-info">
                  <div class="draft-title">{{ draft.title || '无标题' }}</div>
                  <div class="draft-meta">
                    <span>{{ kbName(draft) }}</span>
                    <span aria-hidden="true">·</span>
                    <span>{{ timeLabel(draft) }}</span>
                    <span aria-hidden="true">·</span>
                    <span>约 {{ codePoints(draft.markdown).length }} 字</span>
                  </div>
                </div>
                <div class="draft-actions">
                  <button type="button" class="btn btn-sm" @click="openDoc(draft)">打开文档</button>
                  <button type="button" class="btn btn-sm" @click="exportDraftMarkdown(draft.title, draft.markdown)">导出草稿</button>
                  <button type="button" class="btn btn-sm" :aria-expanded="expanded.has(draft.key)" @click="toggle(draft.key)">
                    <AppIcon :name="expanded.has(draft.key) ? 'chevron-up' : 'chevron-down'" :size="14" />预览
                  </button>
                  <button type="button" class="btn btn-sm btn-danger-text" @click="removeDraft(draft)">删除</button>
                </div>
              </div>
              <div v-if="expanded.has(draft.key)" class="draft-preview">
                <MarkdownViewer :markdown="draft.markdown" :theme="theme" />
              </div>
            </li>
          </ul>
        </section>

        <section v-if="otherDrafts.length" class="drafts-section">
          <h2>其他数据实例的草稿（{{ otherDrafts.length }} 份，可能已过期）</h2>
          <p class="muted small">
            这些草稿来自其他数据实例（可能是旧的备份、已恢复的数据或已删除的测试环境）。打开文档不会自动恢复它们；可以导出后手动比较，或直接删除。
          </p>
          <ul class="list-plain draft-list">
            <li v-for="draft in otherDrafts" :key="draft.key" class="draft-item">
              <div class="draft-head">
                <div class="draft-info">
                  <div class="draft-title">{{ draft.title || '无标题' }}</div>
                  <div class="draft-meta">
                    <span>{{ kbName(draft) }}</span>
                    <span aria-hidden="true">·</span>
                    <span>{{ timeLabel(draft) }}</span>
                    <span aria-hidden="true">·</span>
                    <span>约 {{ codePoints(draft.markdown).length }} 字</span>
                    <span class="badge">数据实例 {{ draft.instance_id.slice(0, 8) }}</span>
                  </div>
                </div>
                <div class="draft-actions">
                  <button type="button" class="btn btn-sm" @click="exportDraftMarkdown(draft.title, draft.markdown)">导出草稿</button>
                  <button type="button" class="btn btn-sm" :aria-expanded="expanded.has(draft.key)" @click="toggle(draft.key)">
                    <AppIcon :name="expanded.has(draft.key) ? 'chevron-up' : 'chevron-down'" :size="14" />预览
                  </button>
                  <button type="button" class="btn btn-sm btn-danger-text" @click="removeDraft(draft)">删除</button>
                </div>
              </div>
              <div v-if="expanded.has(draft.key)" class="draft-preview">
                <MarkdownViewer :markdown="draft.markdown" :theme="theme" />
              </div>
            </li>
          </ul>
        </section>
      </template>
    </main>
  </div>
</template>

<style scoped>
.drafts-section {
  margin-bottom: 24px;
}

.drafts-section h2 {
  font-size: 16px;
  margin-bottom: 10px;
}

.drafts-section > p {
  margin-bottom: 10px;
}

.draft-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.draft-item {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
}

.draft-head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  flex-wrap: wrap;
}

.draft-info {
  flex: 1;
  min-width: 220px;
}

.draft-title {
  font-weight: 600;
  word-break: break-word;
}

.draft-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin-top: 3px;
  font-size: 12px;
  color: var(--text-3);
}

.draft-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.draft-preview {
  padding: 0 14px 12px 14px;
  border-top: 1px solid var(--border);
  max-height: 60vh;
  overflow: auto;
}
</style>
