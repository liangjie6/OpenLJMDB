import { editorSessionId } from './session'

/**
 * 多标签页通信（BroadcastChannel）。只用于提示，例如“此文档也在另一页面编辑”；
 * 最终一致性仍由后端 revision 校验保证（04 文档第 6 节）。
 */
export type BroadcastMessage =
  | { type: 'presence'; instance_id: string; doc_id: string; session_id: string; mode: 'edit' | 'read'; dirty: boolean }
  | { type: 'left'; instance_id: string; doc_id: string; session_id: string }
  | { type: 'saved'; instance_id: string; doc_id: string; session_id: string; revision: number; title: string }
  | { type: 'tree-changed'; instance_id: string; kb_id: string; session_id: string }
  | { type: 'kb-changed'; instance_id: string; session_id: string }
  | { type: 'drafts-changed'; instance_id: string; session_id: string }
  | { type: 'reload-required'; instance_id: string; session_id: string; reason: string }
  | { type: 'ping'; instance_id: string; session_id: string }

type Listener = (message: BroadcastMessage) => void

const CHANNEL_NAME = 'ljmdb'
const PRESENCE_TTL = 12_000

class TabBus {
  private channel: BroadcastChannel | null = null
  private listeners = new Set<Listener>()
  /** doc_id → session_id → { at, mode, dirty } */
  private presence = new Map<string, Map<string, { at: number; mode: 'edit' | 'read'; dirty: boolean }>>()

  constructor() {
    if (typeof BroadcastChannel === 'undefined') return
    try {
      this.channel = new BroadcastChannel(CHANNEL_NAME)
      this.channel.onmessage = (event: MessageEvent<BroadcastMessage>) => this.receive(event.data)
    } catch {
      this.channel = null
    }
  }

  get supported(): boolean {
    return this.channel !== null
  }

  private receive(message: BroadcastMessage): void {
    if (!message || typeof message !== 'object' || message.session_id === editorSessionId) return
    if (message.type === 'presence') {
      let sessions = this.presence.get(message.doc_id)
      if (!sessions) {
        sessions = new Map()
        this.presence.set(message.doc_id, sessions)
      }
      sessions.set(message.session_id, { at: Date.now(), mode: message.mode, dirty: message.dirty })
    } else if (message.type === 'left') {
      this.presence.get(message.doc_id)?.delete(message.session_id)
    }
    for (const listener of this.listeners) listener(message)
  }

  post(message: BroadcastMessage): void {
    try {
      this.channel?.postMessage(message)
    } catch {
      // 忽略：通信只用于提示
    }
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  /** 其他标签页中正在查看/编辑该文档的会话 */
  othersOn(docId: string): Array<{ session_id: string; mode: 'edit' | 'read'; dirty: boolean }> {
    const sessions = this.presence.get(docId)
    if (!sessions) return []
    const now = Date.now()
    const result: Array<{ session_id: string; mode: 'edit' | 'read'; dirty: boolean }> = []
    for (const [sessionId, info] of sessions) {
      if (now - info.at > PRESENCE_TTL) sessions.delete(sessionId)
      else result.push({ session_id: sessionId, mode: info.mode, dirty: info.dirty })
    }
    return result
  }

  /** 请其他标签页立即报告在线状态，等待片刻后再判断草稿是否属于仍打开的页面 */
  async ping(instanceId: string, waitMs = 300): Promise<void> {
    if (!this.channel) return
    this.post({ type: 'ping', instance_id: instanceId, session_id: editorSessionId })
    await new Promise((resolve) => setTimeout(resolve, waitMs))
  }

  isSessionAlive(sessionId: string): boolean {
    const now = Date.now()
    for (const sessions of this.presence.values()) {
      const info = sessions.get(sessionId)
      if (info && now - info.at <= PRESENCE_TTL) return true
    }
    return false
  }
}

export const tabBus = new TabBus()
