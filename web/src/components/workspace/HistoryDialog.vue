<script setup lang="ts">
import { ref, watch } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import DiffView from '@/components/base/DiffView.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import MarkdownViewer from './MarkdownViewer.vue'
import { api } from '@/api'
import type { HistoryDetail, HistoryItem } from '@/api/types'
import type { ContentTheme } from '@/editor/vditorSetup'
import { formatBytes, formatFullTime, formatRelative } from '@/utils/format'

/**
 * 历史版本：新到旧列出，选中后只读预览、与当前版本比较或查看源码。
 * 退出不改变正文；恢复作为一次新的写入（由上层执行修订校验并先保存当前内容）。
 */
const props = defineProps<{
  open: boolean
  docId: string
  current: { title: string; markdown: string; revision: number }
  theme: ContentTheme
  canRestore: boolean
  restoring?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'restore', item: HistoryDetail): void
}>()

const PAGE_SIZE = 50
const items = ref<HistoryItem[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref<unknown>(null)
const selected = ref<HistoryItem | null>(null)
const detail = ref<HistoryDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref<unknown>(null)
const view = ref<'preview' | 'diff' | 'source'>('preview')

const REASON: Record<string, string> = {
  automatic: '自动快照',
  manual: '手动保存',
  before_restore: '恢复前快照',
}

async function load(reset = true) {
  loading.value = true
  error.value = null
  try {
    const nextPage = reset ? 1 : page.value + 1
    const result = await api.documents.history(props.docId, nextPage, PAGE_SIZE)
    const sorted = [...result.items].sort((a, b) => b.created_at - a.created_at || b.revision - a.revision)
    items.value = reset ? sorted : [...items.value, ...sorted]
    total.value = result.total
    page.value = nextPage
    if (reset && items.value.length > 0 && !selected.value) void select(items.value[0]!)
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

async function select(item: HistoryItem) {
  selected.value = item
  detail.value = null
  detailError.value = null
  detailLoading.value = true
  try {
    const d = await api.documents.historyDetail(props.docId, item.id)
    if (selected.value?.id === item.id) detail.value = d
  } catch (e) {
    if (selected.value?.id === item.id) detailError.value = e
  } finally {
    if (selected.value?.id === item.id) detailLoading.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      selected.value = null
      detail.value = null
      view.value = 'preview'
      void load(true)
    }
  },
  { immediate: true },
)
</script>

<template>
  <BaseDialog :open="open" title="历史版本" size="xl" @update:open="emit('update:open', $event)">
    <p class="muted small history-note">
      每篇文档默认保留最近 100 个历史版本；自动保存每 5 分钟最多形成一个快照，手动保存（Ctrl+S）和恢复历史前会额外保留快照。
      恢复会生成新的当前修订，恢复前的内容仍保留在历史中。编辑器的撤销 / 重做只作用于当前会话，不能替代历史版本。
    </p>
    <ErrorBlock v-if="error" :error="error" action="加载历史版本" :on-retry="() => load(true)" />
    <EmptyState
      v-else-if="!loading && items.length === 0"
      icon="history"
      title="还没有历史版本"
      description="保存文档后会按规则形成历史快照；手动保存（Ctrl+S）会立即创建一个。"
    />
    <div v-else class="history-layout">
      <ol class="history-list list-plain" aria-label="历史版本列表">
        <li v-for="item in items" :key="item.id">
          <button
            type="button"
            class="history-item"
            :class="{ active: selected?.id === item.id }"
            :aria-pressed="selected?.id === item.id"
            @click="select(item)"
          >
            <span class="history-time">{{ formatFullTime(item.created_at) }}</span>
            <span class="history-meta">
              <span class="badge">{{ REASON[item.reason] ?? item.reason }}</span>
              <span>修订 r{{ item.revision }}</span>
              <span v-if="item.size_bytes !== null">{{ formatBytes(item.size_bytes) }}</span>
            </span>
            <span class="history-title ellipsis">{{ item.title }}</span>
            <span class="muted small">{{ formatRelative(item.created_at) }}</span>
          </button>
        </li>
        <li v-if="items.length < total" class="history-more">
          <button type="button" class="btn btn-sm" :disabled="loading" @click="load(false)">
            {{ loading ? '加载中…' : `加载更多（共 ${total} 个）` }}
          </button>
        </li>
        <li v-if="loading && items.length === 0" class="muted history-more">正在加载历史版本…</li>
      </ol>
      <section class="history-preview" aria-label="版本预览">
        <template v-if="selected">
          <div class="row wrap preview-head">
            <div class="btn-group" role="group" aria-label="预览方式">
              <button type="button" class="btn btn-sm" :aria-pressed="view === 'preview'" @click="view = 'preview'">渲染预览</button>
              <button type="button" class="btn btn-sm" :aria-pressed="view === 'diff'" @click="view = 'diff'">与当前版本比较</button>
              <button type="button" class="btn btn-sm" :aria-pressed="view === 'source'" @click="view = 'source'">Markdown 源码</button>
            </div>
            <span class="spacer" />
            <button
              type="button"
              class="btn btn-sm btn-primary"
              :disabled="!detail || !canRestore || restoring"
              :title="canRestore ? undefined : '当前不可写入'"
              @click="detail && emit('restore', detail)"
            >
              <AppIcon name="restore" :size="14" />{{ restoring ? '正在恢复…' : '恢复此版本' }}
            </button>
          </div>
          <ErrorBlock v-if="detailError" :error="detailError" action="读取历史版本" :on-retry="() => select(selected!)" />
          <p v-else-if="detailLoading" class="muted">正在读取版本内容…</p>
          <template v-else-if="detail">
            <h3 class="preview-title">{{ detail.title }}</h3>
            <div v-if="view === 'preview'" class="preview-body">
              <MarkdownViewer :markdown="detail.markdown" :theme="theme" :terms="[]" />
            </div>
            <div v-else-if="view === 'diff'">
              <p v-if="detail.title !== current.title" class="small">
                标题：<del>{{ detail.title }}</del> → <ins>{{ current.title }}</ins>
              </p>
              <DiffView
                :old-text="detail.markdown"
                :new-text="current.markdown"
                :old-label="`历史 r${detail.revision}`"
                :new-label="`当前 r${current.revision}`"
              />
            </div>
            <pre v-else class="source-view">{{ detail.markdown }}</pre>
          </template>
        </template>
        <p v-else class="muted">选择左侧的版本查看内容。</p>
      </section>
    </div>
    <template #footer>
      <button type="button" class="btn" @click="emit('update:open', false)">关闭</button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.history-note {
  margin-bottom: 12px;
}

.history-layout {
  display: grid;
  grid-template-columns: 280px 1fr;
  gap: 16px;
  min-height: 52vh;
}

.history-list {
  border-right: 1px solid var(--border);
  padding-right: 10px;
  max-height: 64vh;
  overflow: auto;
}

.history-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: 8px 10px;
  margin-bottom: 4px;
  border: 1px solid transparent;
  border-radius: var(--radius);
  background: none;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.history-item:hover {
  background: var(--bg-hover);
}

.history-item.active {
  border-color: var(--primary);
  background: var(--primary-soft);
}

.history-time {
  font-weight: 500;
  font-size: 13px;
}

.history-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 12px;
  color: var(--text-3);
}

.history-title {
  font-size: 12px;
  color: var(--text-2);
}

.history-more {
  padding: 8px 10px;
}

.history-preview {
  min-width: 0;
  max-height: 64vh;
  overflow: auto;
}

.preview-head {
  position: sticky;
  top: 0;
  z-index: 2;
  padding-bottom: 10px;
  background: var(--bg-elevated);
}

.preview-title {
  font-size: 18px;
  margin: 6px 0 12px;
}

.source-view {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13px;
  padding: 12px;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.spacer {
  flex: 1;
}

@media (max-width: 760px) {
  .history-layout {
    grid-template-columns: 1fr;
  }
  .history-list {
    border-right: 0;
    max-height: 30vh;
  }
}
</style>
