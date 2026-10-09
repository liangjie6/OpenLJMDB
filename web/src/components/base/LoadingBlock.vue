<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

/** 加载态：显示操作对象与阶段；长时间无响应时提供重试 / 取消，不用无限转圈掩盖失败 */
const props = withDefaults(
  defineProps<{ label: string; slowAfterMs?: number; onRetry?: () => void; onCancel?: () => void }>(),
  { slowAfterMs: 8000, onRetry: undefined, onCancel: undefined },
)
const slow = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

onMounted(() => {
  timer = setTimeout(() => (slow.value = true), props.slowAfterMs)
})
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})
</script>

<template>
  <div class="loading-block" role="status" aria-live="polite">
    <span class="spinner" aria-hidden="true" />
    <span>{{ label }}</span>
    <template v-if="slow">
      <span class="muted">响应较慢，服务可能繁忙或已断开。</span>
      <button v-if="onRetry" type="button" class="btn btn-sm" @click="onRetry()">重试</button>
      <button v-if="onCancel" type="button" class="btn btn-sm btn-ghost" @click="onCancel()">取消</button>
    </template>
  </div>
</template>

<style scoped>
.loading-block {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 24px 4px;
  color: var(--text-2);
}
</style>
