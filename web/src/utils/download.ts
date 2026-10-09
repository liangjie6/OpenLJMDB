import { safeFileName } from './text'

/** 由浏览器下载文本内容（草稿导出等）；开始下载不等于用户已保管成功 */
export function downloadText(fileName: string, content: string, mime = 'text/markdown;charset=utf-8'): void {
  const blob = new Blob([content], { type: mime })
  const url = URL.createObjectURL(blob)
  triggerDownload(url, fileName)
  setTimeout(() => URL.revokeObjectURL(url), 30_000)
}

export function triggerDownload(url: string, fileName?: string): void {
  const a = document.createElement('a')
  a.href = url
  if (fileName) a.download = fileName
  a.rel = 'noopener'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

/** 草稿导出：标题作为独立 H1 放在正文之前，与“纯 Markdown 导出”的约定一致 */
export function exportDraftMarkdown(title: string, markdown: string, suffix = '本地草稿'): void {
  const name = `${safeFileName(title || '无标题')}-${suffix}.md`
  downloadText(name, `# ${title || '无标题'}\n\n${markdown}`)
}
