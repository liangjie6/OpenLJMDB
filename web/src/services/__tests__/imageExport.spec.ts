import { afterEach, describe, expect, it, vi } from 'vitest'
import { planImageSlices } from '@/utils/pagination'
import { checkSinglePageSafety, waitForRenderingComplete } from '../imageExport'

afterEach(() => vi.useRealTimers())

describe('image page geometry', () => {
  it('covers the full document exactly once with a smaller first page', () => {
    const slices = planImageSlices(4300, 650, 900)
    expect(slices[0]).toEqual({ start: 0, end: 650 })
    expect(slices.at(-1)?.end).toBe(4300)
    slices.forEach((slice, i) => {
      expect(slice.end - slice.start).toBeLessThanOrEqual(i === 0 ? 650 : 900)
      expect(slice.start).toBe(i === 0 ? 0 : slices[i - 1]!.end)
    })
  })

  it('moves a boundary before overlapping text and inline formula boxes', () => {
    const ranges = [{ start: 780, end: 820 }, { start: 760, end: 790 }]
    const slices = planImageSlices(1800, 800, 800, ranges)
    expect(slices[0]?.end).toBe(760)
    expect(slices.at(-1)?.end).toBe(1800)
    expect(slices.every(slice => ranges.every(range => !(range.start < slice.end && range.end > slice.end)))).toBe(true)
  })

  it('prefers a paragraph boundary that fits on the page', () => {
    expect(planImageSlices(1800, 800, 800, [], [620, 910])[0]?.end).toBe(620)
  })

  it('rejects an unbreakable oversized element instead of clipping it', () => {
    expect(() => planImageSlices(1300, 800, 800, [{ start: 0, end: 1200 }])).toThrow('超出单页')
  })

  it('keeps an empty document exportable and rejects invalid dimensions', () => {
    expect(planImageSlices(0, 800, 800)).toEqual([{ start: 0, end: 0 }])
    expect(() => planImageSlices(10, 0, 800)).toThrow()
    expect(() => planImageSlices(Infinity, 800, 800)).toThrow()
  })

  it('checks the output pixel ratio, width and total area for long images', () => {
    expect(checkSinglePageSafety(7900).safe).toBe(true)
    expect(checkSinglePageSafety(8001).safe).toBe(false)
    expect(checkSinglePageSafety(1000, 9000).safe).toBe(false)
    expect(checkSinglePageSafety(5000, 5000).safe).toBe(false)
    expect(checkSinglePageSafety(NaN).safe).toBe(false)
  })
})

describe('asynchronous document rendering', () => {
  it('does not consider data-math alone to be a rendered formula', async () => {
    vi.useFakeTimers()
    const root = document.createElement('div')
    root.innerHTML = '<span data-math="E=mc^2"></span>'
    const assertion = expect(waitForRenderingComplete(root, 100)).rejects.toThrow('公式或图表')
    await vi.advanceTimersByTimeAsync(180)
    await assertion
  })

  it('waits for a Mermaid SVG and KaTeX nodes before resolving', async () => {
    vi.useFakeTimers()
    const root = document.createElement('div')
    root.innerHTML = '<div class="language-mermaid"></div><span data-math="x"></span>'
    let complete = false
    const waiting = waitForRenderingComplete(root, 1000).then(() => { complete = true })
    await vi.advanceTimersByTimeAsync(60)
    expect(complete).toBe(false)
    root.querySelector('.language-mermaid')!.innerHTML = '<svg></svg>'
    root.querySelector('[data-math]')!.innerHTML = '<span class="katex">x</span>'
    await vi.advanceTimersByTimeAsync(60)
    await waiting
    expect(complete).toBe(true)
  })

  it('stops waiting when the user cancels', async () => {
    const controller = new AbortController()
    controller.abort()
    await expect(waitForRenderingComplete(document.createElement('div'), 1000, controller.signal)).rejects.toThrow('取消')
  })
})
