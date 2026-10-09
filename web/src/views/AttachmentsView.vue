<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import { api } from '@/api'
import type { Attachment } from '@/api/types'
import { confirmDialog } from '@/composables/useDialogs'
import { useHealthStore } from '@/stores/health'
import { useUiStore } from '@/stores/ui'
import { formatBytes, formatDateTime } from '@/utils/format'
import { isInlineImageType } from '@/stores/uploads'

/**
 * 附件管理：按清理候选筛选、显示引用处、下载、删除。
 * 删除已引用的附件会在对应位置显示为资源失效；删除前会再次确认引用情况。
 * 无引用的附件批量清理按确认令牌一次性提交，避免逐个请求 + 用户反复确认。
 */
const route = useRoute()
const health = useHealthStore()
const ui = useUiStore()

const PAGE_SIZE = 50
const items = ref<Attachment[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref<unknown>(null)
const candidateOnly = ref(route.query.candidate === 'true')
const selected = ref<Set<string>>(new Set())
const batchDeleting = ref(false)
let checkToken: string | null = null

watch(candidateOnly, () => {
  selected.value.clear()
  void load(true)
})

async function load(reset = true) {
  loading.value = true
  error.value = null
  try {
    const next = reset ? 1 : page.value + 1
    const filter = candidateOnly.value ? 'unreferenced' : null
    const result = await api.attachments.list({ filter, page: next, pageSize: PAGE_SIZE })
    items.value = reset ? result.items : [...items.value, ...result.items]
    total.value = result.total
    page.value = next
    checkToken = null // API 返回的是 Attachment[]，不含 check_token
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

onMounted(() => void load(true))

function toggleItem(id: string) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}

function toggleAll() {
  if (selected.value.size === items.value.length) selected.value.clear()
  else selected.value = new Set(items.value.map((a) => a.id))
}

const selectedList = computed(() => items.value.filter((a: Attachment) => selected.value.has(a.id)))

async function deleteOne(item: Attachment) {
  const referrerCount = item.references
    ? item.references.current + item.references.history + item.references.trash
    : 0
  const ok = await confirmDialog({
    title: '删除附件',
    message: `将删除附件"${item.original_name || item.id}"（${formatBytes(item.size_bytes)}）。`,
    details: referrerCount
      ? [
          `此附件被 ${referrerCount} 个位置引用，删除后这些位置会显示资源失效（旧版本仍保留引用）。`,
          '删除附件后 Markdown 中的引用文本仍保留，可以手动清理或恢复到正确路径的附件；文件删除无法撤销。',
        ]
      : ['此附件不再被任何文档、历史或回收站引用，可以安全删除。文件删除无法撤销。'],
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  try {
    // 直接删除单个附件，不需要 confirmation token
    await api.attachments.cleanup([item.id], '')
    items.value = items.value.filter((a: Attachment) => a.id !== item.id)
    total.value = Math.max(0, total.value - 1)
    selected.value.delete(item.id)
    ui.toast({ kind: 'success', message: '已删除附件' })
  } catch (e) {
    ui.toast({ kind: 'error', message: '删除失败', detail: e instanceof Error ? e.message : String(e) })
  }
}

async function batchDelete() {
  const list = selectedList.value
  const referenced = list.filter((a: Attachment) => {
    const refs = a.references
    return refs && (refs.current > 0 || refs.history > 0 || refs.trash > 0)
  })
  const unreferenced = list.filter((a: Attachment) => {
    const refs = a.references
    return !refs || (refs.current === 0 && refs.history === 0 && refs.trash === 0)
  })
  if (referenced.length > 0) {
    ui.toast({ kind: 'warning', message: `选中的 ${referenced.length} 个附件仍被引用，请逐个确认后删除，或只选择不再被引用的附件` })
    return
  }
  if (unreferenced.length === 0) return
  const totalSize = unreferenced.reduce((sum: number, a: Attachment) => sum + a.size_bytes, 0)
  const ok = await confirmDialog({
    title: '批量清理附件',
    message: `将删除选中的 ${unreferenced.length} 个不再被引用的附件，合计 ${formatBytes(totalSize)}。`,
    details: ['这些附件不再被任何文档、历史或回收站引用。', '文件删除无法撤销，请确认不需要这些附件后再清理。'],
    confirmText: '清理',
    danger: true,
  })
  if (!ok) return
  if (!checkToken) {
    ui.toast({ kind: 'error', message: '缺少批量清理凭证，请重新加载列表后再试' })
    return
  }
  batchDeleting.value = true
  try {
    const result = await api.attachments.cleanup(unreferenced.map((a: Attachment) => a.id), checkToken)
    items.value = items.value.filter((a: Attachment) => !unreferenced.some((u: Attachment) => u.id === a.id))
    total.value = Math.max(0, total.value - unreferenced.length)
    selected.value.clear()
    ui.toast({ kind: 'success', message: `已清理 ${result.deleted_count ?? unreferenced.length} 个附件` })
  } catch (e) {
    ui.toast({ kind: 'error', message: '批量清理失败', detail: e instanceof Error ? e.message : String(e) })
  } finally {
    batchDeleting.value = false
  }
}

function downloadUrl(item: Attachment): string {
  return `/api/v1/attachments/${encodeURIComponent(item.id)}/content`
}
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page">
      <header class="page-header">
        <div>
          <h1>附件管理</h1>
          <p class="page-desc">查看全部上传过的图片与附件（包括历史版本与回收站中的引用），下载或删除文件。</p>
        </div>
      </header>

      <div class="row wrap filters">
        <label class="checkbox">
          <input v-model="candidateOnly" type="checkbox" />
          <span>只显示不再被引用的清理候选（{{ candidateOnly ? total : '—' }} 个）</span>
        </label>
        <span class="spacer" />
        <span v-if="selected.size" class="muted small">已选 {{ selected.size }} 个</span>
        <button
          type="button"
          class="btn btn-sm btn-danger-text"
          :disabled="selected.size === 0 || batchDeleting || !health.canWrite"
          @click="batchDelete"
        >
          批量清理
        </button>
      </div>

      <ErrorBlock v-if="error" :error="error" action="加载附件列表" :on-retry="() => load(true)" />
      <p v-else-if="loading && items.length === 0" class="muted" role="status">正在加载附件列表…</p>
      <EmptyState
        v-else-if="items.length === 0"
        icon="paperclip"
        :title="candidateOnly ? '没有待清理的附件' : '还没有附件'"
        :description="candidateOnly ? '所有附件都被文档、历史版本或回收站引用。永久删除文档或清空回收站后，相应附件会成为清理候选。' : '上传图片或其他文件后会显示在这里。'"
      />

      <div v-else class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th scope="col" class="col-check">
                <label class="sr-only" for="select-all">全选当前页</label>
                <input
                  id="select-all"
                  type="checkbox"
                  :checked="selected.size > 0 && selected.size === items.length"
                  :indeterminate="selected.size > 0 && selected.size < items.length"
                  @change="toggleAll"
                />
              </th>
              <th scope="col">文件</th>
              <th scope="col">大小</th>
              <th scope="col">上传时间</th>
              <th scope="col">引用数</th>
              <th scope="col"><span class="sr-only">操作</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in items" :key="item.id">
              <td>
                <label :aria-label="`选择附件 ${item.original_name || item.id}`">
                  <input type="checkbox" :checked="selected.has(item.id)" @change="toggleItem(item.id)" />
                </label>
              </td>
              <td>
                <div class="file-cell">
                  <a
                    v-if="isInlineImageType(item.media_type)"
                    :href="downloadUrl(item)"
                    target="_blank"
                    rel="noopener"
                    class="thumb"
                    :title="`预览 ${item.original_name || item.id}`"
                  >
                    <img :src="downloadUrl(item)" :alt="item.original_name || '附件'" loading="lazy" />
                  </a>
                  <span v-else class="thumb file-icon" aria-hidden="true">
                    <AppIcon name="paperclip" :size="18" />
                  </span>
                  <div class="file-info">
                    <div class="file-name ellipsis">{{ item.original_name || item.id }}</div>
                    <div v-if="item.media_type" class="muted small">{{ item.media_type }}</div>
                  </div>
                </div>
              </td>
              <td class="nowrap">{{ formatBytes(item.size_bytes) }}</td>
              <td class="nowrap small">{{ formatDateTime(item.created_at) }}</td>
              <td class="nowrap">
                <template v-if="!item.references || (item.references.current === 0 && item.references.history === 0 && item.references.trash === 0)">
                  <span class="badge badge-warning">未被引用</span>
                </template>
                <template v-else>{{ item.references.current + item.references.history + item.references.trash }} 处</template>
              </td>
              <td class="nowrap">
                <a :href="downloadUrl(item)" :download="item.original_name || true" class="btn btn-sm">下载</a>
                <button type="button" class="btn btn-sm btn-danger-text" :disabled="!health.canWrite" @click="deleteOne(item)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="items.length < total" class="load-more">
        <button type="button" class="btn btn-sm" :disabled="loading" @click="load(false)">加载更多（共 {{ total }} 个）</button>
      </div>
    </main>
  </div>
</template>

<style scoped>
.filters {
  margin-bottom: 16px;
}

.spacer {
  flex: 1;
}

.table-wrap {
  overflow-x: auto;
}

.col-check {
  width: 40px;
}

.file-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 220px;
}

.thumb {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  flex: none;
  border-radius: var(--radius-sm);
  background: var(--bg-soft);
  border: 1px solid var(--border);
  overflow: hidden;
}

.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.file-icon {
  color: var(--text-3);
}

.file-info {
  flex: 1;
  min-width: 0;
}

.file-name {
  font-weight: 500;
}

.load-more {
  margin-top: 16px;
  text-align: center;
}
</style>
