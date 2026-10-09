import { escapeRegExp } from '@/utils/text'

/** 不在这些区域内插入高亮：图表、公式、SVG、代码复制按钮等由渲染器管理的内容 */
const SKIP_SELECTOR =
  'script, style, svg, textarea, .katex, .language-math, .language-mermaid, .language-flowchart, .vditor-copy, .vditor-linenumber__rows, mark.ljmdb-hit'

export interface HighlightResult {
  marks: HTMLElement[]
}

/**
 * 在正文可见文本中查找关键词并以 <mark> 包裹。只操作文本节点，不解析 HTML。
 * 匹配不区分大小写；多个关键词时长词优先。
 */
export function highlightTerms(root: HTMLElement, terms: string[]): HighlightResult {
  clearHighlights(root)
  const cleaned = terms.map((t) => t.trim()).filter(Boolean)
  if (cleaned.length === 0) return { marks: [] }
  const pattern = new RegExp(cleaned.sort((a, b) => b.length - a.length).map(escapeRegExp).join('|'), 'giu')

  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = node.parentElement
      if (!parent || !node.nodeValue || !node.nodeValue.trim()) return NodeFilter.FILTER_REJECT
      if (parent.closest(SKIP_SELECTOR)) return NodeFilter.FILTER_REJECT
      return NodeFilter.FILTER_ACCEPT
    },
  })
  const textNodes: Text[] = []
  while (walker.nextNode()) textNodes.push(walker.currentNode as Text)

  const marks: HTMLElement[] = []
  for (const node of textNodes) {
    const text = node.nodeValue ?? ''
    pattern.lastIndex = 0
    const matches = Array.from(text.matchAll(pattern)).filter((m) => m[0])
    if (matches.length === 0) continue
    const fragment = document.createDocumentFragment()
    let cursor = 0
    for (const match of matches) {
      const start = match.index ?? 0
      if (start > cursor) fragment.appendChild(document.createTextNode(text.slice(cursor, start)))
      const mark = document.createElement('mark')
      mark.className = 'ljmdb-hit'
      mark.textContent = match[0]
      fragment.appendChild(mark)
      marks.push(mark)
      cursor = start + match[0].length
    }
    if (cursor < text.length) fragment.appendChild(document.createTextNode(text.slice(cursor)))
    node.parentNode?.replaceChild(fragment, node)
  }
  marks.forEach((mark, index) => mark.setAttribute('data-hit-index', String(index)))
  return { marks }
}

export function clearHighlights(root: HTMLElement): void {
  root.querySelectorAll('mark.ljmdb-hit').forEach((mark) => {
    const parent = mark.parentNode
    if (!parent) return
    parent.replaceChild(document.createTextNode(mark.textContent ?? ''), mark)
    parent.normalize()
  })
}

export function focusHit(marks: HTMLElement[], index: number): void {
  marks.forEach((m, i) => m.classList.toggle('ljmdb-hit--current', i === index))
  const target = marks[index]
  if (target) target.scrollIntoView({ block: 'center', behavior: 'smooth' })
}

/** 统计源码中的匹配数（用于判断命中是否只出现在图表源码、公式或链接地址中） */
export function countSourceMatches(source: string, terms: string[]): number {
  const cleaned = terms.map((t) => t.trim()).filter(Boolean)
  if (cleaned.length === 0) return 0
  const pattern = new RegExp(cleaned.map(escapeRegExp).join('|'), 'giu')
  return Array.from(source.matchAll(pattern)).filter((m) => m[0]).length
}
