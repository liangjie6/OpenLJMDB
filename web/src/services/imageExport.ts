/**
 * 图片导出核心服务
 */

import '@/styles/imageExport.css'
import { safeFileName } from '@/utils/text'
import { triggerDownload } from '@/utils/download'
import { PAGE_PRESETS, planImageSlices, type PageFormatPreset, type ProtectedRange } from '@/utils/pagination'

export interface ImageBackgroundTheme {
  id: string
  name: string
  desc: string
  bg: string
  bgSoft: string
  bgElevated: string
  text: string
  textSecondary: string
  textMuted: string
  border: string
  borderStrong: string
  info: string
  primary: string
  codeBg: string
  codeHeaderBg: string
  codeBorder: string
  codeText: string
  codeMuted: string
  codeKeyword: string
  codeString: string
  codeNumber: string
  codeFunction: string
  codeVariable: string
  viewerTheme: 'light' | 'dark'
  swatchBg: string
  swatchBorder: string
}

export const BACKGROUND_THEMES: ImageBackgroundTheme[] = [
  {
    id: 'pure-white',
    name: '纯白',
    desc: '清晰简约，适合打印与文档归档',
    bg: '#ffffff',
    bgSoft: '#fafafa',
    bgElevated: '#ffffff',
    text: '#1f2328',
    textSecondary: '#656d76',
    textMuted: '#8c959f',
    border: '#d0d7de',
    borderStrong: '#afb8c1',
    info: '#0969da',
    primary: '#00a35f',
    codeBg: '#f6f8fa',
    codeHeaderBg: '#eaeef2',
    codeBorder: '#d0d7de',
    codeText: '#24292f',
    codeMuted: '#57606a',
    codeKeyword: '#cf222e',
    codeString: '#0a3069',
    codeNumber: '#953800',
    codeFunction: '#8250df',
    codeVariable: '#953800',
    viewerTheme: 'light',
    swatchBg: '#ffffff',
    swatchBorder: '#d0d7de',
  },
  {
    id: 'warm-paper',
    name: '暖纸',
    desc: '柔和米黄，纸质书般舒适护眼',
    bg: '#fbf7ee',
    bgSoft: '#f5efe0',
    bgElevated: '#fbf7ee',
    text: '#2c2724',
    textSecondary: '#7c7062',
    textMuted: '#9e9282',
    border: '#ebdcc6',
    borderStrong: '#d8c4a6',
    info: '#1d6fa5',
    primary: '#1d8348',
    codeBg: '#f2ebdb',
    codeHeaderBg: '#e8dcbe',
    codeBorder: '#e2d6bf',
    codeText: '#38312b',
    codeMuted: '#6b5f4f',
    codeKeyword: '#a03020',
    codeString: '#285838',
    codeNumber: '#8a4e10',
    codeFunction: '#305090',
    codeVariable: '#7c481e',
    viewerTheme: 'light',
    swatchBg: '#fbf7ee',
    swatchBorder: '#e2d6bf',
  },
  {
    id: 'mint-fresh',
    name: '浅绿',
    desc: '清新雅致，青竹淡韵自然格调',
    bg: '#f2f8f4',
    bgSoft: '#e7f2eb',
    bgElevated: '#f2f8f4',
    text: '#1a3026',
    textSecondary: '#577565',
    textMuted: '#7a9686',
    border: '#cee2d2',
    borderStrong: '#b5cebb',
    info: '#0e7090',
    primary: '#16a34a',
    codeBg: '#e4efe7',
    codeHeaderBg: '#d8e7dc',
    codeBorder: '#c7decb',
    codeText: '#1d362a',
    codeMuted: '#4a6756',
    codeKeyword: '#991b1b',
    codeString: '#166534',
    codeNumber: '#9a3412',
    codeFunction: '#075985',
    codeVariable: '#854d0e',
    viewerTheme: 'light',
    swatchBg: '#f2f8f4',
    swatchBorder: '#c7decb',
  },
  {
    id: 'gradient-dawn',
    name: '蓝紫渐变',
    desc: '极光晨曦微光渐变，现代感十足',
    bg: 'linear-gradient(135deg, #f0f4ff 0%, #f6f3ff 50%, #fdf2f8 100%)',
    bgSoft: '#f5f3ff',
    bgElevated: '#ffffff',
    text: '#1e1b4b',
    textSecondary: '#6366f1',
    textMuted: '#818cf8',
    border: '#e0e7ff',
    borderStrong: '#c7d2fe',
    info: '#4f46e5',
    primary: '#4338ca',
    codeBg: 'rgba(255, 255, 255, 0.85)',
    codeHeaderBg: 'rgba(240, 238, 255, 0.9)',
    codeBorder: '#d6d3e8',
    codeText: '#2e1065',
    codeMuted: '#6b7280',
    codeKeyword: '#be185d',
    codeString: '#047857',
    codeNumber: '#b45309',
    codeFunction: '#6366f1',
    codeVariable: '#9333ea',
    viewerTheme: 'light',
    swatchBg: 'linear-gradient(135deg, #e0e7ff 0%, #fdf2f8 100%)',
    swatchBorder: '#c7d2fe',
  },
  {
    id: 'midnight',
    name: '深夜',
    desc: '深邃夜空，极高对比度暗色系',
    bg: '#18191b',
    bgSoft: '#22252a',
    bgElevated: '#1f2227',
    text: '#e6edf3',
    textSecondary: '#8b949e',
    textMuted: '#6e7681',
    border: '#30363d',
    borderStrong: '#484f58',
    info: '#58a6ff',
    primary: '#2ea043',
    codeBg: '#22252a',
    codeHeaderBg: '#1b1d22',
    codeBorder: '#373e47',
    codeText: '#e6edf3',
    codeMuted: '#8b949e',
    codeKeyword: '#ff7b72',
    codeString: '#7ee787',
    codeNumber: '#ffa657',
    codeFunction: '#d2a8ff',
    codeVariable: '#ffa657',
    viewerTheme: 'dark',
    swatchBg: '#18191b',
    swatchBorder: '#484f58',
  },
]

