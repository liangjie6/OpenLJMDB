import { defineStore } from 'pinia'
import { ref } from 'vue'
import { draftStore, DraftStoreError, type DraftRecord } from '@/services/drafts'
import { tabBus } from '@/services/broadcast'
import { editorSessionId } from '@/services/session'
import { useDocStatusStore } from './docStatus'
import { useHealthStore } from './health'

/** IndexedDB 可用性与当前实例草稿概况 */
export const useDraftsStore = defineStore('drafts', () => {
  const available = ref<boolean | null>(null)
  const errorMessage = ref<string | null>(null)
  const instanceDrafts = ref<DraftRecord[]>([])
  const otherDrafts = ref<DraftRecord[]>([])
  let probing: Promise<void> | null = null

  function probe(): Promise<void> {
    if (probing) return probing
    available.value = null
    probing = (async () => {
      try {
        await draftStore.probe()
        available.value = true
        errorMessage.value = null
      } catch (error) {
        available.value = false
        errorMessage.value = error instanceof DraftStoreError ? error.message : '本地草稿存储不可用'
      } finally {
        probing = null
      }
    })()
    return probing
  }

  function reportWriteError(message: string) {
    errorMessage.value = message
  }

  function clearWriteError() {
    if (available.value) errorMessage.value = null
  }

  async function refresh() {
    if (available.value === null) await probe()
    if (!available.value) return
    const health = useHealthStore()
    if (!health.instanceId) await health.refresh()
    if (!health.instanceId) return
    try {
      const all = await draftStore.listAll()
      instanceDrafts.value = all.filter((draft) => draft.instance_id === health.instanceId)
      otherDrafts.value = all.filter((draft) => draft.instance_id !== health.instanceId)
      const counts = new Map<string, number>()
      for (const draft of instanceDrafts.value) counts.set(draft.document_id, (counts.get(draft.document_id) ?? 0) + 1)
      useDocStatusStore().setDraftCounts(counts)
    } catch (error) {
      available.value = false
      errorMessage.value = error instanceof DraftStoreError ? error.message : '读取本地草稿失败，请重试'
    }
  }

  function notifyChanged() {
    const health = useHealthStore()
    tabBus.post({ type: 'drafts-changed', instance_id: health.instanceId, session_id: editorSessionId })
    void refresh()
  }

  tabBus.subscribe((message) => {
    if (message.type === 'drafts-changed') void refresh()
  })

  return {
    available,
    errorMessage,
    instanceDrafts,
    otherDrafts,
    probe,
    refresh,
    notifyChanged,
    reportWriteError,
    clearWriteError
  }
})
