import { uuid } from '@/utils/uuid'

/**
 * 当前标签页 ID。每次页面加载生成新值：标签页关闭或刷新后，
 * 旧页面的草稿保留在 IndexedDB 中，等待用户重新打开文档时确认恢复。
 * 多标签页在线状态（BroadcastChannel）也使用该 ID。
 */
export const editorSessionId: string = uuid()

/** 每次进入编辑都使用新的编辑会话 ID，草稿互不覆盖 */
export function newEditSessionId(): string {
  return uuid()
}

/** 当前页面加载时间 */
export const pageLoadedAt: number = Date.now()
