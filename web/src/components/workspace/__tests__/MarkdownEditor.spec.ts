import { createRequire } from 'node:module'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import MarkdownEditor from '../MarkdownEditor.vue'

const mocks = vi.hoisted(() => ({
  nativePaste: vi.fn(),
  insertMD: vi.fn(),
  destroy: vi.fn(),
  current: null as null | {
    element: HTMLElement
    options: { input: (markdown: string) => void; after: () => void }
  },
}))

vi.mock('vditor', () => ({
  default: class {
    vditor: { ir: { element: HTMLElement } }

    constructor(host: HTMLElement, options: { input: (markdown: string) => void; after: () => void }) {
      const element = document.createElement('div')
      element.setAttribute('contenteditable', 'true')
      element.innerHTML = '<p>正文</p>'
      // Vditor installs its paste handler before the component's after callback.
      element.addEventListener('paste', mocks.nativePaste)
      host.append(element)
      this.vditor = { ir: { element } }
      mocks.current = { element, options }
      queueMicrotask(options.after)
    }

    insertMD(markdown: string) { mocks.insertMD(markdown) }
    disabled() { this.vditor.ir.element.setAttribute('contenteditable', 'false') }
    enable() { this.vditor.ir.element.setAttribute('contenteditable', 'true') }
    destroy() { mocks.destroy() }
    setTheme() {}
  },
}))

vi.mock('@/editor/vditorSetup', () => ({
  VDITOR_CDN: '/vendor/vditor',
  hljsStyle: () => 'github',
  loadScript: vi.fn().mockResolvedValue(undefined),
  editorOptions: (options: { onInput: (markdown: string) => void; onAfter: () => void }) => ({
    input: options.onInput,
    after: options.onAfter,
  }),
}))

// Use the installed parser, so accepting clipboard text is also checked against
// the real YAML and code-block Markdown serialization rather than a parser mock.
createRequire(import.meta.url)('vditor/dist/js/lute/lute.min.js')
const lute = (globalThis as unknown as {
  Lute: { New: () => { Md2VditorIRDOM: (markdown: string) => string; VditorIRDOM2Md: (html: string) => string } }
}).Lute.New()

const markdown = `---
title: 《C语言教程》114｜栈、堆与动态内存：为什么需要malloc
author: 若北辰AI
publish_time: 未知时间
source_url: [https://mp.weixin.qq.com/s/aHPobjSY0zccGMQDpghhhQ](https://mp.weixin.qq.com/s/aHPobjSY0zccGMQDpghhhQ)
exported_at: 2026-10-08T17:14:57.759999
description: 为什么C语言需要malloc？因为很多数据的大小只有运行时才能确定。本章从栈和堆讲起，理解自动内存与动态内存的区别，以及malloc、NULL检查和free背后的基本逻辑。
---

**# 《C语言教程》114｜栈、堆与动态内存：为什么需要malloc**
`

let app: App | null = null
let dirty: ReturnType<typeof vi.fn>
let input: ReturnType<typeof vi.fn>
let notifications: string[]

function select(start: Node, end = start) {
  const range = document.createRange()
  range.setStart(start, 0)
  range.setEnd(end, end.textContent?.length ?? 0)
  const selection = window.getSelection()!
  selection.removeAllRanges()
  selection.addRange(range)
}

async function mount(readonly = false) {
  const host = document.createElement('div')
  document.body.append(host)
  app = createApp(MarkdownEditor, {
    value: '', theme: 'light', readonly, maxUploadBytes: 1024,
    onDomInput: dirty, onInput: input,
  })
  app.mount(host)
  await new Promise((resolve) => setTimeout(resolve, 0))
  await nextTick()
  const element = mocks.current!.element
  select(element.querySelector('p')!.firstChild!)
  return element
}

function paste(element: HTMLElement, text = markdown, html = '', files: File[] = []) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: {
    files,
    getData: (type: string) => type === 'text/plain' ? text : type === 'text/html' ? html : '',
  } })
  element.dispatchEvent(event)
  return event
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.current = null
  notifications = []
  dirty = vi.fn(() => notifications.push('dirty'))
  input = vi.fn(() => notifications.push('input'))
  mocks.nativePaste.mockImplementation((event: ClipboardEvent) => {
    const clipboard = event.clipboardData!
    if (mocks.current!.element.getAttribute('contenteditable') !== 'true' || clipboard.files.length > 0) return
    const text = clipboard.getData('text/plain')
    mocks.current!.options.input(lute.VditorIRDOM2Md(lute.Md2VditorIRDOM(text)))
  })
})

afterEach(() => {
  app?.unmount()
  app = null
  window.getSelection()?.removeAllRanges()
  document.body.innerHTML = ''
})

