import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api'
import { ApiError, isApiError, setRequestEpoch } from '@/api/http'
import type { Health, Limits } from '@/api/types'
import { tabBus } from '@/services/broadcast'
import { editorSessionId } from '@/services/session'
import type { ServiceState } from '@/services/saveController'
import { mergeLimits } from '@/utils/limits'

export type ConnectionStatus = 'loading' | 'ok' | 'disconnected' | 'error'

/**
 * 健康状态与数据世代。
 * 页面首次取得的 instance_id / data_epoch 固定为本页面的写入世代；
 * 之后若服务端世代变化（完整恢复、切换数据目录），页面暂停写入并要求重新加载。
 */
export const useHealthStore = defineStore('health', () => {
  const health = ref<Health | null>(null)
  const status = ref<ConnectionStatus>('loading')
  const lastError = ref<ApiError | null>(null)
  const lastOkAt = ref<number | null>(null)
  const pageInstanceId = ref<string | null>(null)
  const pageEpoch = ref<string | null>(null)
  const epochChanged = ref(false)
  const instanceChanged = ref(false)
  const settingsLimits = ref<Partial<Limits> | null>(null)
  const restoreCompleted = ref(false)

  const maintenance = computed(() => (health.value?.maintenance.active ? health.value.maintenance : null))
  const writable = computed(() => health.value?.writable !== false)
  const limits = computed<Limits>(() => mergeLimits(health.value?.limits, settingsLimits.value))
  const instanceId = computed(() => pageInstanceId.value ?? '')
  /** 是否允许发起写请求（界面按钮可据此禁用） */
  const canWrite = computed(
    () => status.value === 'ok' && writable.value && !maintenance.value && !epochChanged.value,
  )
  const serviceState = computed<ServiceState>(() => {
    if (maintenance.value) return 'maintenance'
    if (!writable.value) return 'readonly'
    return 'ok'
  })

  const recoveryListeners = new Set<() => void>()
  let timer: ReturnType<typeof setTimeout> | null = null
  let polling = false
  let inFlight: Promise<void> | null = null

  function applyHealth(next: Health) {
    const wasBlocked = status.value !== 'ok' || !!maintenance.value || !writable.value
    health.value = next
    status.value = 'ok'
    lastError.value = null
    lastOkAt.value = Date.now()
    if (pageEpoch.value === null) {
      pageInstanceId.value = next.instance_id
      pageEpoch.value = next.data_epoch
      setRequestEpoch(next.data_epoch)
    } else {
      if (next.instance_id && pageInstanceId.value && next.instance_id !== pageInstanceId.value) {
        instanceChanged.value = true
        epochChanged.value = true
      }
      if (next.data_epoch && next.data_epoch !== pageEpoch.value) epochChanged.value = true
    }
    const nowBlocked = !!maintenance.value || !writable.value
    if (wasBlocked && !nowBlocked && !epochChanged.value) {
      for (const listener of recoveryListeners) listener()
    }
  }

  async function refresh(): Promise<void> {
    if (inFlight) return inFlight
    inFlight = (async () => {
      try {
        applyHealth(await api.health())
      } catch (error) {
        lastError.value = isApiError(error)
          ? error
          : new ApiError({ status: 0, code: 'NETWORK_ERROR', message: String(error), kind: 'network' })
        status.value = pageEpoch.value === null && lastError.value.kind === 'http' ? 'error' : 'disconnected'
      } finally {
        inFlight = null
      }
    })()
    return inFlight
  }

  function schedule() {
    if (!polling) return
    if (timer) clearTimeout(timer)
    const fast = status.value !== 'ok' || !!maintenance.value
    timer = setTimeout(async () => {
      await refresh()
      schedule()
    }, fast ? 3000 : 15_000)
  }

  function startPolling() {
    if (polling) return
    polling = true
    schedule()
    const onWake = () => {
      if (document.visibilityState === 'visible') void refresh().then(schedule)
    }
    document.addEventListener('visibilitychange', onWake)
    window.addEventListener('focus', onWake)
    window.addEventListener('online', onWake)
  }

  /** 写请求返回 DATA_EPOCH_CHANGED：暂停全部写入，等待用户重新加载 */
  function markEpochChanged() {
    epochChanged.value = true
    void refresh()
  }

  function notifyRestoreCompleted() {
    restoreCompleted.value = true
    epochChanged.value = true
    tabBus.post({
      type: 'reload-required',
      instance_id: pageInstanceId.value ?? '',
      session_id: editorSessionId,
      reason: 'restore',
    })
  }

  function onRecovered(listener: () => void): () => void {
    recoveryListeners.add(listener)
    return () => recoveryListeners.delete(listener)
  }

  tabBus.subscribe((message) => {
    if (message.type === 'reload-required' && (!pageInstanceId.value || message.instance_id === pageInstanceId.value)) {
      epochChanged.value = true
      if (message.reason === 'restore') restoreCompleted.value = true
      void refresh()
    }
  })

  return {
    health,
    status,
    lastError,
    lastOkAt,
    pageInstanceId,
    pageEpoch,
    epochChanged,
    instanceChanged,
    restoreCompleted,
    settingsLimits,
    maintenance,
    writable,
    limits,
    instanceId,
    canWrite,
    serviceState,
    refresh,
    startPolling,
    markEpochChanged,
    notifyRestoreCompleted,
    onRecovered,
  }
})
