import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api'
import { isApiError } from '@/api/http'
import type { UploadedAttachment } from '@/api/types'
import { formatBytes } from '@/utils/format'
import { markdownAttachment } from '@/utils/links'
import { uuid } from '@/utils/uuid'
import { useHealthStore } from './health'

export type UploadState = 'queued' | 'uploading' | 'done' | 'failed' | 'cancelled'

export interface UploadTask {
  id: string
  docId: string
  /** 发起上传的编辑器实例；结果只插入该实例对应的文档 */
  ownerToken: string
  file: File
  name: string
  size: number
  loaded: number
  state: UploadState
  error: string | null
  result: UploadedAttachment | null
  markdown: string | null
  inserted: boolean
  createdAt: number
}

/** 浏览器内联展示的安全图片类型；SVG 等主动内容按附件链接处理 */
const INLINE_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/bmp', 'image/avif'])

export function isInlineImageType(mediaType: string | null | undefined): boolean {
  return !!mediaType && INLINE_IMAGE_TYPES.has(mediaType.toLowerCase().split(';')[0]!.trim())
}

type Inserter = (task: UploadTask) => boolean

const MAX_CONCURRENT = 2

export const useUploadStore = defineStore('uploads', () => {
  const tasks = ref<UploadTask[]>([])
  const aborters = new Map<string, AbortController>()
  const inserters = new Map<string, Inserter>()

  function registerOwner(token: string, insert: Inserter) {
    inserters.set(token, insert)
  }

  function unregisterOwner(token: string) {
    inserters.delete(token)
  }

  function find(id: string) {
    return tasks.value.find((t) => t.id === id)
  }

  function activeCount(docId?: string): number {
    return tasks.value.filter((t) => (t.state === 'queued' || t.state === 'uploading') && (!docId || t.docId === docId)).length
  }

  /** 上传前校验：返回不能上传的原因 */
  function validate(file: File): string | null {
    const limit = useHealthStore().limits.upload_max_bytes
    if (!file.name || !file.name.trim()) return '文件名为空'
    if (file.size === 0) return `${file.name}：文件为空`
    if (file.size > limit) return `${file.name}：${formatBytes(file.size)} 超过单个附件上限 ${formatBytes(limit)}`
    return null
  }

  function enqueue(files: File[], context: { docId: string; ownerToken: string }): string[] {
    const problems: string[] = []
    for (const file of files) {
      const problem = validate(file)
      if (problem) {
        problems.push(problem)
        continue
      }
      tasks.value.push({
        id: uuid(),
        docId: context.docId,
        ownerToken: context.ownerToken,
        file,
        name: file.name,
        size: file.size,
        loaded: 0,
        state: 'queued',
        error: null,
        result: null,
        markdown: null,
        inserted: false,
        createdAt: Date.now(),
      })
    }
    pump()
    return problems
  }

  function pump() {
    const running = tasks.value.filter((t) => t.state === 'uploading').length
    let slots = MAX_CONCURRENT - running
    for (const task of tasks.value) {
      if (slots <= 0) break
      if (task.state !== 'queued') continue
      slots--
      void run(task)
    }
  }

  async function run(task: UploadTask) {
    const t = find(task.id)
    if (!t) return
    const controller = new AbortController()
    aborters.set(t.id, controller)
    t.state = 'uploading'
    t.loaded = 0
    t.error = null
    try {
      const result = await api.attachments.upload(t.file, {
        signal: controller.signal,
        documentId: t.docId,
        onProgress: (loaded) => {
          const current = find(t.id)
          if (current) current.loaded = loaded
        },
      })
      const current = find(t.id)
      if (!current) return
      current.result = result
      current.loaded = current.size
      current.state = 'done'
      current.markdown = markdownAttachment(result.original_name || current.name, result.url, isInlineImageType(result.media_type))
      const insert = inserters.get(current.ownerToken)
      // 上传成功才插入稳定附件地址；发起上传的编辑器已关闭时不插入其他文档
      current.inserted = insert ? insert(current) : false
    } catch (error) {
      const current = find(t.id)
      if (!current) return
      if (isApiError(error) && error.kind === 'aborted') {
        current.state = 'cancelled'
        current.error = '已取消'
      } else {
        current.state = 'failed'
        current.error = isApiError(error) ? error.message : String(error)
      }
    } finally {
      aborters.delete(t.id)
      pump()
    }
  }

  function cancel(id: string) {
    const task = find(id)
    if (!task) return
    if (task.state === 'queued') {
      task.state = 'cancelled'
      task.error = '已取消'
    }
    aborters.get(id)?.abort()
  }

  function cancelForOwner(ownerToken: string) {
    for (const task of tasks.value) {
      if (task.ownerToken === ownerToken && (task.state === 'queued' || task.state === 'uploading')) cancel(task.id)
    }
  }

  function retry(id: string) {
    const task = find(id)
    if (!task || (task.state !== 'failed' && task.state !== 'cancelled')) return
    task.state = 'queued'
    task.error = null
    pump()
  }

  /** 手动插入（例如自动插入时编辑器暂不可用） */
  function insertManually(id: string): boolean {
    const task = find(id)
    if (!task || task.state !== 'done') return false
    const insert = inserters.get(task.ownerToken)
    if (insert && insert(task)) {
      task.inserted = true
      return true
    }
    return false
  }

  function dismiss(id: string) {
    const task = find(id)
    if (task && (task.state === 'queued' || task.state === 'uploading')) return
    tasks.value = tasks.value.filter((t) => t.id !== id)
  }

  function clearFinished() {
    tasks.value = tasks.value.filter((t) => t.state === 'queued' || t.state === 'uploading' || (t.state === 'done' && !t.inserted))
  }

  return {
    tasks,
    registerOwner,
    unregisterOwner,
    activeCount,
    enqueue,
    cancel,
    cancelForOwner,
    retry,
    insertManually,
    dismiss,
    clearFinished,
  }
})