export { PAGE_PRESETS }

export const EXPORT_PIXEL_RATIO = 2
export const MAX_OUTPUT_EDGE_PIXELS = 16000
export const MAX_OUTPUT_TOTAL_PIXELS = 32_000_000

/**
 * 校验单页长图在指定 pixelRatio 下的物理像素与边长安全性
 */
export function checkSinglePageSafety(
  height: number,
  width = 840,
  pixelRatio = EXPORT_PIXEL_RATIO,
): { safe: boolean; warning?: string } {
  const outputH = height * pixelRatio
  const outputW = width * pixelRatio
  const totalPixels = outputW * outputH

  if (!Number.isFinite(height) || !Number.isFinite(width) || height < 0 || width <= 0 || !Number.isFinite(pixelRatio) || pixelRatio <= 0 || outputH > MAX_OUTPUT_EDGE_PIXELS || outputW > MAX_OUTPUT_EDGE_PIXELS || totalPixels > MAX_OUTPUT_TOTAL_PIXELS) {
    return {
      safe: false,
      warning: `文档内容过长（导出输出高度约 ${outputH} 像素，或总像素约 ${(totalPixels / 1_000_000).toFixed(1)}M），已超出单页 Canvas 尺寸上限。请使用“多页图片”导出。`,
    }
  }

  if (outputH > 10000 || totalPixels > 16_000_000) {
    return {
      safe: true,
      warning: `文档内容较长（高度约 ${height}px），单页长图在部分低内存设备上绘制较慢，推荐选择“多页图片”导出。`,
    }
  }

  return { safe: true }
}

/**
 * 将 base64 DataURL 转换为 Uint8Array
 */
