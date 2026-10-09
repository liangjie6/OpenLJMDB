// @ts-ignore - diff-match-patch 没有类型定义
import DiffMatchPatch from 'diff-match-patch'

export type DiffKind = 'equal' | 'insert' | 'delete'

export interface DiffLine {
  kind: DiffKind
  text: string
  oldNo: number | null
  newNo: number | null
}

export interface DiffResult {
  lines: DiffLine[]
  inserted: number
  deleted: number
  identical: boolean
}

/** 行级差异（old → new），用于草稿/冲突比较与历史对比 */
export function lineDiff(oldText: string, newText: string): DiffResult {
  const dmp = new DiffMatchPatch()
  const { chars1, chars2, lineArray } = dmp.diff_linesToChars_(oldText, newText)
  const diffs = dmp.diff_main(chars1, chars2, false)
  dmp.diff_charsToLines_(diffs, lineArray)
  const lines: DiffLine[] = []
  let oldNo = 1
  let newNo = 1
  let inserted = 0
  let deleted = 0
  for (const [op, chunk] of diffs) {
    const parts = chunk.split('\n')
    if (parts[parts.length - 1] === '') parts.pop()
    for (const text of parts) {
      if (op === DiffMatchPatch.DIFF_EQUAL) {
        lines.push({ kind: 'equal', text, oldNo: oldNo++, newNo: newNo++ })
      } else if (op === DiffMatchPatch.DIFF_INSERT) {
        lines.push({ kind: 'insert', text, oldNo: null, newNo: newNo++ })
        inserted++
      } else {
        lines.push({ kind: 'delete', text, oldNo: oldNo++, newNo: null })
        deleted++
      }
    }
  }
  return { lines, inserted, deleted, identical: inserted === 0 && deleted === 0 }
}

/** 折叠长的未变化区域，只保留上下文 */
export function collapseEqual(lines: DiffLine[], context = 3): Array<DiffLine | { kind: 'gap'; count: number }> {
  const out: Array<DiffLine | { kind: 'gap'; count: number }> = []
  let i = 0
  while (i < lines.length) {
    if (lines[i]!.kind !== 'equal') {
      out.push(lines[i]!)
      i++
      continue
    }
    let j = i
    while (j < lines.length && lines[j]!.kind === 'equal') j++
    const run = lines.slice(i, j)
    const atStart = i === 0
    const atEnd = j === lines.length
    const keepHead = atStart ? 0 : context
    const keepTail = atEnd ? 0 : context
    if (run.length > keepHead + keepTail + 1) {
      out.push(...run.slice(0, keepHead))
      out.push({ kind: 'gap', count: run.length - keepHead - keepTail })
      out.push(...run.slice(run.length - keepTail))
    } else {
      out.push(...run)
    }
    i = j
  }
  return out
}
