import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api'
import type { Settings } from '@/api/types'
import { useUiStore } from './ui'

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<Settings | null>(null)
  const timezone = ref('Asia/Shanghai')

  function apply(data: Settings) {
    settings.value = data
    timezone.value = data.timezone!
    useUiStore().theme = data.theme === 'auto' ? 'system' : data.theme!
  }

  async function load() {
    const data = await api.settings.get()
    apply(data)
    return data
  }

  async function update(patch: Record<string, unknown>) {
    const result = await api.settings.update(patch)
    apply(result.settings)
    return result.settings
  }

  return { settings, timezone, load, update }
})
