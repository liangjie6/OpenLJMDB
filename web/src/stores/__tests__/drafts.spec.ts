import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, reactive, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { draftStore, DraftStoreError, type DraftRecord } from '@/services/drafts'
import { useDraftsStore } from '../drafts'
import { useDocStatusStore } from '../docStatus'
import DraftsView from '@/views/DraftsView.vue'

const mocks = vi.hoisted(() => ({
  probe: vi.fn(),
  listAll: vi.fn(),
  refreshHealth: vi.fn(),
}))

const health = reactive({ instanceId: 'current', refresh: mocks.refreshHealth })

vi.mock('@/services/drafts', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/services/drafts')>(),
  draftStore: { probe: mocks.probe, listAll: mocks.listAll },
}))
vi.mock('@/stores/health', () => ({ useHealthStore: () => health }))
vi.mock('@/services/broadcast', () => ({ tabBus: { subscribe: vi.fn(), post: vi.fn() } }))
vi.mock('@/stores/knowledgeBases', () => ({
  useKnowledgeBaseStore: () => ({ load: vi.fn().mockResolvedValue(undefined), byId: () => null }),
}))
vi.mock('@/stores/ui', () => ({ useUiStore: () => ({ resolvedTheme: 'light' }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/components/AppTopBar.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/workspace/MarkdownViewer.vue', () => ({ default: { render: () => null } }))

function draft(instance: string, document: string, session: string): DraftRecord {
  return {
    key: `${instance}|${document}|${session}`,
    instance_id: instance,
    document_id: document,
    editor_session_id: session,
    tab_session_id: session,
    knowledge_base_id: null,
    base_data_epoch: 'epoch',
    base_revision: 1,
    title: `草稿 ${session}`,
    markdown: 'abc',
    local_seq: 1,
    created_at: 1,
    updated_at: 2,
  }
}

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

async function flush() {
  await new Promise((done) => setTimeout(done, 0))
  await nextTick()
}

let app: App | null = null

beforeEach(() => {
  vi.resetAllMocks()
  setActivePinia(createPinia())
  health.instanceId = 'current'
  mocks.probe.mockResolvedValue(undefined)
  mocks.listAll.mockResolvedValue([])
})

afterEach(() => {
  app?.unmount()
  app = null
  document.body.replaceChildren()
})

describe('本地草稿初始化', () => {
  it('首次刷新会检测 IndexedDB，等待检测完成后读取草稿', async () => {
    const pending = deferred()
    mocks.probe.mockReturnValue(pending.promise)
    const store = useDraftsStore()
    const refresh = store.refresh()
    expect(store.available).toBeNull()
    expect(draftStore.listAll).not.toHaveBeenCalled()
    pending.resolve()
    await refresh
    expect(store.available).toBe(true)
    expect(draftStore.listAll).toHaveBeenCalledOnce()
  })

  it('启动、草稿页与编辑器并发检测时共用一次检测', async () => {
    const pending = deferred()
    mocks.probe.mockReturnValue(pending.promise)
    const store = useDraftsStore()
    const operations = [store.probe(), store.refresh(), store.probe()]
    expect(draftStore.probe).toHaveBeenCalledOnce()
    pending.resolve()
    await Promise.all(operations)
    expect(store.available).toBe(true)
  })

  it('首次健康请求尚未完成时，等取得实例 ID 后读取草稿', async () => {
    const pending = deferred()
    health.instanceId = ''
    mocks.refreshHealth.mockImplementation(async () => {
      await pending.promise
      health.instanceId = 'current'
    })
    const store = useDraftsStore()
    const refresh = store.refresh()
    await flush()
    expect(draftStore.listAll).not.toHaveBeenCalled()
    pending.resolve()
    await refresh
    expect(draftStore.listAll).toHaveBeenCalledOnce()
  })

  it('实际检测失败才标记不可用，重试成功后清除错误并恢复读取', async () => {
    mocks.probe.mockRejectedValueOnce(new DraftStoreError('unavailable', '存储被禁用'))
    const store = useDraftsStore()
    await store.refresh()
    expect(store.available).toBe(false)
    expect(store.errorMessage).toBe('存储被禁用')
    expect(draftStore.listAll).not.toHaveBeenCalled()
    await store.probe()
    await store.refresh()
    expect(store.available).toBe(true)
    expect(store.errorMessage).toBeNull()
    expect(draftStore.listAll).toHaveBeenCalledOnce()
  })

  it('将当前实例与其他实例的草稿分开，文档计数只包含当前实例', async () => {
    const current = draft('current', 'doc', 'one')
    const other = draft('old', 'doc', 'two')
    mocks.listAll.mockResolvedValue([current, other])
    const store = useDraftsStore()
    await store.refresh()
    expect(store.instanceDrafts).toEqual([current])
    expect(store.otherDrafts).toEqual([other])
    expect(useDocStatusStore().draftCounts).toEqual({ doc: 1 })
  })

  it('草稿读取失败时显示错误，避免误报没有草稿', async () => {
    mocks.listAll.mockRejectedValue(new DraftStoreError('failed', '草稿读取失败'))
    const store = useDraftsStore()
    await store.refresh()
    expect(store.available).toBe(false)
    expect(store.errorMessage).toBe('草稿读取失败')
  })
})

describe('本地草稿页面', () => {
  function mountPage() {
    const container = document.createElement('div')
    document.body.append(container)
    app = createApp(DraftsView)
    app.use(createPinia())
    app.mount(container)
    return container
  }

  it('直接打开草稿页时显示检测中，检测成功后显示空列表', async () => {
    const pending = deferred()
    mocks.probe.mockReturnValue(pending.promise)
    const container = mountPage()
    expect(container.textContent).toContain('正在检查本地草稿存储')
    expect(container.textContent).not.toContain('浏览器本地存储不可用')
    pending.resolve()
    await flush()
    expect(container.textContent).toContain('没有本地草稿')
    expect(container.textContent).not.toContain('浏览器本地存储不可用')
  })

  it('显示真实失败原因，并允许通过重试恢复', async () => {
    mocks.probe.mockRejectedValueOnce(new DraftStoreError('unavailable', '存储被禁用'))
    const container = mountPage()
    await flush()
    expect(container.textContent).toContain('浏览器本地存储不可用')
    expect(container.textContent).toContain('存储被禁用')
    container.querySelector<HTMLButtonElement>('.empty-actions button')!.click()
    await flush()
    expect(container.textContent).toContain('没有本地草稿')
    expect(mocks.probe).toHaveBeenCalledTimes(2)
  })

  it('每份草稿只展示并统计一次', async () => {
    mocks.listAll.mockResolvedValue([draft('current', 'doc', 'one'), draft('old', 'doc', 'two')])
    const container = mountPage()
    await flush()
    expect(container.querySelectorAll('.draft-item')).toHaveLength(2)
    expect(container.textContent).toContain('2 份草稿（约 6 字）')
    expect(container.textContent).toContain('当前数据实例的草稿（1 份）')
    expect(container.textContent).toContain('其他数据实例的草稿（1 份')
  })
})
