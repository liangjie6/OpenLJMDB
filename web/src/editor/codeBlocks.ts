import { escapeHtml } from '@/utils/text'

type Highlighter = Window['hljs'] & {
  highlightAuto(source: string, languages?: string[]): { value: string; language?: string }
}

const AUTO_LANGUAGES = ['bash', 'javascript', 'typescript', 'python', 'json', 'yaml', 'xml', 'css', 'sql', 'go', 'java', 'cpp']
const LANGUAGE_LABELS: Record<string, string> = {
  bash: 'Shell', sh: 'Shell', shell: 'Shell', shellscript: 'Shell',
  javascript: 'JavaScript', js: 'JavaScript', typescript: 'TypeScript', ts: 'TypeScript',
  python: 'Python', py: 'Python', json: 'JSON', yaml: 'YAML', yml: 'YAML',
  xml: 'HTML / XML', html: 'HTML', css: 'CSS', sql: 'SQL', go: 'Go',
  java: 'Java', cpp: 'C++', c: 'C', plaintext: 'Text', text: 'Text', txt: 'Text',
}

function codeLanguage(block: HTMLElement): string {
  return /language-([\w#+.-]+)/i.exec(block.className)?.[1]?.toLowerCase() ?? ''
}

function lineCount(source: string): number {
  // Markdown 渲染器附加的末尾换行不额外编号；保留源码中的空行。
  return Math.max(1, source.replace(/\r\n|\r/g, '\n').replace(/\n$/, '').split('\n').length)
}

function languageLabel(language: string): string {
  return LANGUAGE_LABELS[language] ?? (language || 'Text')
}

/** 阅读和编辑预览共用的工具栏；不向可编辑的 Markdown 源码插入 UI。 */
export function renderCodeMenu(block: HTMLElement, menu: HTMLElement): void {
  if (block.classList.contains('ljmdb-unsupported-diagram')) return
  const source = block.textContent ?? ''
  let language = codeLanguage(block)
  // shell 在 highlight.js 中表示终端会话；命令脚本使用 Bash 语法。
  if (language === 'shell' || language === 'shellscript') {
    block.classList.replace(`language-${language}`, 'language-bash')
    language = 'bash'
  }
  if (!language && window.hljs) {
    language = (window.hljs as Highlighter).highlightAuto(source, AUTO_LANGUAGES).language ?? ''
    if (language) block.classList.add(`language-${language}`)
  }
  block.parentElement?.classList.add('ljmdb-code-block')
  block.style.setProperty('--code-gutter', `${Math.max(2, String(lineCount(source)).length)}ch`)
  menu.classList.add('ljmdb-code-toolbar')
  menu.setAttribute('contenteditable', 'false')
  const label = document.createElement('span')
  label.className = 'ljmdb-code-language'
  label.textContent = languageLabel(language)
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'ljmdb-code-copy'
  button.textContent = '复制代码'
  button.setAttribute('aria-live', 'polite')
  const textarea = menu.querySelector('textarea')
  // 替换 Vditor 的内联 onclick，以支持键盘操作、Clipboard API 与失败提示。
  menu.replaceChildren(label, button)
  if (textarea) menu.appendChild(textarea)
  button.addEventListener('click', async (event) => {
    event.stopPropagation()
    try {
      const text = textarea?.value ?? source.replace(/\n$/, '')
      try {
        if (!navigator.clipboard?.writeText) throw new Error('Clipboard API unavailable')
        await navigator.clipboard.writeText(text)
      } catch {
        if (!textarea) throw new Error('Copy unavailable')
        const active = document.activeElement as HTMLElement | null
        const selection = window.getSelection()
        const range = selection?.rangeCount ? selection.getRangeAt(0).cloneRange() : null
        textarea.select()
        const copied = document.execCommand('copy')
        active?.focus({ preventScroll: true })
        if (range && selection) {
          selection.removeAllRanges()
          selection.addRange(range)
        }
        if (!copied) throw new Error('Copy failed')
      }
      button.textContent = '已复制'
    } catch {
      button.textContent = '复制失败，请手动选择'
    }
    window.setTimeout(() => { button.textContent = '复制代码' }, 2000)
  })
}

/** 同步完成高亮和行号，再由阅读视图应用搜索标记。 */
export function renderCodeHighlight(block: HTMLElement): void {
  const source = block.textContent ?? ''
  const hljs = window.hljs as Highlighter | undefined
  const declared = codeLanguage(block)
  const result = !hljs ? { value: escapeHtml(source), language: declared }
    : declared ? {
      value: hljs.highlight(source, { language: hljs.getLanguage(declared) ? declared : 'plaintext', ignoreIllegals: true }).value,
      language: declared,
    } : hljs.highlightAuto(source, AUTO_LANGUAGES)
  block.innerHTML = result.value
  block.classList.add('hljs', 'vditor-linenumber')
  block.style.setProperty('--code-gutter', `${Math.max(2, String(lineCount(source)).length)}ch`)
  const rows = document.createElement('span')
  rows.className = 'vditor-linenumber__rows'
  rows.setAttribute('aria-hidden', 'true')
  const count = lineCount(source)
  for (let i = 0; i < count; i++) rows.appendChild(document.createElement('span'))
  block.appendChild(rows)
  const label = block.parentElement?.querySelector('.ljmdb-code-language')
  if (label) label.textContent = languageLabel(result.language ?? declared)
}
