<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const { toasts, announcement } = storeToRefs(ui)
const politeToasts = computed(() => toasts.value.filter((t) => t.kind !== 'error'))
const errorToasts = computed(() => toasts.value.filter((t) => t.kind === 'error'))

const ICON: Record<string, string> = { info: 'info', success: 'check', warning: 'alert', error: 'alert' }
</script>

<template>
  <div class="toast-host">
    <div role="status" aria-live="polite" class="toast-stack">
      <div v-for="toast in politeToasts" :key="toast.id" class="toast" :class="`toast-${toast.kind}`">
        <AppIcon :name="ICON[toast.kind]!" :size="18" class="toast-icon" />
        <div class="toast-body">
          <div>{{ toast.message }}</div>
          <div v-if="toast.detail" class="toast-detail">{{ toast.detail }}</div>
        </div>
        <button v-if="toast.action" type="button" class="btn btn-sm" @click="toast.action.handler(), ui.dismiss(toast.id)">
          {{ toast.action.label }}
        </button>
        <button type="button" class="btn btn-icon btn-sm" aria-label="关闭提示" @click="ui.dismiss(toast.id)">
          <AppIcon name="close" :size="14" />
        </button>
      </div>
    </div>
    <div role="alert" aria-live="assertive" class="toast-stack">
      <div v-for="toast in errorToasts" :key="toast.id" class="toast toast-error">
        <AppIcon name="alert" :size="18" class="toast-icon" />
        <div class="toast-body">
          <div>{{ toast.message }}</div>
          <div v-if="toast.detail" class="toast-detail">{{ toast.detail }}</div>
        </div>
        <button v-if="toast.action" type="button" class="btn btn-sm" @click="toast.action.handler(), ui.dismiss(toast.id)">
          {{ toast.action.label }}
        </button>
        <button type="button" class="btn btn-icon btn-sm" aria-label="关闭提示" @click="ui.dismiss(toast.id)">
          <AppIcon name="close" :size="14" />
        </button>
      </div>
    </div>
    <div class="sr-only" role="status" aria-live="polite">{{ announcement }}</div>
  </div>
</template>

<style scoped>
.toast-host {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 900;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: min(420px, calc(100vw - 32px));
  pointer-events: none;
}

.toast-stack {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 10px 10px 14px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-left: 4px solid var(--info);
  border-radius: var(--radius);
  box-shadow: var(--shadow-md);
  pointer-events: auto;
}

.toast-icon {
  margin-top: 2px;
  color: var(--info);
}

.toast-success {
  border-left-color: var(--success);
}

.toast-success .toast-icon {
  color: var(--success);
}

.toast-warning {
  border-left-color: var(--warning);
}

.toast-warning .toast-icon {
  color: var(--warning);
}

.toast-error {
  border-left-color: var(--danger);
}

.toast-error .toast-icon {
  color: var(--danger);
}

.toast-body {
  flex: 1;
  min-width: 0;
  word-break: break-word;
}

.toast-detail {
  margin-top: 2px;
  font-size: 12px;
  color: var(--text-3);
}
</style>