describe('Markdown 编辑器粘贴', () => {
  it.each([
    ['纯文本', ''],
    ['IDE 等宽 HTML', '<div style="font-family: monospace">clipboard contents</div>'],
    ['HTML pre', '<pre>clipboard contents</pre>'],
  ])('%s 中的 YAML 文档不会新增代码围栏', async (_name, html) => {
    const element = await mount()
    const event = paste(element, markdown, html)

    expect(event.defaultPrevented).toBe(html !== '')
    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    const nativeEvent = mocks.nativePaste.mock.calls[0]![0] as ClipboardEvent
    expect(nativeEvent.clipboardData!.getData('text/plain')).toBe(markdown)
    expect(nativeEvent.clipboardData!.getData('text/html')).toBe('')
    if (html) expect(nativeEvent).not.toBe(event)
    else expect(nativeEvent).toBe(event)
    expect(mocks.insertMD).not.toHaveBeenCalled()
    expect(dirty).toHaveBeenCalledTimes(1)
    expect(input).toHaveBeenCalledTimes(1)
    expect(notifications).toEqual(['dirty', 'input'])
    const saved = input.mock.calls[0]![0] as string
    expect(saved.startsWith('---\n')).toBe(true)
    expect(saved).not.toContain('```')
    expect(saved).toContain('source_url: [https://mp.weixin.qq.com/s/aHPobjSY0zccGMQDpghhhQ](https://mp.weixin.qq.com/s/aHPobjSY0zccGMQDpghhhQ)')
    expect(saved).toContain('**# 《C语言教程》114｜栈、堆与动态内存：为什么需要malloc**')
  })

  it.each([
    ['```', '```'],
    ['~~~', '~~~'],
    ['```', '````'],
  ])('保留原有 %s / %s 代码围栏，不再套一层代码块', async (opening, closing) => {
    const element = await mount()
    const text = `${opening}c\nint main(void) { return 0; }\n${closing}`
    const event = paste(element, text, '<pre>int main(void) { return 0; }</pre>')

    expect(event.defaultPrevented).toBe(true)
    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    const nativeEvent = mocks.nativePaste.mock.calls[0]![0] as ClipboardEvent
    expect(nativeEvent).not.toBe(event)
    expect(nativeEvent.clipboardData!.getData('text/plain')).toBe(text)
    expect(nativeEvent.clipboardData!.getData('text/html')).toBe('')
    expect(mocks.insertMD).not.toHaveBeenCalled()
    expect(dirty).toHaveBeenCalledTimes(1)
    expect(notifications).toEqual(['dirty', 'input'])
    expect(input).toHaveBeenCalledExactlyOnceWith(`${text}\n`)
  })

  it.each([
    ['普通富文本', '标题', '<h1>标题</h1>'],
    ['没有纯文本的 HTML', '', '<p><strong>正文</strong></p>'],
    ['普通 C 源码', 'int main(void) {\n  return 0;\n}', '<pre>int main(void) { return 0; }</pre>'],
    ['不完整 YAML 前言', '---\ntitle: test\n正文', '<pre>clipboard contents</pre>'],
  ])('%s 由 Vditor 原有粘贴逻辑处理', async (_name, text, html) => {
    const element = await mount()
    const event = paste(element, text, html)

    expect(event.defaultPrevented).toBe(false)
    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    expect(mocks.insertMD).not.toHaveBeenCalled()
    expect(dirty).not.toHaveBeenCalled()
  })

  it('带文件的粘贴保留上传处理', async () => {
    const element = await mount()
    paste(element, markdown, '', [new File(['image'], 'image.png', { type: 'image/png' })])

    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    expect(mocks.insertMD).not.toHaveBeenCalled()
    expect(dirty).not.toHaveBeenCalled()
  })

  it.each(['start', 'end'])('选区 %s 位于代码内部时保留代码粘贴', async (endpoint) => {
    const element = await mount()
    element.innerHTML = endpoint === 'start'
      ? '<pre><code>code</code></pre><p>正文</p>'
      : '<p>正文</p><pre><code>code</code></pre>'
    const code = element.querySelector('code')!.firstChild!
    const paragraph = element.querySelector('p')!.firstChild!
    select(endpoint === 'start' ? code : paragraph, endpoint === 'start' ? paragraph : code)
    paste(element, markdown, '<pre>clipboard contents</pre>')

    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    expect(mocks.insertMD).not.toHaveBeenCalled()
  })

  it.each(['code-block', 'html-block'])('选区在 %s 源码区域时保留原有粘贴', async (type) => {
    const element = await mount()
    element.innerHTML = `<div data-type="${type}"><span>源码</span></div>`
    select(element.querySelector('span')!.firstChild!)
    paste(element)

    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    expect(mocks.insertMD).not.toHaveBeenCalled()
  })

  it('只读编辑器不插入 Markdown', async () => {
    const element = await mount(true)
    paste(element)

    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    expect(mocks.insertMD).not.toHaveBeenCalled()
    expect(dirty).not.toHaveBeenCalled()
  })

  it('卸载后移除粘贴拦截和原生输入监听', async () => {
    const element = await mount()
    app!.unmount()
    app = null
    // Reattach the old editable DOM to detect a handler that was left behind.
    document.body.append(element)
    select(element.querySelector('p')!.firstChild!)
    paste(element)
    element.dispatchEvent(new Event('input', { bubbles: true }))

    expect(mocks.destroy).toHaveBeenCalledTimes(1)
    expect(mocks.nativePaste).toHaveBeenCalledTimes(1)
    expect(mocks.insertMD).not.toHaveBeenCalled()
    expect(dirty).not.toHaveBeenCalled()
  })
})
