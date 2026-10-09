import { isUuid } from './uuid'

/** 内部文档链接统一为根相对 /documents/{uuid}，重命名与移动不改变地址 */
export function documentPath(id: string): string {
  return `/documents/${id}`
}

export function parseDocumentLink(href: string | null | undefined): string | null {
  if (!href) return null
  let path = href
  try {
    const url = new URL(href, window.location.origin)
    if (url.origin !== window.location.origin) return null
    path = url.pathname
  } catch {
    return null
  }
  const match = /^\/documents\/([0-9a-fA-F-]{36})\/?$/.exec(path)
  return match && isUuid(match[1]!) ? match[1]!.toLowerCase() : null
}

export function parseAttachmentLink(href: string | null | undefined): string | null {
  if (!href) return null
  try {
    const url = new URL(href, window.location.origin)
    if (url.origin !== window.location.origin) return null
    const match = /^\/api\/v1\/attachments\/([0-9a-fA-F-]{36})\/content$/.exec(url.pathname)
    return match ? match[1]!.toLowerCase() : null
  } catch {
    return null
  }
}

export function isExternalUrl(href: string | null | undefined): boolean {
  if (!href) return false
  try {
    const url = new URL(href, window.location.origin)
    return (url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== window.location.origin
  } catch {
    return false
  }
}

/** Markdown 链接文本转义，避免标题中的方括号破坏语法 */
export function escapeLinkText(text: string): string {
  return text.replace(/\\/g, '\\\\').replace(/([[\]])/g, '\\$1')
}

export function markdownDocLink(title: string, id: string): string {
  return `[${escapeLinkText(title || '无标题')}](${documentPath(id)})`
}

export function markdownAttachment(name: string, url: string, isImage: boolean): string {
  const text = escapeLinkText(name)
  return isImage ? `![${text}](${url})` : `[${text}](${url})`
}
