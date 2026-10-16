<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import AppIcon from '@/components/base/AppIcon.vue'
import MarkdownEditor from './MarkdownEditor.vue'
import type { ContentTheme } from '@/editor/vditorSetup'
import { findSourceMatches, replaceSourceMatches } from '@/editor/sourceSearch'

const props = defineProps<{
  value: string
  source: boolean
  theme: ContentTheme
  readonly?: boolean
  maxUploadBytes: number
}>()
const emit = defineEmits<{
  (e: 'update:source', source: boolean): void
  (e: 'ready'): void
  (e: 'input', markdown: string): void
  (e: 'dom-input'): void
  (e: 'upload', files: File[]): void
  (e: 'doc-link'): void
  (e: 'layout'): void
}>()

const rich = ref<InstanceType<typeof MarkdownEditor> | null>(null)
const textarea = ref<HTMLTextAreaElement | null>(null)
const sourceText = ref(props.value)
const ready = ref(false)
let richChanged = false
let richBaseline: string | null = null
let pendingValue: string | null = null

// Preserve the original spelling until a user actually edits the rendered view.
// In source mode no Markdown parser is mounted or involved in reading/saving.
function getValue(): string | null {
  if (!ready.value) return null
  return props.source ? sourceText.value : readRichValue()
}

function readRichValue(): string {
  const value = rich.value?.getValue()
  // Toolbar commands can change Markdown before the debounced input callback.
  return value != null && (richChanged || (richBaseline !== null && value !== richBaseline)) ? value : sourceText.value
}

function markReady() {
  if (ready.value) return
  ready.value = true
  emit('ready')
}

function onRichReady() {
  if (pendingValue !== null) {
    rich.value?.setValue(pendingValue)
    pendingValue = null
  }
  richBaseline = rich.value?.getValue() ?? null
  markReady()
  emit('layout')
}

function onRichDomInput() {
  richChanged = true
  emit('dom-input')
}

function onRichInput(markdown: string) {
  if (props.source) return
  richChanged = true
  sourceText.value = markdown
  emit('input', markdown)
}

watch(() => props.source, async (source) => {
  if (source) {
    // Pull synchronously before destroying Vditor, including its debounced input.
    const markdown = readRichValue()
    if (markdown !== sourceText.value) {
      sourceText.value = markdown
      emit('input', markdown)
    }
    richChanged = false
    richBaseline = null
    pendingValue = null
    resetUndo()
  } else {
    searchOpen.value = false
    richChanged = false
    richBaseline = null
  }
  emit('layout')
  await nextTick()
  if (source) {
    markReady()
    textarea.value?.focus()
  }
})

onMounted(() => {
  if (props.source) markReady()
})

function publishSource() {
  emit('dom-input')
  emit('input', sourceText.value)
  emit('layout')
}

// One replacement (including Replace All) is one undo step.
type Snapshot = { text: string; start: number; end: number }
const undoStack = ref<Snapshot[]>([])
const redoStack = ref<Snapshot[]>([])
let lastTyping = 0

function snapshot(): Snapshot {
  return { text: sourceText.value, start: textarea.value?.selectionStart ?? 0, end: textarea.value?.selectionEnd ?? 0 }
}

function resetUndo() {
  undoStack.value = []
  redoStack.value = []
  lastTyping = 0
}

function recordUndo(groupTyping = false) {
  const now = Date.now()
  if (!groupTyping || now - lastTyping > 700 || redoStack.value.length) {
    undoStack.value.push(snapshot())
    if (undoStack.value.length > 100) undoStack.value.shift()
  }
  redoStack.value = []
  lastTyping = groupTyping ? now : 0
}

function beforeSourceInput(event: InputEvent) {
  if (props.readonly) { event.preventDefault(); return }
  if (event.inputType === 'historyUndo' || event.inputType === 'historyRedo') {
    event.preventDefault()
    undo(event.inputType === 'historyRedo')
    return
  }
  recordUndo(['insertText', 'deleteContentBackward', 'deleteContentForward', 'insertCompositionText'].includes(event.inputType))
}

function onSourceInput(event: Event) {
  if (props.readonly) return
  sourceText.value = (event.target as HTMLTextAreaElement).value
  notice.value = ''
  publishSource()
}

async function selectSource(start: number, end = start, focus = true) {
  lastTyping = 0
  await nextTick()
  const el = textarea.value
  if (!el) return
  if (focus) el.focus({ preventScroll: true })
  el.setSelectionRange(start, end)
  // Mirror wrapping so a match in a long document scrolls into view too.
  const mirror = document.createElement('div')
  const style = getComputedStyle(el)
  for (const property of ['box-sizing', 'font-family', 'font-size', 'font-weight', 'line-height', 'letter-spacing', 'padding', 'border-width', 'word-break', 'overflow-wrap', 'tab-size']) {
    mirror.style.setProperty(property, style.getPropertyValue(property))
  }
  Object.assign(mirror.style, { position: 'fixed', visibility: 'hidden', whiteSpace: 'pre-wrap', width: `${el.clientWidth}px`, borderStyle: 'solid' })
  mirror.textContent = el.value.slice(0, start)
  const marker = document.createElement('span')
  marker.textContent = el.value.slice(start, end) || '\u200b'
  mirror.append(marker)
  document.body.append(mirror)
  el.scrollTop = Math.max(0, marker.offsetTop - mirror.offsetTop - el.clientHeight / 2)
  mirror.remove()
}

