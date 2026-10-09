<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'

/**
 * 无障碍对话框：基于原生 <dialog>.showModal()，背景内容 inert，焦点限制在对话框内；
 * 打开时把焦点移入（initialFocus 或第一个可聚焦元素），关闭后返回触发元素。
 * persistent 时 Esc 与背景点击不会关闭（用于未保存确认、不可中断的提交等）。
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    size?: 'sm' | 'md' | 'lg' | 'xl' | 'full'
    persistent?: boolean
    /** CSS 选择器，打开后聚焦的元素 */
    initialFocus?: string
    hideClose?: boolean
    /** 背景点击可关闭（仅非 persistent） */
    closeOnBackdrop?: boolean
  }>(),
  { size: 'md', persistent: false, initialFocus: undefined, hideClose: false, closeOnBackdrop: false },
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'close'): void
}>()

const dialogEl = ref<HTMLDialogElement | null>(null)
const titleId = `dlg-title-${Math.random().toString(36).slice(2, 9)}`
let returnFocus: HTMLElement | null = null

function focusInitial() {
  const el = dialogEl.value
  if (!el) return
  const target =
    (props.initialFocus ? el.querySelector<HTMLElement>(props.initialFocus) : null) ??
    el.querySelector<HTMLElement>('[data-autofocus]') ??
    el.querySelector<HTMLElement>(
      '.dialog-body input:not([disabled]), .dialog-body textarea:not([disabled]), .dialog-body select:not([disabled]), .dialog-footer button:not([disabled])',
    ) ??
    el.querySelector<HTMLElement>('button:not([disabled])')
  target?.focus()
}

async function show() {
  const el = dialogEl.value
  if (!el || el.open) return
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  try {
    el.showModal()
  } catch {
    el.setAttribute('open', '')
  }
  await nextTick()
  focusInitial()
}

function hide() {
  const el = dialogEl.value
  if (el?.open) el.close()
  const target = returnFocus
  returnFocus = null
  if (target && target.isConnected) requestAnimationFrame(() => target.focus())
}

function requestClose() {
  if (props.persistent) return
  emit('update:open', false)
  emit('close')
}

function onCancel(event: Event) {
  // Esc：persistent 时阻止关闭
  event.preventDefault()
  requestClose()
}

function onClick(event: MouseEvent) {
  if (!props.closeOnBackdrop || props.persistent) return
  if (event.target === dialogEl.value) requestClose()
}

watch(
  () => props.open,
  (open) => {
    if (open) void nextTick(show)
    else hide()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (dialogEl.value?.open) hide()
})

defineExpose({ focusInitial })
</script>

<template>
  <dialog
    ref="dialogEl"
    class="base-dialog"
    :class="`size-${size}`"
    :aria-labelledby="titleId"
    @cancel="onCancel"
    @click="onClick"
  >
    <div v-if="open" class="dialog-inner">
      <header class="dialog-header">
        <h2 :id="titleId" class="dialog-title">{{ title }}</h2>
        <slot name="header-extra" />
        <button
          v-if="!hideClose && !persistent"
          type="button"
          class="btn btn-icon"
          aria-label="关闭"
          @click="requestClose"
        >
          <AppIcon name="close" />
        </button>
      </header>
      <div class="dialog-body">
        <slot />
      </div>
      <footer v-if="$slots.footer" class="dialog-footer">
        <slot name="footer" />
      </footer>
    </div>
  </dialog>
</template>

<style scoped>
.base-dialog {
  padding: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  color: var(--text);
  box-shadow: var(--shadow-lg);
  width: min(520px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
}

.base-dialog::backdrop {
  background: var(--bg-overlay);
}

.size-sm {
  width: min(420px, calc(100vw - 32px));
}

.size-lg {
  width: min(760px, calc(100vw - 32px));
}

.size-xl {
  width: min(1080px, calc(100vw - 32px));
}

.size-full {
  width: calc(100vw - 32px);
  height: calc(100vh - 32px);
  max-height: calc(100vh - 32px);
}

.dialog-inner {
  display: flex;
  flex-direction: column;
  max-height: inherit;
  height: 100%;
}

.size-full .dialog-inner {
  height: calc(100vh - 34px);
}

.dialog-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 18px 10px;
}

.dialog-title {
  flex: 1;
  font-size: 16px;
  font-weight: 600;
}

.dialog-body {
  padding: 4px 18px 16px;
  overflow: auto;
  flex: 1;
  min-height: 0;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  padding: 12px 18px 16px;
  border-top: 1px solid var(--border);
}
</style>
