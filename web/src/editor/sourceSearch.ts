export interface SourceMatch {
  start: number
  end: number
}

/** Literal, non-overlapping matches; keep offsets in the original UTF-16 source. */
export function findSourceMatches(source: string, query: string, caseSensitive: boolean): SourceMatch[] {
  if (!query) return []
  const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return Array.from(source.matchAll(new RegExp(escaped, caseSensitive ? 'gu' : 'giu')), (match) => ({
    start: match.index,
    end: match.index + match[0].length,
  }))
}

/** Replacement text is literal too, including $&, $1 and Markdown punctuation. */
export function replaceSourceMatches(source: string, matches: SourceMatch[], replacement: string): string {
  const parts: string[] = []
  let offset = 0
  for (const match of matches) {
    parts.push(source.slice(offset, match.start), replacement)
    offset = match.end
  }
  parts.push(source.slice(offset))
  return parts.join('')
}
