import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, reactive, type App } from 'vue'
import ImportView from '../ImportView.vue'
import type { ImportPreview } from '@/api/types'
import { ApiError } from '@/api/http'

const mocks = vi.hoisted(() => ({
  preflight: vi.fn(), classify: vi.fn(), commit: vi.fn(),
  loadKbs: vi.fn(), loadSettings: vi.fn(), loadTree: vi.fn(), invalidate: vi.fn(),
}))

const libraries = [{ id: 'kb-a', name: 'C++' }, { id: 'kb-b', name: '数据库' }]
const settings = reactive({ import_auto_classify: false, import_classifier_configured: true })
const trees = reactive<Record<string, { loading: boolean; error: null }>>({})
vi.mock('@/api', () => ({ api: { imports: { preflight: mocks.preflight, classify: mocks.classify, commit: mocks.commit } } }))
vi.mock('@/stores/knowledgeBases', () => ({ useKnowledgeBaseStore: () => ({
  sorted: libraries, load: mocks.loadKbs, byId: (id: string) => libraries.find((kb) => kb.id === id),
}) }))
vi.mock('@/stores/settings', () => ({ useSettingsStore: () => ({ settings, load: mocks.loadSettings }) }))
vi.mock('@/stores/health', () => ({ useHealthStore: () => ({ canWrite: true, limits: {
  tree_max_depth: 32, markdown_max_bytes: 10485760, zip_max_bytes: 524288000,
  zip_max_expanded_bytes: 1073741824, zip_max_entries: 10000,
} }) }))
vi.mock('@/stores/tree', () => ({ useTreeStore: () => ({
  trees, load: mocks.loadTree, invalidate: mocks.invalidate,
  childrenOf: (id: string, parent: string | null) => parent ? [] : [{ id: `parent-${id}`, title: '分类子目录' }],
  node: (id: string, parent: string) => parent === `parent-${id}` ? { id: parent } : undefined,
  depthOf: () => 1, pathTitles: () => ['分类子目录'],
}) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
vi.mock('@/components/AppTopBar.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/KbFormDialog.vue', () => ({ default: { render: () => null } }))

let app: App | null = null

function preview(kind: 'markdown' | 'zip' = 'markdown'): ImportPreview {
  return {
    preview_id: 'preview', source_kind: kind, expires_at: null, source_name: null,
    document_count: 2, attachment_count: 0, total_bytes: 20, max_depth: kind === 'zip' ? 2 : 1,
    documents: [
      { source_id: 'source-a', title: '第一篇', path: 'a.md', parent_path: null, depth: 1, duplicate_title: false },
      { source_id: 'source-b', title: '第二篇', path: 'b.md', parent_path: kind === 'zip' ? 'a.md' : null, depth: kind === 'zip' ? 2 : 1, duplicate_title: false },
    ], warnings: [], errors: [],
  }
}

function button(container: HTMLElement, label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button')].find((b) => b.textContent?.trim() === label)
  if (!found) throw new Error(`Missing button: ${label}`)
  return found
}

async function flush() {
  await new Promise((done) => setTimeout(done, 0))
  await nextTick()
}

async function upload(kind: 'markdown' | 'zip' = 'markdown') {
  const container = document.createElement('div')
  document.body.append(container)
  app = createApp(ImportView)
  app.component('RouterLink', { props: ['to'], template: '<a :href="to"><slot /></a>' })
  app.mount(container)
  await flush()
  const input = container.querySelector<HTMLInputElement>('input[type=file]')!
  Object.defineProperty(input, 'files', { value: kind === 'zip'
    ? [new File(['zip'], 'notes.zip')]
    : [new File(['first'], 'a.md'), new File(['second'], 'b.md')], configurable: true })
  input.dispatchEvent(new Event('change'))
  await nextTick()
  button(container, '开始预检').click()
  await flush()
  await flush()
  return container
}

async function select(container: HTMLElement, id: string, value: string) {
  const el = container.querySelector<HTMLSelectElement>(`#${id}`)!
  el.value = value
  el.dispatchEvent(new Event('change'))
  await flush()
}

beforeEach(() => {
  vi.resetAllMocks()
  sessionStorage.clear()
  settings.import_auto_classify = false
  settings.import_classifier_configured = true
  mocks.loadKbs.mockResolvedValue(undefined)
  mocks.loadSettings.mockResolvedValue(settings)
  mocks.loadTree.mockImplementation(async (id: string) => ({ treeRevision: id === 'kb-a' ? 7 : 11 }))
  mocks.preflight.mockResolvedValue(preview())
  mocks.classify.mockImplementation(async (_preview: string, source: string) => ({
    source_id: source, target_knowledge_base_id: source === 'source-a' ? 'kb-b' : 'kb-a',
    target_parent_id: null, probability: 0.55, confidence: 0.1,
  }))
  mocks.commit.mockResolvedValue({ result: { document_count: 2, documents: [] } })
})

afterEach(() => {
  app?.unmount()
  app = null
  document.body.innerHTML = ''
})

describe('逐篇导入位置', () => {
  it('关闭自动分类时，可以手动为每篇选择不同知识库和父文档', async () => {
    const container = await upload()
    expect(mocks.classify).not.toHaveBeenCalled()
    expect(container.querySelectorAll('.destination-card')).toHaveLength(2)
    await select(container, 'destination-kb-1', 'kb-b')
    await select(container, 'destination-parent-1', 'parent-kb-b')
    button(container, '确认导入').click()
    await flush()
    expect(mocks.commit.mock.calls[0]?.[0]).toEqual({ preview_id: 'preview', targets: [
      { source_id: 'source-a', target_knowledge_base_id: 'kb-a', target_parent_id: null, expected_tree_revision: 7 },
      { source_id: 'source-b', target_knowledge_base_id: 'kb-b', target_parent_id: 'parent-kb-b', expected_tree_revision: 11 },
    ] })
    expect(mocks.invalidate).toHaveBeenCalledWith('kb-a')
    expect(mocks.invalidate).toHaveBeenCalledWith('kb-b')
  })

  it('自动分类回填一个知识库，低置信度也采用结果，人工修改优先', async () => {
    settings.import_auto_classify = true
    const container = await upload()
    expect(mocks.classify).toHaveBeenCalledTimes(2)
    expect(container.querySelector<HTMLSelectElement>('#destination-kb-0')!.value).toBe('kb-b')
    expect(container.textContent).toContain('匹配概率 55%')
    await select(container, 'destination-kb-0', 'kb-a')
    button(container, '确认导入').click()
    await flush()
    expect(mocks.commit.mock.calls[0]?.[0].targets.map((d: { target_knowledge_base_id: string }) => d.target_knowledge_base_id)).toEqual(['kb-a', 'kb-a'])
  })

  it('自动分类失败后仍然可以手动导入', async () => {
    settings.import_auto_classify = true
    mocks.classify.mockRejectedValue(new Error('分类服务暂时不可用'))
    const container = await upload()
    expect(container.textContent).toContain('分类服务暂时不可用')
    expect(button(container, '确认导入').disabled).toBe(false)
    button(container, '确认导入').click()
    await flush()
    expect(mocks.commit).toHaveBeenCalledOnce()
  })

  it('未配置密钥时不调用分类接口，仍可手动选择位置', async () => {
    settings.import_auto_classify = true
    settings.import_classifier_configured = false
    const container = await upload()
    expect(mocks.classify).not.toHaveBeenCalled()
    expect(container.textContent).toContain('分类服务尚未配置')
    expect(button(container, '确认导入').disabled).toBe(false)
  })

  it('ZIP 跳过自动分类，沿用整包位置和父子结构', async () => {
    settings.import_auto_classify = true
    mocks.preflight.mockResolvedValue(preview('zip'))
    const container = await upload('zip')
    expect(mocks.classify).not.toHaveBeenCalled()
    expect(container.querySelectorAll('.destination-card')).toHaveLength(0)
    button(container, '确认导入').click()
    await flush()
    expect(mocks.commit.mock.calls[0]?.[0]).toEqual({ preview_id: 'preview', target_knowledge_base_id: 'kb-a', target_parent_id: null, expected_tree_revision: 7 })
  })

  it('停止分类后忽略迟到的结果，不覆盖人工位置', async () => {
    settings.import_auto_classify = true
    let finish!: (value: unknown) => void
    mocks.classify.mockReturnValueOnce(new Promise((resolve) => { finish = resolve }))
    const container = await upload()
    expect(button(container, '确认导入').disabled).toBe(true)
    button(container, '停止分类，手动选择').click()
    await select(container, 'destination-kb-0', 'kb-a')
    finish({ source_id: 'source-a', target_knowledge_base_id: 'kb-b', probability: 0.99 })
    await flush()
    expect(container.querySelector<HTMLSelectElement>('#destination-kb-0')!.value).toBe('kb-a')
    expect(button(container, '确认导入').disabled).toBe(false)
  })

  it('提交返回网关错误时保留原批次，禁止改位置并用同一正文与幂等键重试', async () => {
    mocks.commit.mockRejectedValueOnce(new ApiError({ status: 502, code: 'GATEWAY_ERROR', message: '网关错误', kind: 'http' }))
    const container = await upload()
    button(container, '确认导入').click()
    await flush()
    expect(button(container, '返回确认页').disabled).toBe(true)
    expect(button(container, '重新选择文件').disabled).toBe(true)
    expect(container.textContent).toContain('结果尚未确认')
    const original = mocks.commit.mock.calls[0]
    button(container, '重试').click()
    await flush()
    expect(mocks.commit.mock.calls[1]).toEqual(original)
    expect(container.textContent).toContain('导入完成')
  })
})