export function dataUrlToUint8Array(dataUrl: string): Uint8Array {
  const commaIdx = dataUrl.indexOf(',')
  const base64 = commaIdx >= 0 ? dataUrl.slice(commaIdx + 1) : dataUrl
  const binary = atob(base64)
  const len = binary.length
  const bytes = new Uint8Array(len)
  for (let i = 0; i < len; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

/**
 * 应用主题的全部正文与代码块色彩变量，阻断外层宿主主题干扰
 */
export function applyThemeVariables(el: HTMLElement, theme: ImageBackgroundTheme): void {
  el.style.background = theme.bg
  el.style.color = theme.text
  el.style.setProperty('--bg', theme.bg)
  el.style.setProperty('--bg-soft', theme.bgSoft)
  el.style.setProperty('--bg-elevated', theme.bgElevated)
  el.style.setProperty('--border', theme.border)
  el.style.setProperty('--border-strong', theme.borderStrong)
  el.style.setProperty('--text', theme.text)
  el.style.setProperty('--text-2', theme.textSecondary)
  el.style.setProperty('--text-3', theme.textMuted)
  el.style.setProperty('--info', theme.info)
  el.style.setProperty('--primary', theme.primary)

  el.style.setProperty('--code-bg', theme.codeBg)
  el.style.setProperty('--code-header-bg', theme.codeHeaderBg)
  el.style.setProperty('--code-border', theme.codeBorder)
  el.style.setProperty('--code-text', theme.codeText)
  el.style.setProperty('--code-muted', theme.codeMuted)
  el.style.setProperty('--code-keyword', theme.codeKeyword)
  el.style.setProperty('--code-string', theme.codeString)
  el.style.setProperty('--code-number', theme.codeNumber)
  el.style.setProperty('--code-function', theme.codeFunction)
  el.style.setProperty('--code-variable', theme.codeVariable)

  const resets = el.querySelectorAll<HTMLElement>('.vditor-reset')
  resets.forEach((r) => {
    r.style.color = theme.text
    r.style.setProperty('--code-bg', theme.codeBg)
    r.style.setProperty('--code-header-bg', theme.codeHeaderBg)
    r.style.setProperty('--code-border', theme.codeBorder)
    r.style.setProperty('--code-text', theme.codeText)
    r.style.setProperty('--code-muted', theme.codeMuted)
    r.style.setProperty('--code-keyword', theme.codeKeyword)
    r.style.setProperty('--code-string', theme.codeString)
    r.style.setProperty('--code-number', theme.codeNumber)
    r.style.setProperty('--code-function', theme.codeFunction)
    r.style.setProperty('--code-variable', theme.codeVariable)
  })
}

/**
 * 清理复制按钮与搜索高亮标记
 */
export function cleanDocumentArtifacts(root: HTMLElement): void {
  root.querySelectorAll('.ljmdb-code-copy').forEach((btn) => btn.remove())

  const hits = root.querySelectorAll('mark, .ljmdb-hit')
  hits.forEach((mark) => {
    const parent = mark.parentNode
    while (mark.firstChild) {
      parent?.insertBefore(mark.firstChild, mark)
    }
    mark.remove()
  })
}

/**
 * 等待 KaTeX 与 Mermaid 图表在源 DOM 中异步完成渲染
 */
function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw new DOMException('导出已取消', 'AbortError')
}

async function abortable<T>(promise: Promise<T>, signal?: AbortSignal, timeoutMs = 15000): Promise<T> {
  throwIfAborted(signal)
  return new Promise<T>((resolve, reject) => {
    const cleanup = () => { clearTimeout(timer); signal?.removeEventListener('abort', onAbort) }
    const onAbort = () => { cleanup(); reject(new DOMException('导出已取消', 'AbortError')) }
    const timer = setTimeout(() => { cleanup(); reject(new Error('准备图片或字体超时，请重试。')) }, timeoutMs)
    signal?.addEventListener('abort', onAbort, { once: true })
    promise.then(value => { cleanup(); resolve(value) }, error => { cleanup(); reject(error) })
  })
}

export async function waitForRenderingComplete(root: HTMLElement, timeoutMs = 15000, signal?: AbortSignal): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (true) {
    throwIfAborted(signal)
    const diagrams = [...root.querySelectorAll('.language-mermaid')]
    const formulae = [...root.querySelectorAll('[data-math], .language-math, .math')]
    const pending = diagrams.some(el => !el.querySelector('svg') && el.getAttribute('data-processed') !== 'true' && !el.classList.contains('vditor-reset--error')) ||
      formulae.some(el => !el.querySelector('.katex') && !el.classList.contains('katex') && !el.classList.contains('vditor-reset--error'))
    if (!pending) return
    if (Date.now() >= deadline) throw new Error('公式或图表尚未完成排版，请稍候重试。')
    await abortable(new Promise<void>(resolve => setTimeout(resolve, 60)), signal)
  }
}

