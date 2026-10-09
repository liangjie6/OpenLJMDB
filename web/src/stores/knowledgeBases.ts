import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api'
import { ApiError, isApiError } from '@/api/http'
import type { KnowledgeBase } from '@/api/types'
import { tabBus } from '@/services/broadcast'
import { editorSessionId } from '@/services/session'
import { useHealthStore } from './health'

/** 列表按最后活动时间倒序，并以 ID 打破并列（01 文档 5.1） */
function sortKbs(list: KnowledgeBase[]): KnowledgeBase[] {
  return [...list].sort((a, b) => b.updated_at - a.updated_at || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
}

export const useKnowledgeBaseStore = defineStore('knowledgeBases', () => {
  const items = ref<KnowledgeBase[]>([])
  const loaded = ref(false)
  const loading = ref(false)
  const error = ref<ApiError | null>(null)

  const sorted = computed(() => sortKbs(items.value))

  function byId(id: string | null | undefined): KnowledgeBase | undefined {
    if (!id) return undefined
    return items.value.find((kb) => kb.id === id)
  }

  async function load(): Promise<void> {
    loading.value = true
    try {
      items.value = await api.knowledgeBases.listAll()
      loaded.value = true
      error.value = null
    } catch (e) {
      // 保留最后一次有效数据，并标记可能过期
      error.value = isApiError(e) ? e : null
      throw e
    } finally {
      loading.value = false
    }
  }

  function upsert(kb: KnowledgeBase) {
    const index = items.value.findIndex((item) => item.id === kb.id)
    if (index >= 0) items.value.splice(index, 1, kb)
    else items.value.push(kb)
  }

  function notifyChanged() {
    const health = useHealthStore()
    tabBus.post({ type: 'kb-changed', instance_id: health.instanceId, session_id: editorSessionId })
  }

  async function create(input: { name: string; description: string }, idempotencyKey: string) {
    const kb = await api.knowledgeBases.create(input, idempotencyKey)
    upsert(kb)
    notifyChanged()
    return kb
  }

  async function update(id: string, input: { name: string; description: string }) {
    const kb = await api.knowledgeBases.update(id, input)
    upsert(kb)
    notifyChanged()
    return kb
  }

  async function remove(kb: KnowledgeBase, expectedTreeRevision: number | null) {
    const result = await api.knowledgeBases.remove(kb.id, expectedTreeRevision)
    items.value = items.value.filter((item) => item.id !== kb.id)
    notifyChanged()
    return result
  }

  /** 本页写入后更新最后活动时间与数量，避免整表刷新 */
  function touch(id: string, patch: Partial<Pick<KnowledgeBase, 'updated_at' | 'document_count'>> & { documentDelta?: number }) {
    const kb = byId(id)
    if (!kb) return
    upsert({
      ...kb,
      updated_at: patch.updated_at ?? Date.now(),
      document_count:
        patch.document_count ?? Math.max(0, kb.document_count + (patch.documentDelta ?? 0)),
    })
  }

  tabBus.subscribe((message) => {
    if (message.type === 'kb-changed' && loaded.value) void load().catch(() => undefined)
  })

  return { items, sorted, loaded, loading, error, byId, load, create, update, remove, upsert, touch }
})
