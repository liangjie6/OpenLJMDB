import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/http'
import type { DocumentDetail, SaveDocumentBody, SaveResult } from '@/api/types'
import { SaveController, type DraftPayload, type ServiceState } from '../saveController'

interface Harness {
  ctrl: SaveController
  saves: SaveDocumentBody[]
  drafts: DraftPayload[]
  deletedDrafts: number[]
  editor: { value: string }
  server: DocumentDetail
  service: { state: ServiceState }
  saveImpl: (body: SaveDocumentBody) => Promise<SaveResult>
  setSaveImpl(fn: (body: SaveDocumentBody) => Promise<SaveResult>): void
}

function doc(partial: Partial<DocumentDetail> = {}): DocumentDetail {
  return {
    id: 'd1',
    knowledge_base_id: 'k1',
    parent_id: null,
    title: '标题',
    markdown: 'hello',
    html: '',
    render_version: '',
    revision: 1,
    created_at: 1,
    updated_at: 1,
    ...partial,
  }
}

function setup(): Harness {
  const h = {} as Harness
  h.saves = []
  h.drafts = []
  h.deletedDrafts = []
  h.editor = { value: 'hello' }
  h.server = doc()
  h.service = { state: 'ok' }
  // 默认保存实现：模拟服务端乐观锁
  h.saveImpl = async (body) => {
    if (body.expected_revision !== h.server.revision) {
      throw new ApiError({ status: 409, code: 'REVISION_CONFLICT', message: '冲突', kind: 'http' })
    }
    h.server = { ...h.server, title: body.title, markdown: body.markdown, revision: h.server.revision + 1, updated_at: Date.now() }
    return { id: 'd1', revision: h.server.revision, updated_at: h.server.updated_at, title: body.title }
  }
  h.setSaveImpl = (fn) => {
    h.saveImpl = fn
  }
  h.ctrl = new SaveController(
    {
      save: (body) => {
        h.saves.push(body)
        return h.saveImpl(body)
      },
      fetchDocument: async () => ({ ...h.server }),
      readEditor: () => h.editor.value,
      writeDraft: async (d) => {
        h.drafts.push(d)
      },
      deleteDraft: async (seq) => {
        h.deletedDrafts.push(seq)
      },
      serviceState: () => h.service.state,
      validate: (title) => (title.trim() ? null : '标题不能为空'),
    },
    { title: '标题', markdown: 'hello', revision: 1 },
  )
  return h
}

/** 模拟用户在编辑器中输入：DOM 输入事件 + 编辑器内容变化 */
function type(h: Harness, value: string) {
  h.editor.value = value
  h.ctrl.notifyEditorInput()
}