// Font sets differ between documents (especially KaTeX). Cache by the actual
// families in this export rather than reusing the first document's font set.
const fontCSSCache = new Map<string, Promise<string>>()
export async function getSharedFontEmbedCSS(el: HTMLElement, signal?: AbortSignal): Promise<string> {
  const families = new Set<string>()
  for (const node of [el, ...el.querySelectorAll<HTMLElement>('*')]) families.add(getComputedStyle(node).fontFamily)
  const italic = [...el.querySelectorAll<HTMLElement>('*')].some(node => getComputedStyle(node).fontStyle === 'italic')
  const key = [...families].sort().join('|') + (italic ? '|italic' : '')
  let cached = fontCSSCache.get(key)
  if (!cached) {
    cached = import('html-to-image').then(async ({ getFontEmbedCSS }) => {
      const css = await getFontEmbedCSS(el)
      // The local CJK font contains six large faces. Regular and bold cover
      // intermediate weights through browser font matching; keep italic only
      // when used. KaTeX and other families retain their required faces.
      return (css.match(/@font-face\s*\{[^}]*\}/g) ?? []).filter(rule => {
        if (!rule.includes('JetBrains Maple Mono')) return true
        const weight = /font-weight:\s*(\d+)/.exec(rule)?.[1]
        return (weight === '400' || weight === '700') && (italic || !/font-style:\s*italic/.test(rule))
      }).join('\n')
    })
    fontCSSCache.set(key, cached)
    cached.catch(() => fontCSSCache.delete(key))
    if (fontCSSCache.size > 4) fontCSSCache.delete(fontCSSCache.keys().next().value!)
  }
  return abortable(cached, signal, 30000)
}

/**
 * 带有超时和 AbortSignal 的网络图片拉取
 */
async function fetchImageBlobWithTimeout(
  url: string,
  signal?: AbortSignal,
  timeoutMs = 4000,
): Promise<Blob> {
  throwIfAborted(signal)
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  const onParentAbort = () => ctrl.abort()
  if (signal) signal.addEventListener('abort', onParentAbort)

  try {
    const response = await fetch(url, {
      mode: 'cors',
      referrerPolicy: 'no-referrer',
      signal: ctrl.signal,
    })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    return await response.blob()
  } finally {
    clearTimeout(timer)
    if (signal) signal.removeEventListener('abort', onParentAbort)
  }
}

export interface ImagePrepResult {
  corsWarnings: string[]
  createdUrls: string[]
}

/**
 * 准备图片：取消懒加载、处理损坏图片、探测外部 CORS 并替换无法载入的图片
 */
