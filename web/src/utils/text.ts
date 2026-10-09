/** 按 Unicode 码点拆分（后端高亮区间以码点计数） */
export function codePoints(text: string): string[] {
  return Array.from(text)
}

export function codePointLength(text: string): number {
  let n = 0
  for (const _ of text) n++
  return n
}

export interface TextSegment {
  text: string
  hit: boolean
}

/**
 * 将 `[start,end)` 码点区间转换为文本片段。区间会被裁剪、排序、合并；
 * 结果只包含纯文本，由模板以文本插值渲染，不会把片段当作 HTML。
 */
export function highlightSegments(text: string, ranges: ReadonlyArray<readonly [number, number]>): TextSegment[] {
  const chars = codePoints(text)
  const len = chars.length
  const valid = ranges
    .map(([s, e]) => [Math.max(0, Math.min(len, Math.floor(s))), Math.max(0, Math.min(len, Math.floor(e)))] as [number, number])
    .filter(([s, e]) => e > s)
    .sort((a, b) => a[0] - b[0])
  const merged: [number, number][] = []
  for (const r of valid) {
    const last = merged[merged.length - 1]
    if (last && r[0] <= last[1]) last[1] = Math.max(last[1], r[1])
    else merged.push([r[0], r[1]])
  }
  const segments: TextSegment[] = []
  let cursor = 0
  for (const [s, e] of merged) {
    if (s > cursor) segments.push({ text: chars.slice(cursor, s).join(''), hit: false })
    segments.push({ text: chars.slice(s, e).join(''), hit: true })
    cursor = e
  }
  if (cursor < len) segments.push({ text: chars.slice(cursor).join(''), hit: false })
  return segments
}

/** 在没有服务端区间时，按关键词在纯文本中计算码点区间（用于标题等） */
export function findTermRanges(text: string, terms: string[]): [number, number][] {
  const cleaned = terms.map((t) => t.trim()).filter(Boolean)
  if (cleaned.length === 0 || !text) return []
  const re = new RegExp(cleaned.sort((a, b) => b.length - a.length).map(escapeRegExp).join('|'), 'giu')
  const result: [number, number][] = []
  for (const match of text.matchAll(re)) {
    if (!match[0]) continue
    const start = codePointLength(text.slice(0, match.index))
    result.push([start, start + codePointLength(match[0])])
  }
  return result
}

export function splitSearchTerms(query: string): string[] {
  const seen = new Set<string>()
  const terms: string[] = []
  for (const raw of query.split(/\s+/)) {
    const term = raw.replace(/^["“”']+|["“”']+$/g, '').trim()
    if (!term) continue
    const key = term.toLocaleLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    terms.push(term)
  }
  return terms
}

export function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export function escapeHtml(text: string): string {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;')
}

export function truncate(text: string, maxCodePoints: number): string {
  const chars = codePoints(text)
  return chars.length <= maxCodePoints ? text : `${chars.slice(0, maxCodePoints).join('')}…`
}

export function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length
}

/** 标题校验：去除两端空白后非空，长度不超过上限（码点） */
export function validateTitle(title: string, maxChars: number): string | null {
  const trimmed = title.trim()
  if (!trimmed) return '标题不能为空'
  const len = codePointLength(trimmed)
  if (len > maxChars) return `标题最多 ${maxChars} 个字符（当前 ${len} 个）`
  return null
}

/** 生成用于锚点的 slug：保留中文、字母与数字，其他字符折叠为连字符 */
export function slugify(text: string): string {
  const slug = text
    .normalize('NFKC')
    .toLocaleLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .replace(/^-+|-+$/g, '')
  return slug || 'section'
}

/** 导出文件名清洗：移除路径分隔符与非法字符，限制长度 */
export function safeFileName(name: string, fallback = 'document'): string {
  const cleaned = name
    .replace(/[\\/:*?"<>|\u0000-\u001f]/g, '_')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^\.+/, '')
  const limited = codePoints(cleaned).slice(0, 80).join('')
  return limited || fallback
}
