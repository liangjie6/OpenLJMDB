/**
 * IndexedDB 本地草稿。
 *
 * 主键为 instance_id + document_id + editor_session_id：不同数据集合、不同文档、不同标签页
 * 的草稿互不覆盖（03 文档 4.1、04 文档第 6 节）。草稿只是异常恢复辅助，不替代完整备份。
 */

export interface DraftRecord {
  key: string
  instance_id: string
  document_id: string
  /** 编辑会话：每次在标签页中进入编辑都会生成新值，避免覆盖尚未处理的旧草稿 */
  editor_session_id: string
  /** 所属标签页（用于判断该草稿是否正被另一个仍打开的页面编辑） */
  tab_session_id: string
  knowledge_base_id: string | null
  base_data_epoch: string
  base_revision: number
  title: string
  markdown: string
  /** 本地编辑序号，单调递增 */
  local_seq: number
  created_at: number
  updated_at: number
}

export type DraftStoreErrorKind = 'unavailable' | 'quota' | 'failed'

export class DraftStoreError extends Error {
  readonly kind: DraftStoreErrorKind
  constructor(kind: DraftStoreErrorKind, message: string) {
    super(message)
    this.name = 'DraftStoreError'
    this.kind = kind
  }
}

const DB_NAME = 'ljmdb-drafts'
const DB_VERSION = 1
const STORE = 'drafts'

export function draftKey(instanceId: string, documentId: string, sessionId: string): string {
  return `${instanceId}|${documentId}|${sessionId}`
}

function wrapError(error: unknown): DraftStoreError {
  if (error instanceof DraftStoreError) return error
  const name = error instanceof DOMException ? error.name : (error as { name?: string } | null)?.name
  if (name === 'QuotaExceededError') {
    return new DraftStoreError('quota', '浏览器存储空间不足，本地草稿无法写入')
  }
  if (name === 'InvalidStateError' || name === 'SecurityError' || name === 'UnknownError') {
    return new DraftStoreError('unavailable', '浏览器本地存储不可用（可能处于隐私模式或已被禁用）')
  }
  return new DraftStoreError('failed', `本地草稿写入失败：${error instanceof Error ? error.message : String(error)}`)
}

function promisify<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

function txDone(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
    tx.onabort = () => reject(tx.error ?? new DOMException('Transaction aborted', 'AbortError'))
  })
}

export class DraftStore {
  private dbPromise: Promise<IDBDatabase> | null = null
  private factory: IDBFactory | null

  constructor(factory?: IDBFactory | null) {
    this.factory = factory ?? (typeof indexedDB !== 'undefined' ? indexedDB : null)
  }

  private open(): Promise<IDBDatabase> {
    if (this.dbPromise) return this.dbPromise
    const factory = this.factory
    if (!factory) {
      return Promise.reject(new DraftStoreError('unavailable', '当前浏览器不支持 IndexedDB，本地草稿保护不可用'))
    }
    this.dbPromise = new Promise<IDBDatabase>((resolve, reject) => {
      let req: IDBOpenDBRequest
      try {
        req = factory.open(DB_NAME, DB_VERSION)
      } catch (error) {
        reject(wrapError(error))
        return
      }
      req.onupgradeneeded = () => {
        const db = req.result
        if (!db.objectStoreNames.contains(STORE)) {
          const store = db.createObjectStore(STORE, { keyPath: 'key' })
          store.createIndex('by_document', ['instance_id', 'document_id'], { unique: false })
          store.createIndex('by_instance', 'instance_id', { unique: false })
        }
      }
      req.onsuccess = () => {
        const db = req.result
        db.onversionchange = () => {
          db.close()
          this.dbPromise = null
        }
        db.onclose = () => {
          this.dbPromise = null
        }
        resolve(db)
      }
      req.onerror = () => reject(wrapError(req.error))
      req.onblocked = () => reject(new DraftStoreError('unavailable', '本地草稿数据库被其他页面占用，请关闭旧页面后重试'))
    }).catch((error) => {
      this.dbPromise = null
      throw wrapError(error)
    })
    return this.dbPromise
  }

  /** 探测是否可用（打开数据库并做一次读事务） */
  async probe(): Promise<void> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readonly')
      await promisify(tx.objectStore(STORE).count())
    } catch (error) {
      throw wrapError(error)
    }
  }

  async put(record: DraftRecord): Promise<void> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readwrite')
      tx.objectStore(STORE).put(record)
      await txDone(tx)
    } catch (error) {
      throw wrapError(error)
    }
  }

  async get(key: string): Promise<DraftRecord | null> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readonly')
      const result = await promisify(tx.objectStore(STORE).get(key))
      return (result as DraftRecord | undefined) ?? null
    } catch (error) {
      throw wrapError(error)
    }
  }

  async listForDocument(instanceId: string, documentId: string): Promise<DraftRecord[]> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readonly')
      const index = tx.objectStore(STORE).index('by_document')
      const result = await promisify(index.getAll(IDBKeyRange.only([instanceId, documentId])))
      return (result as DraftRecord[]).sort((a, b) => b.updated_at - a.updated_at)
    } catch (error) {
      throw wrapError(error)
    }
  }

  async listAll(): Promise<DraftRecord[]> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readonly')
      const result = await promisify(tx.objectStore(STORE).getAll())
      return (result as DraftRecord[]).sort((a, b) => b.updated_at - a.updated_at)
    } catch (error) {
      throw wrapError(error)
    }
  }

  async listForInstance(instanceId: string): Promise<DraftRecord[]> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readonly')
      const index = tx.objectStore(STORE).index('by_instance')
      const result = await promisify(index.getAll(IDBKeyRange.only(instanceId)))
      return (result as DraftRecord[]).sort((a, b) => b.updated_at - a.updated_at)
    } catch (error) {
      throw wrapError(error)
    }
  }

  async delete(key: string): Promise<void> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readwrite')
      tx.objectStore(STORE).delete(key)
      await txDone(tx)
    } catch (error) {
      throw wrapError(error)
    }
  }

  /**
   * 仅当草稿的本地编辑序号不大于已确认保存的序号时删除。
   * 用于“保存成功后只清理已确认提交的草稿版本，不删除正在编辑的更新草稿”。
   */
  async deleteIfSeqAtMost(key: string, seq: number): Promise<boolean> {
    const db = await this.open()
    try {
      const tx = db.transaction(STORE, 'readwrite')
      const store = tx.objectStore(STORE)
      const existing = (await promisify(store.get(key))) as DraftRecord | undefined
      let deleted = false
      if (existing && existing.local_seq <= seq) {
        store.delete(key)
        deleted = true
      }
      await txDone(tx)
      return deleted
    } catch (error) {
      throw wrapError(error)
    }
  }
}

export const draftStore = new DraftStore()
