<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import AppIcon from '@/components/base/AppIcon.vue'
import type { SaveSnapshotView } from '@/services/saveController'
import { formatClock, formatDateTime } from '@/utils/format'

/**
 * 保存状态：区分“已保存 / 有修改 / 保存中 / 保存失败正在重试 / 暂停重试 / 版本冲突”，
 * 并单独说明本地草稿是否已写入浏览器存储。状态变化通过 role=status 播报，不只依赖颜色。
 */
const props = defineProps<{
  view: SaveSnapshotView | null
  mode: 'read' | 'edit'
  serverUpdatedAt: number | null
}>()

const emit = defineEmits<{
  (e: 'retry'): void
  (e: 'save'): void
  (e: 'resolve-conflict'): void
  (e: 'export-draft'): void
  (e: 'reload'): void
}>()

const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

watch(
  () => props.view?.state,
  (state) => {
    if (state === 'retry_wait' && !timer) {
      timer = setInterval(() => (now.value = Date.now()), 1000)
    } else if (state !== 'retry_wait' && timer) {
      clearInterval(timer)
      timer = null
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

const countdown = computed(() => {
  const at = props.view?.nextRetryAt
  return at ? Math.max(0, Math.ceil((at - now.value) / 1000)) : 0
})

type Tone = 'ok' | 'pending' | 'busy' | 'warn' | 'error'

const status = computed<{ icon: string; tone: Tone; text: string; detail: string }>(() => {
  const v = props.view
  if (props.mode === 'read' || !v) {
    return {
      icon: 'eye',
      tone: 'ok',
      text: '阅读模式',
      detail: props.serverUpdatedAt ? `最近更新 ${formatDateTime(props.serverUpdatedAt)}` : '',
    }
  }
  const lastSaved = v.lastSavedAt ? `最近保存 ${formatClock(v.lastSavedAt)}` : ''
  if (v.state === 'blocked') {
    return v.blocked === 'epoch_changed'
      ? { icon: 'lock', tone: 'error', text: '数据已恢复或替换，保存已停止', detail: '需重新加载页面后比较草稿' }
      : { icon: 'alert', tone: 'error', text: '文档已被删除，无法保存', detail: '修改保留在本地草稿' }
  }
  if (v.state === 'conflict') {
    return { icon: 'alert', tone: 'error', text: '存在版本冲突', detail: '另一页面已保存新版本，自动保存已暂停' }
  }
  if (v.waitingForService) {
    return {
      icon: 'lock',
      tone: 'warn',
      text: v.waitingForService === 'maintenance' ? '维护中，暂停保存' : '数据目录不可写，暂停保存',
      detail: '尚未写入数据库',
    }
  }
  if (v.state === 'saving') return { icon: 'refresh', tone: 'busy', text: '保存中…', detail: lastSaved }
  if (v.state === 'retry_wait') {
    return {
      icon: 'alert',
      tone: 'warn',
      text: `保存失败，${countdown.value} 秒后重试`,
      detail: `第 ${v.retryAttempt}/${v.maxRetries} 次自动重试 · ${v.error?.message ?? ''}`,
    }
  }
  if (v.state === 'paused') {
    return {
      icon: 'alert',
      tone: 'error',
      text: v.error?.local ? '无法保存' : v.error?.retryable ? '保存失败，自动重试已暂停' : '保存失败',
      detail: `${v.error?.message ?? ''}${v.error?.local ? '' : ' · 尚未写入数据库'}`,
    }
  }
  if (v.state === 'dirty' || v.dirty) {
    return {
      icon: 'edit',
      tone: 'pending',
      text: v.autosaveSuspended ? '已恢复草稿，尚未保存' : '有未保存的修改',
      detail: v.autosaveSuspended ? '继续编辑将自动保存' : lastSaved || '等待自动保存',
    }
  }
  return {
    icon: 'check',
    tone: 'ok',
    text: '已保存',
    detail: lastSaved || (props.serverUpdatedAt ? `最近更新 ${formatDateTime(props.serverUpdatedAt)}` : ''),
  }
})

const draftText = computed(() => {
  const v = props.view
  if (!v || props.mode === 'read') return ''
  const relevant = v.dirty || v.state === 'conflict' || v.state === 'blocked' || v.state === 'paused' || v.state === 'retry_wait'
  if (!relevant) return ''
  if (v.draftStatus === 'error') return `本地草稿写入失败：${v.draftError ?? ''}`
  if (v.draftStatus === 'saved' && v.draftSavedAt) return `本地草稿已保留 ${formatClock(v.draftSavedAt)}${v.draftUpToDate ? '' : '（新输入待写入）'}`
  if (v.draftStatus === 'pending') return '正在写入本地草稿…'
  return '本地草稿尚未写入'
})

/** 播报文本：只在状态类别变化时更新，避免倒计时每秒打扰 */
const announcement = computed(() => {
  const v = props.view
  if (!v || props.mode === 'read') return ''
  if (v.state === 'retry_wait') return '保存失败，正在自动重试'
  return status.value.text
})
</script>

<template>
  <div class="save-status" :class="`tone-${status.tone}`">
    <span class="status-main">
      <AppIcon :name="status.icon" :size="14" :class="{ spinning: status.tone === 'busy' }" />
      <span class="status-text">{{ status.text }}</span>
    </span>
    <span v-if="status.detail" class="status-detail">{{ status.detail }}</span>
    <span v-if="draftText" class="status-draft" :class="{ 'draft-error': view?.draftStatus === 'error' }">{{ draftText }}</span>
    <span class="status-actions">
      <button v-if="view && (view.state === 'retry_wait' || view.state === 'paused')" type="button" class="btn btn-sm" @click="emit('retry')">
        立即重试
      </button>
      <button v-if="view && view.autosaveSuspended && view.state === 'dirty'" type="button" class="btn btn-sm btn-primary" @click="emit('save')">
        保存
      </button>
      <button v-if="view?.state === 'conflict'" type="button" class="btn btn-sm btn-primary" @click="emit('resolve-conflict')">
        处理冲突
      </button>
      <button v-if="view?.state === 'blocked' && view.blocked === 'epoch_changed'" type="button" class="btn btn-sm" @click="emit('reload')">
        重新加载
      </button>
      <button
        v-if="view && mode === 'edit' && (view.state === 'paused' || view.state === 'blocked' || view.state === 'conflict' || view.draftStatus === 'error')"
        type="button"
        class="btn btn-sm btn-ghost"
        @click="emit('export-draft')"
      >
        导出草稿
      </button>
    </span>
    <span class="sr-only" role="status" aria-live="polite">{{ announcement }}</span>
  </div>
</template>

<style scoped>
.save-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 10px;
  font-size: 12.5px;
  color: var(--text-3);
  min-width: 0;
}

.status-main {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 500;
  color: var(--text-2);
}

.tone-ok .status-main {
  color: var(--success);
}

.tone-pending .status-main {
  color: var(--text-2);
}

.tone-warn .status-main {
  color: var(--warning);
}

.tone-error .status-main {
  color: var(--danger);
}

.status-detail,
.status-draft {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 360px;
}

.draft-error {
  color: var(--danger);
}

.status-actions {
  display: inline-flex;
  gap: 6px;
}

.status-actions:empty {
  display: none;
}

.spinning {
  animation: spin 1s linear infinite;
}
</style>
