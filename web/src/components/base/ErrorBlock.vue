<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { isApiError } from '@/api/http'
import { formatFullTime } from '@/utils/format'

/**
 * 错误展示：说明发生的操作、现有内容是否安全、下一步动作；
 * 错误码、request_id 等技术信息收纳在可展开区域并可复制。
 */
const props = withDefaults(
  defineProps<{
    error: unknown
    action?: string
    safety?: string
    retryLabel?: string
    compact?: boolean
    onRetry?: () => void
  }>(),
  { action: undefined, safety: undefined, retryLabel: '重试', compact: false, onRetry: undefined },
)

const showDetails = ref(false)
const copied = ref(false)
const occurredAt = Date.now()

const message = computed(() => {
  const e = props.error
  if (isApiError(e)) return e.message
  if (e instanceof Error) return e.message
  return String(e ?? '未知错误')
})

const technical = computed(() => {
  const e = props.error
  const lines = [`时间：${formatFullTime(occurredAt)}`]
  if (isApiError(e)) {
    lines.push(`错误码：${e.code}`)
    if (e.status) lines.push(`HTTP 状态：${e.status}`)
    if (e.requestId) lines.push(`request_id：${e.requestId}`)
    if (Object.keys(e.details).length > 0) lines.push(`详情：${JSON.stringify(e.details, null, 2)}`)
  } else if (e instanceof Error && e.stack) {
    lines.push(e.stack)
  }
  return lines.join('\n')
})

async function copy() {
  try {
    await navigator.clipboard.writeText(`${props.action ? props.action + '失败：' : ''}${message.value}\n${technical.value}`)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <div class="notice notice-danger error-block" :class="{ compact }" role="alert">
    <AppIcon name="alert" class="notice-icon" :size="18" />
    <div class="notice-body">
      <div class="notice-title">{{ action ? `${action}失败` : '操作失败' }}</div>
      <div class="error-message">{{ message }}</div>
      <div v-if="safety" class="error-safety">{{ safety }}</div>
      <div class="row wrap error-actions">
        <button v-if="onRetry" type="button" class="btn btn-sm" @click="onRetry()">
          <AppIcon name="refresh" :size="14" />{{ retryLabel }}
        </button>
        <slot />
        <button type="button" class="btn btn-sm btn-ghost" :aria-expanded="showDetails" @click="showDetails = !showDetails">
          {{ showDetails ? '收起技术信息' : '技术信息' }}
        </button>
      </div>
      <div v-if="showDetails" class="error-details">
        <pre>{{ technical }}</pre>
        <button type="button" class="btn btn-sm" @click="copy">
          <AppIcon name="copy" :size="14" />{{ copied ? '已复制' : '复制错误详情' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.error-block {
  margin: 8px 0;
}

.error-message {
  margin-top: 2px;
  word-break: break-word;
}

.error-safety {
  margin-top: 4px;
  color: var(--text-2);
}

.error-actions {
  margin-top: 8px;
}

.error-details pre {
  margin: 8px 0;
  padding: 8px 10px;
  max-height: 220px;
  overflow: auto;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
</style>
