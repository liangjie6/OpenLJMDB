<script setup lang="ts">
import { computed } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import type { DocumentDetail } from '@/api/types'
import type { DraftRecord } from '@/services/drafts'
import { editorSessionId } from '@/services/session'
import { useHealthStore } from '@/stores/health'
import { formatBytes, formatDateTime, formatFullTime } from '@/utils/format'
import { utf8Bytes } from '@/utils/text'

/**
 * 重新打开文档时发现的本地草稿：展示草稿时间、基础修订和服务端最近更新时间。
 * 基础修订仍匹配：可恢复继续编辑；不匹配：进入比较，保留两份内容。
 * 恢复草稿不会立即静默覆盖服务端。
 */
const props = defineProps<{
  open: boolean
  drafts: DraftRecord[]
  server: DocumentDetail
  busyKey?: string | null
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'restore', draft: DraftRecord): void
  (e: 'compare', draft: DraftRecord): void
  (e: 'export', draft: DraftRecord): void
  (e: 'save-as-new', draft: DraftRecord): void
  (e: 'discard', draft: DraftRecord): void
}>()

const health = useHealthStore()

interface DraftInfo {
  draft: DraftRecord
  baseMatches: boolean
  epochMatches: boolean
  source: string
  size: string
}

const items = computed<DraftInfo[]>(() =>
  props.drafts.map((draft) => {
    const epochMatches = draft.base_data_epoch === health.pageEpoch
    return {
      draft,
      epochMatches,
      baseMatches: epochMatches && draft.base_revision === props.server.revision,
      source: draft.tab_session_id === editorSessionId ? '本页面之前的编辑' : '已关闭或刷新的页面',
      size: formatBytes(utf8Bytes(draft.markdown)),
    }
  }),
)
</script>

<template>
  <BaseDialog :open="open" title="发现未保存到服务的本地草稿" size="lg" @update:open="emit('update:open', $event)">
    <p class="intro">
      浏览器中保留了此文档 {{ drafts.length }} 份尚未确认保存的草稿。服务端当前版本为修订
      <strong>r{{ server.revision }}</strong>，最近更新于 {{ formatDateTime(server.updated_at) }}。
      在你确认之前，草稿不会自动提交。
    </p>
    <ul class="list-plain draft-list">
      <li v-for="item in items" :key="item.draft.key" class="draft-card">
        <div class="draft-head">
          <strong class="ellipsis">{{ item.draft.title || '无标题' }}</strong>
          <span v-if="item.baseMatches" class="badge badge-primary">基础修订一致</span>
          <span v-else-if="!item.epochMatches" class="badge badge-danger">来自恢复或切换前的数据</span>
          <span v-else class="badge badge-warning">基础修订不一致，需要比较</span>
        </div>
        <dl class="dl small">
          <dt>草稿时间</dt>
          <dd>{{ formatFullTime(item.draft.updated_at) }}</dd>
          <dt>基于修订</dt>
          <dd>r{{ item.draft.base_revision }}（服务端当前 r{{ server.revision }}）</dd>
          <dt>来源</dt>
          <dd>{{ item.source }} · {{ item.size }}</dd>
        </dl>
        <div class="row wrap draft-actions">
          <button
            v-if="item.baseMatches"
            type="button"
            class="btn btn-sm btn-primary"
            :disabled="!!busyKey"
            @click="emit('restore', item.draft)"
          >
            恢复草稿继续编辑
          </button>
          <button
            type="button"
            class="btn btn-sm"
            :class="{ 'btn-primary': !item.baseMatches }"
            :disabled="!!busyKey"
            @click="emit('compare', item.draft)"
          >
            比较{{ item.baseMatches ? '差异' : '并合并' }}
          </button>
          <button type="button" class="btn btn-sm" @click="emit('export', item.draft)">
            <AppIcon name="download" :size="14" />导出草稿
          </button>
          <button type="button" class="btn btn-sm" :disabled="!!busyKey || !health.canWrite" @click="emit('save-as-new', item.draft)">
            另存为新文档
          </button>
          <button type="button" class="btn btn-sm btn-danger-text" :disabled="!!busyKey" @click="emit('discard', item.draft)">
            放弃草稿
          </button>
          <span v-if="busyKey === item.draft.key" class="spinner" aria-label="处理中" />
        </div>
      </li>
    </ul>
    <p class="muted small">浏览器草稿只用于异常恢复，可能被浏览器清除，不属于完整备份。</p>
    <template #footer>
      <button type="button" class="btn" @click="emit('update:open', false)">稍后处理</button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.intro {
  margin-bottom: 12px;
}

.draft-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 12px;
}

.draft-card {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-soft);
}

.draft-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.draft-actions {
  margin-top: 10px;
}
</style>