describe('SaveController', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-04T12:00:00Z'))
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('输入停止 3 秒后保存', async () => {
    const h = setup()
    type(h, 'hello world')
    expect(h.ctrl.view().state).toBe('dirty')
    await vi.advanceTimersByTimeAsync(2900)
    expect(h.saves).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(200)
    expect(h.saves).toHaveLength(1)
    expect(h.saves[0]).toMatchObject({ markdown: 'hello world', expected_revision: 1 })
    expect(h.ctrl.view().state).toBe('clean')
    expect(h.ctrl.view().revision).toBe(2)
  })

  it('持续输入时最长 30 秒触发一次保存', async () => {
    const h = setup()
    for (let i = 0; i < 40; i++) {
      type(h, `text ${i}`)
      await vi.advanceTimersByTimeAsync(1000)
    }
    // 40 秒持续输入（每秒一次，从不停顿 3 秒）：第 30 秒触发一次
    expect(h.saves.length).toBeGreaterThanOrEqual(1)
    expect(h.saves[0]!.markdown).toBe('text 29')
  })

  it('连续输入 90 秒按最长间隔多次保存，停止后再保存最终内容', async () => {
    const h = setup()
    for (let i = 0; i < 90; i++) {
      type(h, `v${i}`)
      await vi.advanceTimersByTimeAsync(1000)
    }
    expect(h.saves.length).toBe(3)
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.saves.at(-1)!.markdown).toBe('v89')
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('保存中继续输入：成功只确认请求快照，界面保持有修改', async () => {
    const h = setup()
    let release!: () => void
    h.setSaveImpl(
      (body) =>
        new Promise((resolve) => {
          release = () => {
            h.server = { ...h.server, markdown: body.markdown, revision: h.server.revision + 1 }
            resolve({ id: 'd1', revision: h.server.revision, updated_at: Date.now(), title: body.title })
          }
        }),
    )
    type(h, 'first')
    await vi.advanceTimersByTimeAsync(3000)
    expect(h.ctrl.view().state).toBe('saving')
    type(h, 'first and more')
    release()
    await vi.advanceTimersByTimeAsync(0)
    expect(h.ctrl.view().state).toBe('dirty')
    expect(h.ctrl.getConfirmed().markdown).toBe('first')
    // 恢复默认实现后，第二轮保存基于新 revision
    h.setSaveImpl(async (body) => {
      h.server = { ...h.server, markdown: body.markdown, revision: h.server.revision + 1 }
      return { id: 'd1', revision: h.server.revision, updated_at: Date.now(), title: body.title }
    })
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.saves.at(-1)).toMatchObject({ markdown: 'first and more', expected_revision: 2 })
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('同一文档只有一个请求在途', async () => {
    const h = setup()
    let inFlight = 0
    let maxInFlight = 0
    h.setSaveImpl(async (body) => {
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      await new Promise((r) => setTimeout(r, 5000))
      inFlight--
      h.server = { ...h.server, markdown: body.markdown, revision: h.server.revision + 1 }
      return { id: 'd1', revision: h.server.revision, updated_at: Date.now(), title: body.title }
    })
    type(h, 'a')
    await vi.advanceTimersByTimeAsync(3000)
    void h.ctrl.saveNow()
    void h.ctrl.saveNow()
    type(h, 'ab')
    void h.ctrl.saveNow()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(maxInFlight).toBe(1)
    expect(h.saves.at(-1)!.markdown).toBe('ab')
  })

  it('临时失败有界退避重试，5 次后暂停，不依赖新的输入', async () => {
    const h = setup()
    h.setSaveImpl(async () => {
      throw new ApiError({ status: 0, code: 'NETWORK_ERROR', message: 'down', kind: 'network' })
    })
    type(h, 'x')
    await vi.advanceTimersByTimeAsync(3000)
    expect(h.saves).toHaveLength(1)
    expect(h.ctrl.view().state).toBe('retry_wait')
    const delays = [2000, 4000, 8000, 16_000, 30_000]
    for (const [i, d] of delays.entries()) {
      await vi.advanceTimersByTimeAsync(d)
      expect(h.saves).toHaveLength(i + 2)
    }
    expect(h.ctrl.view().state).toBe('paused')
    await vi.advanceTimersByTimeAsync(120_000)
    expect(h.saves).toHaveLength(6)
    // 草稿在失败时已写入
    expect(h.drafts.length).toBeGreaterThan(0)
    // 服务恢复后继续
    h.setSaveImpl(async (body) => ({ id: 'd1', revision: 2, updated_at: Date.now(), title: body.title }))
    h.ctrl.resume()
    await vi.advanceTimersByTimeAsync(0)
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('校验错误不自动重发，修改后再尝试', async () => {
    const h = setup()
    h.ctrl.updateTitle('   ')
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.saves).toHaveLength(0)
    expect(h.ctrl.view().state).toBe('paused')
    expect(h.ctrl.view().error?.local).toBe(true)
    h.ctrl.updateTitle('新标题')
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.saves).toHaveLength(1)
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('409 冲突进入冲突状态并暂停自动写入', async () => {
    const h = setup()
    h.server = { ...h.server, markdown: 'other tab', revision: 2 }
    type(h, 'mine')
    await vi.advanceTimersByTimeAsync(3100)
    const view = h.ctrl.view()
    expect(view.state).toBe('conflict')
    expect(view.conflict?.server?.markdown).toBe('other tab')
    type(h, 'mine 2')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(h.saves).toHaveLength(1)
    // 明确合并后以最新 revision 保存（调用方先把合并结果写入编辑器）
    h.editor.value = 'merged'
    await h.ctrl.applyMerged({ title: '标题', markdown: 'merged' }, h.server)
    expect(h.saves.at(-1)).toMatchObject({ markdown: 'merged', expected_revision: 2 })
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('响应丢失后重试收到冲突，若服务端内容与已发快照一致则识别为已保存', async () => {
    const h = setup()
    let first = true
    h.setSaveImpl(async (body) => {
      if (body.expected_revision !== h.server.revision) {
        throw new ApiError({ status: 409, code: 'REVISION_CONFLICT', message: '冲突', kind: 'http' })
      }
      h.server = { ...h.server, markdown: body.markdown, revision: h.server.revision + 1 }
      if (first) {
        first = false
        throw new ApiError({ status: 0, code: 'TIMEOUT', message: 'timeout', kind: 'timeout' })
      }
      return { id: 'd1', revision: h.server.revision, updated_at: Date.now(), title: body.title }
    })
    type(h, 'saved but lost')
    await vi.advanceTimersByTimeAsync(3000)
    expect(h.ctrl.view().state).toBe('retry_wait')
    await vi.advanceTimersByTimeAsync(2000)
    expect(h.ctrl.view().state).toBe('clean')
    expect(h.ctrl.view().revision).toBe(2)
  })

  it('DATA_EPOCH_CHANGED 阻止写入且不自动重发', async () => {
    const h = setup()
    const onEpoch = vi.fn()
    h.setSaveImpl(async () => {
      throw new ApiError({ status: 409, code: 'DATA_EPOCH_CHANGED', message: 'epoch', kind: 'http' })
    })
    ;(h.ctrl as unknown as { deps: { onEpochChanged: () => void } }).deps.onEpochChanged = onEpoch
    type(h, 'x')
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.ctrl.view().state).toBe('blocked')
    expect(h.ctrl.view().blocked).toBe('epoch_changed')
    expect(onEpoch).toHaveBeenCalled()
    type(h, 'xy')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(h.saves).toHaveLength(1)
  })

  it('维护中不发送请求，服务恢复后继续', async () => {
    const h = setup()
    h.service.state = 'maintenance'
    type(h, 'during backup')
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.saves).toHaveLength(0)
    expect(h.ctrl.view().waitingForService).toBe('maintenance')
    h.service.state = 'ok'
    h.ctrl.resume()
    await vi.advanceTimersByTimeAsync(0)
    expect(h.saves).toHaveLength(1)
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('手动保存请求显式快照', async () => {
    const h = setup()
    type(h, 'manual')
    await h.ctrl.saveNow()
    expect(h.saves[0]).toMatchObject({ snapshot: true })
  })

  it('编辑器规范化 Markdown 不被视为修改', async () => {
    const h = setup()
    h.ctrl.setEditorBaseline('hello\n')
    h.editor.value = 'hello\n'
    h.ctrl.updateMarkdown('hello\n')
    await vi.advanceTimersByTimeAsync(5000)
    expect(h.saves).toHaveLength(0)
    expect(h.ctrl.view().state).toBe('clean')
  })

  it('恢复草稿后不立即覆盖服务端，继续编辑后才自动保存', async () => {
    const h = setup()
    h.ctrl.restoreDraft({ title: '标题', markdown: 'draft' })
    h.editor.value = 'draft'
    expect(h.ctrl.view().state).toBe('dirty')
    expect(h.ctrl.view().autosaveSuspended).toBe(true)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(h.saves).toHaveLength(0)
    type(h, 'draft+')
    await vi.advanceTimersByTimeAsync(3100)
    expect(h.saves).toHaveLength(1)
  })

  it('flush 完成最新保存，失败时返回 false', async () => {
    const h = setup()
    type(h, 'leaving')
    await expect(h.ctrl.flush()).resolves.toBe(true)
    expect(h.saves.at(-1)!.markdown).toBe('leaving')
    h.setSaveImpl(async () => {
      throw new ApiError({ status: 503, code: 'DATABASE_BUSY', message: 'busy', kind: 'http' })
    })
    type(h, 'leaving 2')
    await expect(h.ctrl.flush()).resolves.toBe(false)
  })

  it('草稿写入失败如实上报', async () => {
    const h = setup()
    ;(h.ctrl as unknown as { deps: { writeDraft: () => Promise<void> } }).deps.writeDraft = async () => {
      throw new Error('QuotaExceededError')
    }
    type(h, 'x')
    await vi.advanceTimersByTimeAsync(900)
    expect(h.ctrl.view().draftStatus).toBe('error')
    expect(await h.ctrl.persistDraftNow()).toBe(false)
  })

  it('保存成功后清理已确认的草稿', async () => {
    const h = setup()
    type(h, 'x')
    await vi.advanceTimersByTimeAsync(900)
    expect(h.ctrl.view().draftStatus).toBe('saved')
    await vi.advanceTimersByTimeAsync(2500)
    expect(h.ctrl.view().state).toBe('clean')
    expect(h.deletedDrafts.length).toBeGreaterThan(0)
  })
})
