import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, reactive, ref, type App } from 'vue'
import ArticleEditor from '../ArticleEditor.vue'

const mocks = vi.hoisted(() => ({ mounts: 0, text: '', dirty: null as null | (() => void), input: null as null | ((text: string) => void) }))
vi.mock('../MarkdownEditor.vue', () => ({
  default: defineComponent({
    props: ['value'],
    emits: ['ready', 'input', 'dom-input', 'layout'],
    setup(props, { emit, expose }) {
      mocks.mounts++
      // Deliberately normalize the rich view. A visit must preserve original source.
      mocks.text = `${String(props.value).trim()}\n`
      mocks.dirty = () => emit('dom-input')
      mocks.input = (text) => emit('input', text)
      expose({
        isReady: () => true,
        getValue: () => mocks.text,
        setValue: (text: string) => { mocks.text = text },
        getEditElement: () => null,
        focus: () => {},
        insertValue: () => true,
      })
      nextTick(() => emit('ready'))
      return () => h('div', { class: 'mock-rich' })
    },
  }),
}))

let app: App | null = null
let host: HTMLDivElement
let editor: InstanceType<typeof ArticleEditor>
let state: { source: boolean; readonly: boolean }
let events: string[]
let input: ReturnType<typeof vi.fn>
let ready: ReturnType<typeof vi.fn>
const original = '---\ntitle: 示例\n---\n\n#  标题\n\n**Foo** foo\n\n'

async function mount(source = true, readonly = false) {
  state = reactive({ source, readonly })
  const child = ref<InstanceType<typeof ArticleEditor> | null>(null)
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ setup: () => () => h(ArticleEditor, {
    ref: child, value: original, source: state.source, readonly: state.readonly,
    theme: 'light', maxUploadBytes: 1024,
    'onUpdate:source': (value: boolean) => { state.source = value },
    onInput: input, onReady: ready, onDomInput: () => events.push('dirty'),
  }) })
  app.mount(host)
  await nextTick()
  await nextTick()
  editor = child.value!
}

function button(label: string) {
  return Array.from(host.querySelectorAll('button')).find((el) => el.textContent?.trim() === label || el.getAttribute('aria-label') === label)!
}

async function field(label: string, text: string) {
  const el = host.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!
  el.value = text
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}

beforeEach(() => {
  mocks.mounts = 0
  events = []
  input = vi.fn(() => events.push('input'))
  ready = vi.fn()
})
afterEach(() => {
  app?.unmount()
  app = null
  document.body.innerHTML = ''
})

