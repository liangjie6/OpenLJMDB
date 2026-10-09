/**
 * Vditor 公共配置：编辑（ir 模式）与阅读共用同一套 Markdown 语法契约与本地资源。
 *
 * - 所有运行期资源来自 /vendor/vditor（scripts/copy-vendor.mjs 复制的锁定版本），不访问 CDN。
 * - Mermaid 强制 securityLevel=strict；KaTeX / Mermaid 的错误信息转义后再显示（Vditor 会以 innerHTML 插入）。
 * - 阅读渲染输出再经 DOMPurify 白名单净化，外部链接 / 图片做标记。
 * - 首版语法契约之外的图表（echarts、graphviz、plantuml 等）降级为源码显示。
 */
import type Vditor from 'vditor'
import DOMPurify from 'dompurify'
import { escapeHtml } from '@/utils/text'
import { isExternalUrl, parseAttachmentLink, parseDocumentLink } from '@/utils/links'
import { renderCodeHighlight, renderCodeMenu } from './codeBlocks'

export type { Vditor }

export const VDITOR_VERSION = '3.11.3'
export const VDITOR_CDN = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/vendor/vditor`

export type ContentTheme = 'light' | 'dark'

/** 编辑预览、阅读共用的 Markdown 选项（GFM、脚注、公式、Mermaid、代码高亮） */
export const MARKDOWN_OPTIONS: IMarkdownConfig = {
  autoSpace: false,
  callout: true,
  paragraphBeginningSpace: false,
  fixTermTypo: false,
  toc: false,
  footnotes: true,
  imageCaption: false,
  codeBlockPreview: true,
  mathBlockPreview: true,
  sanitize: true,
  linkBase: '',
  linkPrefix: '',
  listStyle: false,
  mark: false,
  gfmAutoLink: true,
  sup: false,
  sub: false,
}

export const MATH_OPTIONS: IMath = {
  engine: 'KaTeX',
  inlineDigit: false,
  macros: {},
}

/** 首版语法契约之外、且未随程序打包的图表语言 */
export const UNSUPPORTED_DIAGRAMS = ['echarts', 'mindmap', 'graphviz', 'markmap', 'abc', 'plantuml', 'smiles', 'wavedrom']

export function hljsStyle(theme: ContentTheme): string {
  return theme === 'dark' ? 'github-dark' : 'github'
}

// ------------------------------------------------------------------ 第三方库安全拦截

type AnyRecord = Record<string, unknown>

function errorMessage(error: unknown): string {
  if (error && typeof error === 'object' && 'message' in error) return String((error as { message: unknown }).message)
  return String(error)
}

function patchMermaid(value: unknown): unknown {
  const mermaid = value as AnyRecord | null
  if (!mermaid || typeof mermaid !== 'object' || mermaid.__ljmdbPatched) return value
  const initialize = mermaid.initialize as ((config: AnyRecord) => void) | undefined
  const render = mermaid.render as ((...args: unknown[]) => Promise<unknown>) | undefined
  if (typeof initialize !== 'function' || typeof render !== 'function') return value
  return {
    ...mermaid,
    __ljmdbPatched: true,
    initialize(config: AnyRecord = {}) {
      const fontFamily = getComputedStyle(document.documentElement).getPropertyValue('--font-sans').trim()
      initialize({
        ...config,
        // 严格模式：标签文本编码、禁用点击回调等交互
        securityLevel: 'strict',
        startOnLoad: false,
        fontFamily,
        altFontFamily: fontFamily,
      })
    },
    async render(...args: unknown[]) {
      try {
        return await render(...args)
      } catch (error) {
        throw new Error(`图表语法错误，已保留源码：${escapeHtml(errorMessage(error))}`)
      }
    },
  }
}

function patchKatex(value: unknown): unknown {
  const katex = value as AnyRecord | null
  if (!katex || typeof katex !== 'object' || katex.__ljmdbPatched) return value
  const renderToString = katex.renderToString as ((expr: string, options: AnyRecord) => string) | undefined
  if (typeof renderToString !== 'function') return value
  return {
    ...katex,
    __ljmdbPatched: true,
    renderToString(expr: string, options: AnyRecord = {}) {
      try {
        return renderToString(expr, { ...options, trust: false, strict: 'ignore', throwOnError: true, maxExpand: 1000 })
      } catch (error) {
        throw new Error(`公式语法错误：${escapeHtml(errorMessage(error))}`)
      }
    },
  }
}

function guardGlobal(name: string, patch: (value: unknown) => unknown) {
  const target = window as unknown as AnyRecord
  let current = target[name] ? patch(target[name]) : undefined
  Object.defineProperty(window, name, {
    configurable: true,
    enumerable: true,
    get() {
      return current
    },
    set(next: unknown) {
      current = next ? patch(next) : next
    },
  })
}

let guardsInstalled = false

/**
 * 在任何 Vditor 代码运行前调用。mermaid.min.js / katex.min.js 以全局变量导出，
 * 这里通过访问器在赋值时替换为加固后的对象。
 */
export function installVendorGuards(): void {
  if (guardsInstalled || typeof window === 'undefined') return
  guardsInstalled = true
  guardGlobal('mermaid', patchMermaid)
  guardGlobal('katex', patchKatex)
}

// ------------------------------------------------------------------ HTML 净化

let purifier: typeof DOMPurify | null = null

function getPurifier() {
  if (purifier) return purifier
  purifier = DOMPurify(window)
  purifier.addHook('afterSanitizeAttributes', (node: Node) => {
    const el = node as Element
    if (el.tagName === 'A') {
      const href = el.getAttribute('href') ?? ''
      if (parseDocumentLink(href)) {
        el.classList.add('ljmdb-doc-link')
      } else if (parseAttachmentLink(href)) {
        el.classList.add('ljmdb-attachment-link')
      } else if (isExternalUrl(href)) {
        el.setAttribute('target', '_blank')
        el.setAttribute('rel', 'noopener noreferrer nofollow')
        el.classList.add('ljmdb-external-link')
      }
    } else if (el.tagName === 'IMG') {
      const src = el.getAttribute('src') ?? ''
      if (isExternalUrl(src)) {
        el.classList.add('ljmdb-external-img')
        el.setAttribute('referrerpolicy', 'no-referrer')
        el.setAttribute('title', `外部图片（需要联网访问）：${src}`)
      }
      el.setAttribute('loading', 'lazy')
    } else if (el.tagName === 'INPUT') {
      // 阅读模式任务列表只读
      el.setAttribute('disabled', '')
    }
  })
  return purifier
}

export function sanitizeHtml(html: string): string {
  return getPurifier().sanitize(html, {
    ADD_ATTR: ['target', 'data-math', 'data-type', 'data-lang', 'data-subtype', 'loading', 'referrerpolicy'],
    FORBID_TAGS: ['style', 'form', 'iframe', 'object', 'embed', 'frame', 'frameset', 'base', 'meta', 'link'],
    FORBID_ATTR: ['style', 'srcset'],
    ALLOW_DATA_ATTR: true,
  }) as string
}

/** 把未打包的图表语言改为普通源码块，避免请求不存在的渲染脚本 */
export function markUnsupportedDiagrams(html: string): string {
  const pattern = new RegExp(`class="language-(${UNSUPPORTED_DIAGRAMS.join('|')})"`, 'g')
  return html.replace(pattern, 'class="ljmdb-unsupported-diagram" data-lang="$1"')
}

export function previewOptions(theme: ContentTheme, extra: Partial<IPreviewOptions> = {}): IPreviewOptions {
  return {
    mode: theme,
    cdn: VDITOR_CDN,
    lang: 'zh_CN',
    icon: 'ant',
    anchor: 0,
    emojiPath: `${VDITOR_CDN}/dist/images/emoji`,
    // 代码高亮由 highlightCodeBlocks 同步完成，保证搜索高亮在其之后执行
    hljs: { enable: false, lineNumber: true, style: hljsStyle(theme), defaultLang: '', renderMenu: renderCodeMenu },
    math: MATH_OPTIONS,
    markdown: MARKDOWN_OPTIONS,
    theme: { current: theme, path: `${VDITOR_CDN}/dist/css/content-theme` },
    render: { media: { enable: false } },
    speech: { enable: false },
    transform: (html: string) => sanitizeHtml(markUnsupportedDiagrams(html)),
    ...extra,
  }
}

// ------------------------------------------------------------------ 脚本加载与代码高亮

const scriptLoads = new Map<string, Promise<void>>()

/** 与 Vditor 的 addScript 语义一致：已存在同 ID 的脚本则视为已加载 */
export function loadScript(src: string, id: string): Promise<void> {
  if (document.getElementById(id)) return Promise.resolve()
  const existing = scriptLoads.get(id)
  if (existing) return existing
  const promise = new Promise<void>((resolve, reject) => {
    const el = document.createElement('script')
    el.src = src
    el.async = true
    el.onload = () => {
      if (document.getElementById(id)) el.remove()
      else el.id = id
      resolve()
    }
    el.onerror = () => {
      scriptLoads.delete(id)
      el.remove()
      reject(new Error(`本地资源加载失败：${src}`))
    }
    document.head.appendChild(el)
  })
  scriptLoads.set(id, promise)
  return promise
}

const SKIP_HIGHLIGHT = /language-(mermaid|flowchart|math|echarts|mindmap|plantuml|smiles|abc|graphviz|markmap|wavedrom)\b/

export async function highlightCodeBlocks(root: HTMLElement): Promise<void> {
  const blocks = Array.from(root.querySelectorAll<HTMLElement>('pre > code'))
  if (blocks.length === 0) return
  // 资源加载失败时仍显示源码、行号和复制入口。
  await loadScript(`${VDITOR_CDN}/dist/js/highlight.js/highlight.min.js?v=11.7.0`, 'vditorHljsScript').catch(() => undefined)
  if (window.hljs) {
    await loadScript(`${VDITOR_CDN}/dist/js/highlight.js/third-languages.js?v=1.0.1`, 'vditorHljsThirdScript').catch(() => undefined)
  }
  for (const block of blocks) {
    if (SKIP_HIGHLIGHT.test(block.className) || block.classList.contains('hljs')) continue
    if (block.classList.contains('ljmdb-unsupported-diagram')) continue
    renderCodeHighlight(block)
  }
}

// ------------------------------------------------------------------ 编辑器

const DOC_LINK_ICON =
  '<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M6 2h9l5 5v6.5h-2V8h-4V4H6v16h6v2H6a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2Zm11.6 13.1 1.4-1.4a3 3 0 0 1 4.2 4.2l-2.1 2.1a3 3 0 0 1-4.2 0l-.4-.4 1.4-1.4.4.4a1 1 0 0 0 1.4 0l2.1-2.1a1 1 0 1 0-1.4-1.4l-1.4 1.4-1.4-1.4Zm-1.5 6.4-1.4 1.4a3 3 0 0 1-4.2-4.2l2.1-2.1a3 3 0 0 1 4.2 0l.4.4-1.4 1.4-.4-.4a1 1 0 0 0-1.4 0l-2.1 2.1a1 1 0 1 0 1.4 1.4l1.4-1.4 1.4 1.4Z"/></svg>'

export interface EditorOptionsInput {
  value: string
  theme: ContentTheme
  placeholder?: string
  maxUploadBytes: number
  uploadHandler: (files: File[]) => Promise<null> | null
  onInput: (markdown: string) => void
  onAfter: () => void
  onDocLink: () => void
}

export function editorOptions(input: EditorOptionsInput): IOptions {
  return {
    mode: 'ir',
    cdn: VDITOR_CDN,
    lang: 'zh_CN',
    icon: 'ant',
    theme: input.theme === 'dark' ? 'dark' : 'classic',
    value: input.value,
    placeholder: input.placeholder ?? '开始输入正文…',
    height: 'auto',
    minHeight: 420,
    width: '100%',
    undoDelay: 800,
    typewriterMode: false,
    // 不使用 Vditor 自带的 localStorage 缓存；草稿由 IndexedDB 按实例与会话隔离保存
    cache: { enable: false },
    counter: { enable: false },
    outline: { enable: false, position: 'right' },
    resize: { enable: false },
    toolbarConfig: { pin: true, hide: false },
    toolbar: [
      'headings',
      'bold',
      'italic',
      'strike',
      '|',
      'list',
      'ordered-list',
      'check',
      'outdent',
      'indent',
      '|',
      'quote',
      'line',
      'code',
      'inline-code',
      'table',
      '|',
      'link',
      {
        name: 'ljmdb-doc-link',
        tip: '插入文档链接',
        tipPosition: 'n',
        icon: DOC_LINK_ICON,
        click: () => input.onDocLink(),
      },
      'upload',
      '|',
      'undo',
      'redo',
    ],
    preview: {
      mode: 'editor',
      hljs: { enable: true, lineNumber: true, style: hljsStyle(input.theme), defaultLang: '', renderMenu: renderCodeMenu },
      math: MATH_OPTIONS,
      markdown: MARKDOWN_OPTIONS,
      theme: { current: input.theme, path: `${VDITOR_CDN}/dist/css/content-theme` },
      render: { media: { enable: false } },
      actions: [],
    },
    hint: {
      emojiPath: `${VDITOR_CDN}/dist/images/emoji`,
      emojiTail: '',
      delay: 200,
      parse: true,
    },
    link: { isOpen: false },
    image: { isPreview: true },
    upload: {
      handler: input.uploadHandler,
      multiple: true,
      max: input.maxUploadBytes,
    },
    input: input.onInput,
    after: input.onAfter,
    tab: '\t',
  }
}
