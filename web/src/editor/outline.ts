import { slugify } from '@/utils/text'

export interface OutlineItem {
  key: string
  level: number
  text: string
  /** 唯一锚点（重复标题生成不同锚点） */
  anchor: string
  element: HTMLElement
}

const EXCLUDE = '.vditor-ir__preview, pre, code, svg, .language-mermaid, .language-math, .katex, .vditor-reset--error'

function headingText(el: HTMLElement): string {
  const clone = el.cloneNode(true) as HTMLElement
  clone.querySelectorAll('[class*="vditor-ir__marker"], .vditor-anchor, .vditor-ir__link').forEach((n) => n.remove())
  return (clone.textContent ?? '').replace(/\u200b/g, '').replace(/\s+/g, ' ').trim()
}

/**
 * 从渲染后的正文（阅读视图）或 ir 编辑区提取标题。
 * assignIds 为 true 时（阅读视图）为标题写入唯一 id；编辑区不修改 Vditor 的 DOM。
 */
export function collectOutline(root: HTMLElement | null | undefined, assignIds: boolean): OutlineItem[] {
  if (!root) return []
  const used = new Map<string, number>()
  const items: OutlineItem[] = []
  root.querySelectorAll<HTMLElement>('h1, h2, h3, h4, h5, h6').forEach((el, index) => {
    const excluded = el.parentElement?.closest(EXCLUDE)
    if (excluded && root.contains(excluded)) return
    const text = headingText(el)
    if (!text) return
    const base = `heading-${slugify(text)}`
    const count = used.get(base) ?? 0
    used.set(base, count + 1)
    const anchor = count === 0 ? base : `${base}-${count + 1}`
    if (assignIds) {
      el.id = anchor
      el.setAttribute('tabindex', '-1')
    }
    items.push({ key: `${index}-${anchor}`, level: Number(el.tagName.slice(1)), text, anchor, element: el })
  })
  return items
}

/** 滚动到标题并移动焦点（阅读视图聚焦标题；编辑区把光标放到标题末尾） */
export function focusHeading(item: OutlineItem, editable: boolean): void {
  const el = item.element
  if (!el.isConnected) return
  el.scrollIntoView({ block: 'start', behavior: 'smooth' })
  if (editable) {
    const selection = window.getSelection()
    const host = el.closest<HTMLElement>('[contenteditable="true"]')
    if (!selection || !host) return
    host.focus({ preventScroll: true })
    const range = document.createRange()
    range.selectNodeContents(el)
    range.collapse(false)
    selection.removeAllRanges()
    selection.addRange(range)
  } else {
    el.focus({ preventScroll: true })
  }
}

/** 当前滚动位置所在的标题 */
export function activeHeadingIndex(items: OutlineItem[], container: HTMLElement | null, offset = 96): number {
  if (!container || items.length === 0) return -1
  const top = container.getBoundingClientRect().top + offset
  let active = -1
  for (let i = 0; i < items.length; i++) {
    const rect = items[i]!.element.getBoundingClientRect()
    if (rect.top <= top) active = i
    else break
  }
  return active
}
