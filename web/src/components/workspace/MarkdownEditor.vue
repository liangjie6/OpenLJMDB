<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import Vditor from 'vditor'
import { editorOptions, hljsStyle, loadScript, VDITOR_CDN, type ContentTheme } from '@/editor/vditorSetup'

/**
 * Vditor 即时渲染（ir）编辑器封装。首版只开放 ir 模式。
 * - dom-input：编辑区原生输入事件（立即触发，用于标记修改与草稿节流）
 * - input：Vditor 回调的 Markdown（编辑器内部约 800ms 防抖）
 * - upload：粘贴 / 拖入 / 工具栏选择的文件，由上层上传队列处理
 */
const props = defineProps<{
  value: string
  theme: ContentTheme
  readonly?: boolean
  maxUploadBytes: number
}>()

const emit = defineEmits<{
  (e: 'ready'): void
  (e: 'input', markdown: string): void
  (e: 'dom-input'): void
  (e: 'upload', files: File[]): void
  (e: 'doc-link'): void
  (e: 'layout'): void
}>()

const host = ref<HTMLDivElement | null>(null)
const instance = shallowRef<Vditor | null>(null)
const ready = ref(false)
let destroyed = false
let editElement: HTMLElement | null = null
let observer: MutationObserver | null = null

function onDomInput() {
  emit('dom-input')
}

function onPaste(event: ClipboardEvent) {
  // Get clipboard data
  const clipboardData = event.clipboardData
  if (!clipboardData) return

  const text = clipboardData.getData('text/plain')
  if (!text) return

  // Check if the pasted content contains complete markdown code blocks
  // Pattern: ``` followed by content and closing ```
  const hasCompleteCodeBlocks = /```[\s\S]*?```/.test(text)

  if (hasCompleteCodeBlocks && instance.value) {
    // Prevent default paste behavior
    event.preventDefault()

    // Insert the text directly without auto-pairing
    instance.value.insertValue(text)
  }
}

onMounted(async () => {
  // 首次打开编辑器时也能识别未标注语言的代码，无需先进入阅读模式。
  await loadScript(`${VDITOR_CDN}/dist/js/highlight.js/highlight.min.js?v=11.7.0`, 'vditorHljsScript').catch(() => undefined)
  if (destroyed) return
  if (!host.value) return
  const vditor = new Vditor(
    host.value,
    editorOptions({
      value: props.value,
      theme: props.theme,
      maxUploadBytes: props.maxUploadBytes,
      uploadHandler: (files) => {
        emit('upload', files)
        return null
      },
      onInput: (markdown) => {
        if (!destroyed) emit('input', markdown)
      },
      onAfter: () => {
        if (destroyed) return
        ready.value = true
        editElement = (instance.value?.vditor.ir?.element as HTMLElement | undefined) ?? null
        editElement?.addEventListener('input', onDomInput)
        editElement?.addEventListener('paste', onPaste)
        editElement?.setAttribute('aria-label', '正文编辑区')
        editElement?.setAttribute('aria-multiline', 'true')
        editElement?.setAttribute('role', 'textbox')
        if (props.readonly) instance.value?.disabled()
        if (editElement) {
          // 大纲随标题变化刷新
          observer = new MutationObserver(() => emit('layout'))
          observer.observe(editElement, { childList: true, subtree: true, characterData: true })
        }
        emit('ready')
      },
      onDocLink: () => emit('doc-link'),
    }),
  )
  instance.value = vditor
})

onBeforeUnmount(() => {
  destroyed = true
  observer?.disconnect()
  editElement?.removeEventListener('input', onDomInput)
  editElement?.removeEventListener('paste', onPaste)
  try {
    instance.value?.destroy()
  } catch {
    // 编辑器尚未完成初始化时销毁可能抛错，忽略
  }
  instance.value = null
})

watch(
  () => props.theme,
  (theme) => {
    if (!ready.value) return
    instance.value?.setTheme(theme === 'dark' ? 'dark' : 'classic', theme, hljsStyle(theme), `${VDITOR_CDN}/dist/css/content-theme`)
  },
)

watch(
  () => props.readonly,
  (readonly) => {
    if (!ready.value) return
    if (readonly) instance.value?.disabled()
    else instance.value?.enable()
  },
)

defineExpose({
  isReady: () => ready.value,
  getValue: (): string | null => (ready.value && instance.value ? instance.value.getValue() : null),
  /** 程序化设置内容（不触发 input 回调）；clearStack 清空撤销栈 */
  setValue: (markdown: string, clearStack = true) => {
    if (ready.value) instance.value?.setValue(markdown, clearStack)
  },
  insertValue: (markdown: string) => {
    if (!ready.value || !instance.value) return false
    instance.value.insertValue(markdown)
    return true
  },
  focus: () => instance.value?.focus(),
  getEditElement: () => editElement,
})
</script>

<template>
  <div class="markdown-editor">
    <div ref="host" class="markdown-editor-host" />
  </div>
</template>

<style scoped>
.markdown-editor {
  min-height: 420px;
}
</style>