function undo(redo = false) {
  if (props.readonly) return
  const from = redo ? redoStack : undoStack
  const to = redo ? undoStack : redoStack
  const previous = from.value.pop()
  if (!previous) return
  to.value.push(snapshot())
  sourceText.value = previous.text
  lastTyping = 0
  notice.value = ''
  publishSource()
  void selectSource(previous.start, previous.end)
}

const searchOpen = ref(false)
const findInput = ref<HTMLInputElement | null>(null)
const query = ref('')
const replacement = ref('')
const caseSensitive = ref(false)
const matches = computed(() => findSourceMatches(sourceText.value, query.value, caseSensitive.value))
const matchIndex = ref(-1)
const notice = ref('')
const matchStatus = computed(() => {
  if (!query.value) return '输入查找内容'
  if (!matches.value.length) return '无匹配结果'
  return `${matchIndex.value >= 0 ? matchIndex.value + 1 : 0} / ${matches.value.length} 处`
})

async function openSearch() {
  const selection = props.source
    ? textarea.value?.value.slice(textarea.value.selectionStart, textarea.value.selectionEnd)
    : window.getSelection()?.toString()
  if (selection && !selection.includes('\n')) query.value = selection
  emit('update:source', true)
  searchOpen.value = true
  await nextTick()
  findInput.value?.focus()
  findInput.value?.select()
}

function closeSearch() {
  searchOpen.value = false
  textarea.value?.focus()
}

watch([query, caseSensitive], () => {
  matchIndex.value = matches.value.length ? 0 : -1
  notice.value = ''
  const match = matches.value[0]
  if (match) void selectSource(match.start, match.end, false)
})

watch(sourceText, () => {
  matchIndex.value = matches.value.length ? Math.min(Math.max(matchIndex.value, 0), matches.value.length - 1) : -1
})

function moveMatch(delta: number, keepFocus = false) {
  const count = matches.value.length
  if (!count) return
  matchIndex.value = matchIndex.value < 0 ? (delta > 0 ? 0 : count - 1) : (matchIndex.value + delta + count) % count
  const match = matches.value[matchIndex.value]!
  notice.value = ''
  void selectSource(match.start, match.end, !keepFocus)
}

function replace(all: boolean) {
  if (props.readonly || !matches.value.length) return
  const index = Math.max(0, matchIndex.value)
  const match = matches.value[index]!
  const count = all ? matches.value.length : 1
  recordUndo()
  sourceText.value = replaceSourceMatches(sourceText.value, all ? matches.value : [match], replacement.value)
  publishSource()
  notice.value = `已替换 ${count} 处`
  if (all) {
    matchIndex.value = -1
    void selectSource(match.start, match.start + replacement.value.length)
  } else {
    const nextOffset = match.start + replacement.value.length
    let next = matches.value.findIndex((item) => item.start >= nextOffset)
    if (next < 0 && matches.value.length) next = 0
    matchIndex.value = next
    const nextMatch = matches.value[next]
    void selectSource(nextMatch?.start ?? nextOffset, nextMatch?.end ?? nextOffset)
  }
}

function onKeydown(event: KeyboardEvent) {
  if (event.isComposing) return
  const key = event.key.toLowerCase()
  if ((event.ctrlKey || event.metaKey) && !event.altKey) {
    if (key === 'f' || key === 'h') {
      event.preventDefault()
      void openSearch()
    } else if (props.source && event.target === textarea.value && (key === 'z' || key === 'y')) {
      event.preventDefault()
      undo(key === 'y' || event.shiftKey)
    }
  }
  if (event.key === 'Escape' && searchOpen.value) {
    event.preventDefault()
    event.stopPropagation()
    closeSearch()
  }
  if (event.target === textarea.value && /^(Arrow|Home|End|Page)/.test(event.key)) lastTyping = 0
}

function onSourceFiles(event: ClipboardEvent | DragEvent) {
  const data = 'clipboardData' in event ? event.clipboardData : event.dataTransfer
  if (!data?.files.length) return
  event.preventDefault()
  if (!props.readonly) emit('upload', Array.from(data.files))
}

function setValue(markdown: string, clearStack = true) {
  if (!clearStack) recordUndo()
  sourceText.value = markdown
  richChanged = false
  if (clearStack) resetUndo()
  if (!props.source) {
    if (rich.value?.isReady()) {
      rich.value.setValue(markdown, clearStack)
      richBaseline = rich.value.getValue()
    } else pendingValue = markdown
  }
  emit('layout')
}