export async function prepareImagesForExport(root: HTMLElement, signal?: AbortSignal, timeoutMs = 5000): Promise<ImagePrepResult> {
  throwIfAborted(signal)
  const warnings: string[] = []
  const placeholder = (element: Element, name: string) => {
    const box = document.createElement('span')
    box.className = 'export-broken-placeholder'
    box.textContent = `图片无法载入：${name}`
    element.replaceWith(box)
    warnings.push(name)
  }
  root.querySelectorAll('.ljmdb-img-broken').forEach(box => {
    const name = box.textContent?.replace('图片无法加载：', '').replace('重试', '').trim() || '图片'
    placeholder(box, name)
  })
  const images = [...root.querySelectorAll<HTMLImageElement>('img')]
  // A bounded worker pool avoids fetching all images of a long note at once.
  let next = 0
  const worker = async () => {
    while (next < images.length) {
      throwIfAborted(signal)
      const img = images[next++]!
      const src = img.getAttribute('src') || ''
      const name = img.alt || src || '图片'
      img.loading = 'eager'
      try {
        if (!src) throw new Error('图片地址为空')
        if (!src.startsWith('data:')) {
          const blob = await fetchImageBlobWithTimeout(src, signal, timeoutMs)
          // Embed once before pagination: html-to-image then needs no unbounded
          // cross-origin requests and each page shares the same prepared data.
          img.src = await abortable(new Promise<string>((resolve, reject) => {
            const reader = new FileReader()
            reader.onload = () => resolve(String(reader.result))
            reader.onerror = () => reject(reader.error)
            reader.readAsDataURL(blob)
          }), signal, timeoutMs)
        }
        await abortable(img.decode(), signal, timeoutMs)
        if (!img.naturalWidth) throw new Error('图片未能解码')
      } catch (error) {
        throwIfAborted(signal)
        placeholder(img, name)
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, images.length) }, worker))
  return { corsWarnings: [...new Set(warnings)], createdUrls: [] }
}

export interface SinglePageExportOptions {
  sourceElement: HTMLElement
  title: string
  theme: ImageBackgroundTheme
  metaText?: string
  signal?: AbortSignal
  onProgress?: (progress: number, message: string) => void
}

/**
 * 单页长图导出实现
 */
