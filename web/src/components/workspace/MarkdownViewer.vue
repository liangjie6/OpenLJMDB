<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import VditorMethod from 'vditor/dist/method'
import { highlightCodeBlocks, previewOptions, type ContentTheme } from '@/editor/vditorSetup'
import { collectOutline, type OutlineItem } from '@/editor/outline'
import { clearHighlights, focusHit, highlightTerms } from '@/editor/searchHighlight'
import { parseDocumentLink } from '@/utils/links'
import { escapeHtml } from '@/utils/text'

/**
 * 只读阅读视图：与编辑器相同的 Lute 版本与 Markdown 选项渲染，输出经 DOMPurify 净化。
 * 任务列表复选框只读；内部文档链接在应用内跳转；搜索关键词在可见文本中高亮。
 */
const props = withDefaults(defineProps<{
  markdown: string
  theme: ContentTheme
  terms?: string[]
}>(), {
  terms: () => []
})

const emit = defineEmits<{
  (e: 'outline', items: OutlineItem[]): void
  (e: 'hits', count: number): void
  (e: 'rendered'): void
  (e: 'render-error', message: string): void
}>()

const router = useRouter()
const container = ref<HTMLDivElement | null>(null)
const rendering = ref(true)
let current: HTMLDivElement | null = null
let seq = 0
let marks: HTMLElement[] = []

async function render() {
  const el = container.value
  if (!el) return
  const token = ++seq
  rendering.value = true
  const next = document.createElement('div')
  next.className = 'doc-content'
  next.hidden = true
  el.appendChild(next)
  try {
    await VditorMethod.preview(next, props.markdown, previewOptions(props.theme))
    if (token !== seq) {
      next.remove()
      return
    }
    await highlightCodeBlocks(next).catch(() => undefined)
    next.querySelectorAll<HTMLInputElement>('input[type="checkbox"]').forEach((box) => {
      box.disabled = true
      box.setAttribute('aria-readonly', 'true')
    })
    if (token !== seq) {
      next.remove()
      return
    }
    for (const child of Array.from(el.children)) if (child !== next) child.remove()
    next.hidden = false
    current = next
    emit('outline', collectOutline(next, true))
    applyHighlights()
    emit('rendered')
  } catch (error) {
    if (token !== seq) return
    next.remove()
    const message = error instanceof Error ? error.message : String(error)
    // 渲染失败不影响获取 Markdown：显示源码
    const fallback = document.createElement('pre')
    fallback.className = 'doc-render-fallback'
    fallback.innerHTML = escapeHtml(props.markdown)
    for (const child of Array.from(el.children)) child.remove()
    el.appendChild(fallback)
    current = null
    emit('outline', [])
    emit('render-error', message)
  } finally {
    if (token === seq) rendering.value = false
  }
}

function applyHighlights() {
  if (!current) return
  clearHighlights(current)
  marks = props.terms.length ? highlightTerms(current, props.terms).marks : []
  emit('hits', marks.length)
}

function onClick(event: MouseEvent) {
  const target = event.target as HTMLElement | null
  const anchor = target?.closest('a')
  if (!anchor || !container.value?.contains(anchor)) return
  const href = anchor.getAttribute('href') ?? ''
  if (href.startsWith('#')) {
    // 脚注等页内锚点：在正文内滚动，不改变路由
    event.preventDefault()
    const id = decodeURIComponent(href.slice(1))
    const dest = id ? container.value.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`) : null
    dest?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    return
  }
  const docId = parseDocumentLink(href)
  if (docId && !event.ctrlKey && !event.metaKey && !event.shiftKey && event.button === 0) {
    event.preventDefault()
    void router.push(`/documents/${docId}`)
  }
}

function onImageError(event: Event) {
  const img = event.target
  if (!(img instanceof HTMLImageElement) || img.dataset.ljmdbBroken) return
  img.dataset.ljmdbBroken = '1'
  // 图片加载失败：展示替代文本、文件名与重试入口，不阻塞正文阅读
  const box = document.createElement('span')
  box.className = 'ljmdb-img-broken'
  const src = img.getAttribute('src') ?? ''
  const name = img.getAttribute('alt') || decodeURIComponent(src.split('/').filter(Boolean).slice(-2, -1)[0] ?? src)
  const label = document.createElement('span')
  label.textContent = `图片无法加载：${name}`
  const retry = document.createElement('button')
  retry.type = 'button'
  retry.className = 'btn btn-sm'
  retry.textContent = '重试'
  retry.addEventListener('click', () => {
    delete img.dataset.ljmdbBroken
    img.src = `${src}${src.includes('?') ? '&' : '?'}retry=${Date.now()}`
    box.replaceWith(img)
  })
  box.append(label, retry)
  img.replaceWith(box)
}

onMounted(() => {
  container.value?.addEventListener('error', onImageError, true)
  void render()
})

onBeforeUnmount(() => {
  seq++
  container.value?.removeEventListener('error', onImageError, true)
})

watch(() => [props.markdown, props.theme], () => void render())
watch(
  () => props.terms.join('\u0000'),
  () => applyHighlights(),
)

defineExpose({
  focusHit: (index: number) => focusHit(marks, index),
  getRoot: () => current,
  isRendering: () => rendering.value,
})
</script>

<template>
  <div class="doc-viewer">
    <div v-if="rendering && !current" class="viewer-skeleton" aria-hidden="true">
      <div class="skeleton" style="height: 16px; width: 82%" />
      <div class="skeleton" style="height: 16px; width: 64%" />
      <div class="skeleton" style="height: 16px; width: 74%" />
    </div>
    <div ref="container" class="viewer-content" :aria-busy="rendering" @click="onClick" />
  </div>
</template>

<style scoped>
.viewer-skeleton {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 8px 0;
}

.viewer-content :deep(.doc-render-fallback) {
  white-space: pre-wrap;
  font-family: var(--font-mono);
  font-size: 13px;
  padding: 12px;
  background: var(--bg-soft);
  border-radius: var(--radius);
}
</style>
