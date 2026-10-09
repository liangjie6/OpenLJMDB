import { defineStore } from 'pinia'
import { reactive } from 'vue'
import type { SaveState } from '@/services/saveController'

/** 文档保存状态摘要，供文档树标注未保存、失败与冲突 */
export interface DocStatusEntry {
  state: SaveState
  dirty: boolean
}

export const useDocStatusStore = defineStore('docStatus', () => {
  const entries = reactive<Record<string, DocStatusEntry>>({})
  /** 本实例中存在本地草稿的文档 → 草稿数量 */
  const draftCounts = reactive<Record<string, number>>({})

  function set(docId: string, entry: DocStatusEntry | null) {
    if (!entry || (entry.state === 'clean' && !entry.dirty)) delete entries[docId]
    else entries[docId] = entry
  }

  function setDraftCounts(counts: Map<string, number>) {
    for (const key of Object.keys(draftCounts)) delete draftCounts[key]
    for (const [docId, count] of counts) draftCounts[docId] = count
  }

  return { entries, draftCounts, set, setDraftCounts }
})