export async function exportSinglePageImage(
  options: SinglePageExportOptions,
): Promise<{ corsWarnings: string[]; pageCount: number }> {
  const { sourceElement, title, theme, metaText, signal, onProgress } = options
  if (signal?.aborted) throw new Error('导出已取消')

  onProgress?.(10, '等待图表与数学公式排版完成…')
  await waitForRenderingComplete(sourceElement, 15000, signal)
  await abortable(document.fonts.ready, signal)

  onProgress?.(25, '加载依赖库并配置导出画布…')
  const { toPng } = await import('html-to-image')

  // 创建屏幕外的沙箱父容器（固定定位，不干扰用户视窗）
  const sandbox = document.createElement('div')
  sandbox.className = 'export-offscreen-sandbox'
  document.body.appendChild(sandbox)

  // 内部画布保持标准流 static 布局，避免进入 SVG foreignObject 时坐标偏移导致透明空白
  const wrapper = document.createElement('div')
  wrapper.className = `export-render-canvas doc-content vditor-reset theme-${theme.id}`
  wrapper.style.width = '840px'
  wrapper.style.padding = '44px 48px'
  applyThemeVariables(wrapper, theme)

  const header = document.createElement('div')
  header.className = 'export-doc-header'
  header.style.marginBottom = '24px'
  header.style.paddingBottom = '16px'
  header.style.borderBottom = `1px solid ${theme.border}`

  const titleEl = document.createElement('h1')
  titleEl.className = 'export-doc-title'
  titleEl.style.fontSize = '26px'
  titleEl.style.fontWeight = '700'
  titleEl.style.margin = '0 0 8px 0'
  titleEl.style.lineHeight = '1.35'
  titleEl.textContent = title || '无标题'
  header.appendChild(titleEl)

  if (metaText) {
    const meta = document.createElement('div')
    meta.className = 'export-doc-meta'
    meta.style.fontSize = '12px'
    meta.style.color = theme.textSecondary
    meta.textContent = metaText
    header.appendChild(meta)
  }
  wrapper.appendChild(header)

  const content = sourceElement.cloneNode(true) as HTMLElement
  cleanDocumentArtifacts(content)
  content.style.background = 'transparent'
  wrapper.appendChild(content)
  applyThemeVariables(wrapper, theme)
  content.style.background = 'transparent'

  const footer = document.createElement('div')
  footer.className = 'export-doc-footer'
  footer.style.marginTop = '32px'
  footer.style.paddingTop = '12px'
  footer.style.borderTop = `1px solid ${theme.border}`
  footer.style.display = 'flex'
  footer.style.justifyContent = 'space-between'
  footer.style.fontSize = '12px'
  footer.style.color = theme.textSecondary
  footer.innerHTML = `<span>OpenLJMDB</span><span>文档长图</span>`
  wrapper.appendChild(footer)

  sandbox.appendChild(wrapper)

  let createdUrls: string[] = []
  let corsWarnings: string[] = []

  try {
    if (signal?.aborted) throw new Error('导出已取消')

    onProgress?.(45, '检查并优化页面图片资源…')
    const prep = await prepareImagesForExport(wrapper, signal)
    createdUrls = prep.createdUrls
    corsWarnings = prep.corsWarnings

    if (signal?.aborted) throw new Error('导出已取消')

    const measuredH = wrapper.offsetHeight
    const safety = checkSinglePageSafety(measuredH, 840, EXPORT_PIXEL_RATIO)
    if (!safety.safe) {
      throw new Error(safety.warning || '文档高度超出单页长图绘制上限')
    }

    onProgress?.(70, '正在绘制长图真实像素…')
    const fontCss = await getSharedFontEmbedCSS(wrapper, signal)

    const dataUrl = await abortable(toPng(wrapper, {
      pixelRatio: EXPORT_PIXEL_RATIO,
      cacheBust: false,
      skipFonts: false,
      skipAutoScale: true,
      fontEmbedCSS: fontCss,
      style: {
        position: 'static',
        inset: 'auto',
        left: 'auto',
        top: 'auto',
        right: 'auto',
        bottom: 'auto',
        transform: 'none',
        margin: '0',
      },
    }), signal, 60000)

    if (signal?.aborted) throw new Error('导出已取消')

    onProgress?.(95, '长图已就绪，正在启动下载…')
    const fileName = `${safeFileName(title || '无标题')}-长图.png`
    triggerDownload(dataUrl, fileName)

    onProgress?.(100, '长图导出完成')
    return { corsWarnings: Array.from(new Set(corsWarnings)), pageCount: 1 }
  } finally {
    sandbox.remove()
    for (const url of createdUrls) {
      URL.revokeObjectURL(url)
    }
  }
}

export interface MultiPageExportOptions {
  sourceElement: HTMLElement
  title: string
  theme: ImageBackgroundTheme
  formatPreset: PageFormatPreset
  metaText?: string
  signal?: AbortSignal
  onProgress?: (progress: number, message: string) => void
}

/**
 * 多页切片导出实现
 */