defineExpose({
  isReady: () => ready.value,
  getValue,
  setValue,
  insertValue: (markdown: string) => {
    if (props.readonly) return false
    if (!props.source) return rich.value?.insertValue(markdown) ?? false
    const el = textarea.value
    if (!el) return false
    recordUndo()
    const start = el.selectionStart
    sourceText.value = sourceText.value.slice(0, start) + markdown + sourceText.value.slice(el.selectionEnd)
    publishSource()
    void selectSource(start + markdown.length)
    return true
  },
  focus: () => props.source ? textarea.value?.focus() : rich.value?.focus(),
  getEditElement: () => props.source ? textarea.value : rich.value?.getEditElement(),
  openSearch,
})
</script>

<template>
  <div class="article-editor" @keydown.capture="onKeydown">
    <div v-if="source" class="source-toolbar">
      <span>Markdown 原文 · 直接编辑，不渲染</span>
      <span class="toolbar-spacer" />
      <button type="button" class="btn btn-sm" :disabled="readonly || !undoStack.length" @click="undo()">撤销</button>
      <button type="button" class="btn btn-sm" :disabled="readonly || !redoStack.length" @click="undo(true)">重做</button>
    </div>
    <div v-if="source && searchOpen" class="editor-search" role="search" aria-label="在正文源码中查找和替换">
      <div class="search-row">
        <input ref="findInput" v-model="query" class="input" aria-label="查找内容" placeholder="查找原文（Enter 下一处）" @keydown.enter.prevent="moveMatch($event.shiftKey ? -1 : 1, true)" />
        <span class="match-status" role="status" aria-live="polite">{{ matchStatus }}</span>
        <button type="button" class="btn btn-icon btn-sm" aria-label="上一处匹配" :disabled="!matches.length" @click="moveMatch(-1)"><AppIcon name="chevron-up" /></button>
        <button type="button" class="btn btn-icon btn-sm" aria-label="下一处匹配" :disabled="!matches.length" @click="moveMatch(1)"><AppIcon name="chevron-down" /></button>
        <button type="button" class="btn btn-icon btn-sm" aria-label="关闭查找和替换" @click="closeSearch"><AppIcon name="close" /></button>
      </div>
      <div class="search-row">
        <input v-model="replacement" class="input" aria-label="替换为" placeholder="替换为（留空则删除）" @keydown.enter.prevent="replace(false)" />
        <button type="button" class="btn btn-sm" :disabled="readonly || !matches.length" @click="replace(false)">替换</button>
        <button type="button" class="btn btn-sm" :disabled="readonly || !matches.length" @click="replace(true)">全部替换</button>
      </div>
      <div class="search-options">
        <label><input v-model="caseSensitive" type="checkbox" /> 区分大小写</label>
        <span>按原文查找，包含 Markdown 标记</span>
        <span role="status" aria-live="polite">{{ notice }}</span>
      </div>
    </div>
    <textarea
      v-if="source"
      ref="textarea"
      :value="sourceText"
      class="textarea source-textarea"
      aria-label="Markdown 源码编辑区"
      :readonly="readonly"
      spellcheck="false"
      autocapitalize="off"
      autocomplete="off"
      @beforeinput="beforeSourceInput"
      @input="onSourceInput"
      @pointerdown="lastTyping = 0"
      @paste="onSourceFiles"
      @dragover="($event.dataTransfer?.types.includes('Files')) && $event.preventDefault()"
      @drop="onSourceFiles"
    />
    <MarkdownEditor
      v-else
      ref="rich"
      :value="sourceText"
      :theme="theme"
      :readonly="readonly"
      :max-upload-bytes="maxUploadBytes"
      @ready="onRichReady"
      @dom-input="onRichDomInput"
      @input="onRichInput"
      @upload="emit('upload', $event)"
      @doc-link="emit('doc-link')"
      @layout="emit('layout')"
    />
  </div>
</template>

<style scoped>
.article-editor { min-width: 0; }
.source-toolbar, .search-row, .search-options { display: flex; align-items: center; gap: 6px; }
.source-toolbar { flex-wrap: wrap; padding: 8px 0; color: var(--text-3); font-size: 12px; }
.toolbar-spacer { flex: 1; }
.editor-search { padding: 10px; margin-bottom: 8px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg-elevated); }
.search-row + .search-row, .search-options { margin-top: 8px; }
.search-row .input { flex: 1; min-width: 0; font-size: 13px; }
.search-row .btn, .match-status { flex: none; }
.match-status { color: var(--text-3); font-size: 12px; }
.search-options { flex-wrap: wrap; gap: 8px 16px; font-size: 12px; color: var(--text-3); }
.search-options label { display: inline-flex; align-items: center; gap: 4px; color: var(--text); }
.source-textarea { display: block; height: 65vh; min-height: 420px; padding: 16px; font-family: var(--font-mono); font-size: 14px; line-height: 1.75; tab-size: 4; overflow-wrap: break-word; }
@media (max-width: 600px) {
  .search-row { flex-wrap: wrap; }
  .search-row .input { flex-basis: 100%; }
  .source-textarea { padding: 12px; font-size: 16px; }
}
</style>
