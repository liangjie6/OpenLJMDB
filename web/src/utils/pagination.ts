/** Page geometry for document image exports, in CSS pixels. */
export interface PageFormatPreset {
  id: 'a4' | 'card' | 'compact'
  name: string
  width: number
  targetHeight: number
  desc: string
}

export const PAGE_PRESETS: PageFormatPreset[] = [
  {
    id: 'a4',
    name: 'A4 比例 (1:1.414)',
    width: 800,
    targetHeight: 1130,
    desc: '标准纵向 A4，适合阅读、保存与打印',
  },
  {
    id: 'card',
    name: '手机长卡片 (9:16)',
    width: 720,
    targetHeight: 1280,
    desc: '适合移动端屏幕全屏浏览与聊天分享',
  },
  {
    id: 'compact',
    name: '方正卡片 (3:4)',
    width: 800,
    targetHeight: 1060,
    desc: '视觉比例紧凑优美，适合社交媒体发表',
  },
]


export interface ImageSlice { start: number; end: number }
export interface ProtectedRange { start: number; end: number }

/** Split a laid out document without drawing a canvas taller than one page.
 * Text lines, formulas and media are protected; block boundaries are preferred.
 */
export function planImageSlices(
  height: number,
  firstCapacity: number,
  capacity: number,
  protectedRanges: ProtectedRange[] = [],
  blockBreaks: number[] = [],
): ImageSlice[] {
  if (![height, firstCapacity, capacity].every(Number.isFinite) || height < 0 || firstCapacity <= 0 || capacity <= 0) {
    throw new Error('无效的页面尺寸')
  }
  if (height === 0) return [{ start: 0, end: 0 }]
  const slices: ImageSlice[] = []
  const isSafe = (y: number) => !protectedRanges.some(range => range.start < y && range.end > y)
  let start = 0
  while (start < height) {
    const available = slices.length === 0 ? firstCapacity : capacity
    const limit = Math.min(height, start + available)
    let end = limit
    if (limit < height) {
      const preferred = blockBreaks.filter(y => y > start + available * 0.45 && y <= limit && isSafe(y))
      if (preferred.length) end = Math.max(...preferred)
      else {
        // Moving to the start of an intersecting line can intersect an earlier
        // overlapping inline box, so repeat until the entire boundary is clear.
        for (let i = 0; i <= protectedRanges.length; i++) {
          const crossing = protectedRanges.filter(range => range.start < end && range.end > end)
          if (!crossing.length) break
          end = Math.min(...crossing.map(range => range.start))
        }
      }
    }
    if (end <= start + 0.5) throw new Error('有内容超出单页可用高度，请选择更高的页面比例或缩小图片后重试。')
    slices.push({ start, end })
    start = end
  }
  return slices
}