describe('文章源码编辑', () => {
  it('直接打开源码时不挂载 Markdown 渲染器，保留原始内容', async () => {
    await mount()
    expect(mocks.mounts).toBe(0)
    expect(editor.getValue()).toBe(original)
    expect(host.querySelector('textarea')!.value).toBe(original)
    expect(ready).toHaveBeenCalledTimes(1)
    expect(input).not.toHaveBeenCalled()
  })

  it('即时通知修改和原文内容，插入文档链接使用源码选区', async () => {
    await mount()
    const el = host.querySelector('textarea')!
    el.dispatchEvent(new InputEvent('beforeinput', { inputType: 'insertText', bubbles: true }))
    el.value = '# 新正文\n**保留 Markdown**'
    el.dispatchEvent(new Event('input', { bubbles: true }))
    expect(events).toEqual(['dirty', 'input'])
    expect(editor.getValue()).toBe('# 新正文\n**保留 Markdown**')
    await nextTick()
    el.setSelectionRange(2, 5)
    expect(editor.insertValue('[文档](ljmdb://doc/id)')).toBe(true)
    expect(editor.getValue()).toBe('# [文档](ljmdb://doc/id)\n**保留 Markdown**')
  })

  it('在两种编辑模式之间往返时不规范化原文或重设保存基线', async () => {
    await mount(false)
    expect(editor.getValue()).toBe(original)
    state.source = true
    await nextTick()
    expect(host.querySelector('.mock-rich')).toBeNull()
    expect(editor.getValue()).toBe(original)
    state.source = false
    await nextTick()
    await nextTick()
    state.source = true
    await nextTick()
    expect(editor.getValue()).toBe(original)
    expect(ready).toHaveBeenCalledTimes(1)
    expect(input).not.toHaveBeenCalled()
  })

  it('切换前立即提取尚未触发防抖回调的渲染编辑内容', async () => {
    await mount(false)
    mocks.text = '# 最新输入\n'
    mocks.dirty!()
    state.source = true
    await nextTick()
    expect(editor.getValue()).toBe('# 最新输入\n')
    expect(host.querySelector('textarea')!.value).toBe('# 最新输入\n')
  })

  it('没有原生输入事件的工具栏修改也会在切换前提取并通知保存', async () => {
    await mount(false)
    mocks.text = '**工具栏加粗**\n'
    expect(editor.getValue()).toBe('**工具栏加粗**\n')
    state.source = true
    await nextTick()
    expect(editor.getValue()).toBe('**工具栏加粗**\n')
    expect(input).toHaveBeenLastCalledWith('**工具栏加粗**\n')
  })

  it('从渲染编辑打开查找替换会进入源码模式，支持循环定位、单次替换和撤销', async () => {
    await mount(false)
    await editor.openSearch()
    expect(state.source).toBe(true)
    await field('查找内容', 'foo')
    expect(host.textContent).toContain('1 / 2 处')
    button('上一处匹配').click()
    await nextTick()
    expect(host.textContent).toContain('2 / 2 处')
    button('下一处匹配').click()
    await nextTick()
    expect(host.textContent).toContain('1 / 2 处')
    await field('替换为', '$&新内容')
    button('替换').click()
    await nextTick()
    expect(editor.getValue()).toBe(original.replace('Foo', () => '$&新内容'))
    expect(host.textContent).toContain('已替换 1 处')
    button('撤销').click()
    await nextTick()
    expect(editor.getValue()).toBe(original)
    button('重做').click()
    await nextTick()
    expect(editor.getValue()).toBe(original.replace('Foo', () => '$&新内容'))
  })

  it('全部替换为包含查询词的内容只执行一轮，并且可以整体撤销和空值删除', async () => {
    await mount()
    await editor.openSearch()
    await field('查找内容', 'foo')
    await field('替换为', 'foofoo')
    button('全部替换').click()
    await nextTick()
    expect(editor.getValue()).toBe(original.replace('Foo', 'foofoo').replace('** foo', '** foofoo'))
    expect(host.textContent).toContain('已替换 2 处')
    button('撤销').click()
    await nextTick()
    expect(editor.getValue()).toBe(original)
    await field('替换为', '')
    button('全部替换').click()
    await nextTick()
    expect(editor.getValue()).toBe(original.replace('Foo', '').replace('foo', ''))
  })

  it('锁定状态可查找但不可替换或插入，远端内容更新后查找结果刷新', async () => {
    await mount(true, true)
    await editor.openSearch()
    await field('查找内容', 'foo')
    expect(button('全部替换').disabled).toBe(true)
    expect(host.querySelector('textarea')!.readOnly).toBe(true)
    expect(editor.insertValue('禁止插入')).toBe(false)
    expect(editor.getValue()).toBe(original)
    editor.setValue('远端正文')
    await nextTick()
    expect(host.querySelector('textarea')!.value).toBe('远端正文')
    expect(host.textContent).toContain('无匹配结果')
    expect(input).not.toHaveBeenCalled()
  })

  it('源码粘贴附件沿用上传队列，粘贴纯文本交给原生输入', async () => {
    await mount()
    const el = host.querySelector('textarea')!
    const text = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(text, 'clipboardData', { value: { files: [] } })
    el.dispatchEvent(text)
    expect(text.defaultPrevented).toBe(false)
    const file = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(file, 'clipboardData', { value: { files: [new File(['image'], 'image.png', { type: 'image/png' })] } })
    el.dispatchEvent(file)
    expect(file.defaultPrevented).toBe(true)
    expect(editor.getValue()).toBe(original)
  })
})