export async function exportMultiPageZip(options: MultiPageExportOptions): Promise<{ corsWarnings: string[]; pageCount: number }> {
  const { sourceElement, title, theme, formatPreset, metaText, signal, onProgress } = options
  throwIfAborted(signal)
  onProgress?.(5, '正在准备文字与图表…')
  await waitForRenderingComplete(sourceElement, 15000, signal)
  await abortable(document.fonts.ready, signal)
  const [{ toPng }, { zipSync }] = await Promise.all([import('html-to-image'), import('fflate')])
  throwIfAborted(signal)

  const sandbox = document.createElement('div')
  sandbox.className = 'export-offscreen-sandbox'
  document.body.appendChild(sandbox)
  let createdUrls: string[] = []
  try {
    const width = formatPreset.width
    const contentWidth = width - 88
    const capacity = formatPreset.targetHeight - 36 - 36 - 36 - 20 - 28 - 16 - 2
    const titleBlock = document.createElement('div')
    titleBlock.style.cssText = `width:${contentWidth}px;display:flow-root;margin:0 0 16px;font-family:var(--font-sans)`
    const titleEl = document.createElement('h1')
    titleEl.style.cssText = 'font-size:24px;font-weight:700;margin:0 0 8px;line-height:1.35;overflow-wrap:anywhere'
    titleEl.textContent = title || '无标题'
    titleBlock.appendChild(titleEl)
    if (metaText) {
      const meta = document.createElement('div')
      meta.style.cssText = `font-size:12px;line-height:1.75;color:${theme.textSecondary}`
      meta.textContent = metaText
      titleBlock.appendChild(meta)
    }
    sandbox.appendChild(titleBlock)
    const titleHeight = Math.ceil(titleBlock.getBoundingClientRect().height) + 16
    titleBlock.remove()
    if (titleHeight >= capacity - 80) throw new Error('标题过长，请缩短标题或选择更高的页面比例。')

    const content = sourceElement.cloneNode(true) as HTMLElement
    cleanDocumentArtifacts(content)
    content.classList.add('export-document-content')
    content.style.width = `${contentWidth}px`
    content.style.display = 'flow-root'
    content.style.overflow = 'visible'
    content.style.margin = '0'
    content.style.boxSizing = 'border-box'
    sandbox.appendChild(content)
    applyThemeVariables(content, theme)
    content.style.background = 'transparent'
    onProgress?.(15, '正在准备图片…')
    const prep = await prepareImagesForExport(content, signal)
    createdUrls = prep.createdUrls
    throwIfAborted(signal)

    // An image or a diagram is kept whole. Tall media is scaled before layout;
    // text stays at its original font size and is split between rendered lines.
    const mediaHeight = Math.max(100, capacity - titleHeight - 32)
    for (const media of content.querySelectorAll<HTMLElement>('img, .language-mermaid svg')) {
      const rect = media.getBoundingClientRect()
      if (!rect.width || !rect.height) continue
      const scale = Math.min(1, mediaHeight / rect.height, contentWidth / rect.width)
      // Freeze the measured dimensions so page-specific CSS and percentage
      // SVG widths cannot change the layout after pagination.
      media.style.setProperty('width', `${rect.width * scale}px`, 'important')
      media.style.setProperty('height', `${rect.height * scale}px`, 'important')
      media.style.setProperty('max-height', `${mediaHeight}px`, 'important')
      media.style.setProperty('max-width', '100%', 'important')
    }
    await abortable(document.fonts.ready, signal)
    const origin = content.getBoundingClientRect().top
    const height = Math.ceil(content.getBoundingClientRect().height)
    const protectedRanges: ProtectedRange[] = []
    const protect = (rect: DOMRect) => {
      if (rect.height > 0 && rect.height < capacity - titleHeight) protectedRanges.push({ start: Math.max(0, rect.top - origin - 0.5), end: Math.min(height, rect.bottom - origin + 0.5) })
    }
    const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (!node.textContent?.trim() || node.parentElement?.closest('svg, .katex, .vditor-linenumber__rows, style')) continue
      const range = document.createRange()
      range.selectNodeContents(node)
      for (const rect of range.getClientRects()) protect(rect)
    }
    for (const media of content.querySelectorAll('img, svg, .katex, tr')) protect(media.getBoundingClientRect())
    const breaks = [...content.children].map(el => Math.max(0, el.getBoundingClientRect().top - origin - 0.5))
    const slices = planImageSlices(height, capacity - titleHeight, capacity, protectedRanges, breaks)
    const originRect = content.getBoundingClientRect()
    const blocks = [...content.children].map(element => ({ element: element as HTMLElement, rect: element.getBoundingClientRect() }))
    const fontCss = await getSharedFontEmbedCSS(content, signal)
    const entries: Record<string, Uint8Array> = {}
    for (let index = 0; index < slices.length; index++) {
      throwIfAborted(signal)
      onProgress?.(25 + Math.floor(index / slices.length * 65), `正在生成第 ${index + 1} / ${slices.length} 页…`)
      const page = document.createElement('div')
      page.className = 'export-page-card'
      page.style.cssText = `width:${width}px;height:${formatPreset.targetHeight}px;padding:36px 44px;box-sizing:border-box`
      const header = document.createElement('div')
      header.className = 'export-page-header'
      header.style.cssText = `height:36px;margin-bottom:20px;border-bottom:1px solid ${theme.border};box-sizing:border-box;font-size:13px;color:${theme.textSecondary}`
      const name = document.createElement('span')
      name.style.cssText = 'max-width:75%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap'
      name.textContent = title || '无标题'
      const number = document.createElement('span')
      number.textContent = `${index + 1} / ${slices.length}`
      header.append(name, number)
      const body = document.createElement('div')
      body.className = 'export-page-body'
      if (index === 0) body.appendChild(titleBlock.cloneNode(true))
      const slice = slices[index]!
      const viewport = document.createElement('div')
      viewport.style.cssText = `height:${slice.end - slice.start}px;overflow:hidden;position:relative;width:100%`
      const copy = content.cloneNode(false) as HTMLElement
      copy.style.position = 'relative'
      copy.style.overflow = 'visible'
      copy.style.height = `${slice.end - slice.start}px`
      // Keep only blocks that intersect this page. Their measured coordinates
      // preserve layout, including a paragraph or table continued on the next
      // page, without repeatedly serializing the full long document.
      for (const { element, rect } of blocks) {
        const top = rect.top - originRect.top
        if (top >= slice.end || top + rect.height <= slice.start) continue
        const block = element.cloneNode(true) as HTMLElement
        block.style.position = 'absolute'
        block.style.top = `${top - slice.start}px`
        block.style.left = `${rect.left - originRect.left}px`
        block.style.width = `${rect.width}px`
        block.style.margin = '0'
        block.style.boxSizing = 'border-box'
        copy.appendChild(block)
      }
      viewport.appendChild(copy)
      body.appendChild(viewport)
      const footer = document.createElement('div')
      footer.className = 'export-page-footer'
      footer.style.cssText = `height:28px;margin-top:16px;border-top:1px solid ${theme.border};box-sizing:border-box;font-size:12px;color:${theme.textSecondary}`
      const brand = document.createElement('span')
      brand.textContent = 'OpenLJMDB'
      const pageNumber = document.createElement('span')
      pageNumber.textContent = `第 ${index + 1} / ${slices.length} 页`
      footer.append(brand, pageNumber)
      page.append(header, body, footer)
      sandbox.appendChild(page)
      applyThemeVariables(page, theme)
      copy.style.background = 'transparent'
      if (body.scrollHeight > body.clientHeight + 1) throw new Error('本页内容超出可用高度，请选择更高的页面比例后重试。')
      const data = await abortable(toPng(page, { pixelRatio: EXPORT_PIXEL_RATIO, skipAutoScale: true, fontEmbedCSS: fontCss }), signal, 60000)
      entries[`${safeFileName(title || '无标题')}-第${String(index + 1).padStart(3, '0')}页.png`] = dataUrlToUint8Array(data)
      page.remove()
      await new Promise<void>(resolve => setTimeout(resolve, 0))
    }
    throwIfAborted(signal)
    onProgress?.(95, '正在打包图片…')
    const bytes = zipSync(entries, { level: 0 })
    const url = URL.createObjectURL(new Blob([bytes], { type: 'application/zip' }))
    throwIfAborted(signal)
    triggerDownload(url, `${safeFileName(title || '无标题')}-分页图片.zip`)
    setTimeout(() => URL.revokeObjectURL(url), 30000)
    onProgress?.(100, '图片已生成')
    return { corsWarnings: prep.corsWarnings, pageCount: slices.length }
  } finally {
    sandbox.remove()
    createdUrls.forEach(url => URL.revokeObjectURL(url))
  }
}
