import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { readLocal, writeLocal } from '@/utils/storage'

export type ThemePreference = 'light' | 'dark' | 'system'
export type DocumentView = 'grid' | 'list' | 'compact'

export interface ToastAction {
  label: string
  handler: () => void
}

export interface Toast {
  id: number
  kind: 'info' | 'success' | 'warning' | 'error'
  message: string
  detail?: string
  action?: ToastAction
  timeout: number
}

let toastSeq = 0

export const useUiStore = defineStore('ui', () => {
  const theme = ref<ThemePreference>(readLocal<ThemePreference>('theme', 'system'))
  const resolvedTheme = ref<'light' | 'dark'>('light')
  const outlineVisible = ref<boolean>(readLocal('outline-visible', true))
  const storedDocumentView = readLocal<unknown>('document-view', 'grid')
  const documentView = ref<DocumentView>(
    storedDocumentView === 'list' || storedDocumentView === 'compact' ? storedDocumentView : 'grid',
  )
  // 桌面折叠偏好与手机抽屉状态独立，切换文档不会重置桌面布局。
  const sidebarCollapsed = ref(readLocal<unknown>('sidebar-collapsed', false) === true)
  const sidebarOpen = ref(false)
  const sidebarMedia = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(max-width: 900px)') : null
  const sidebarIsDrawer = ref(sidebarMedia?.matches ?? false)
  const sidebarVisible = computed(() => sidebarIsDrawer.value ? sidebarOpen.value : !sidebarCollapsed.value)
  sidebarMedia?.addEventListener?.('change', (event) => {
    sidebarIsDrawer.value = event.matches
    sidebarOpen.value = false
  })

  function toggleSidebar() {
    if (sidebarIsDrawer.value) sidebarOpen.value = !sidebarOpen.value
    else sidebarCollapsed.value = !sidebarCollapsed.value
  }
  const outlineDrawerOpen = ref(false)
  const toasts = ref<Toast[]>([])
  /** 屏幕阅读器播报（polite） */
  const announcement = ref('')

  const media = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null

  function applyTheme() {
    const dark = theme.value === 'dark' || (theme.value === 'system' && !!media?.matches)
    resolvedTheme.value = dark ? 'dark' : 'light'
    document.documentElement.dataset.theme = resolvedTheme.value
    document.documentElement.style.colorScheme = resolvedTheme.value
  }
  media?.addEventListener?.('change', applyTheme)
  watch(theme, (value) => {
    writeLocal('theme', value)
    applyTheme()
  })
  watch(outlineVisible, (value) => writeLocal('outline-visible', value))
  watch(documentView, (value) => writeLocal('document-view', value))
  watch(sidebarCollapsed, (value) => writeLocal('sidebar-collapsed', value))

  function toast(input: Omit<Toast, 'id' | 'timeout'> & { timeout?: number }): number {
    const id = ++toastSeq
    const timeout = input.timeout ?? (input.kind === 'error' ? 10_000 : input.action ? 8000 : 4000)
    toasts.value.push({ ...input, id, timeout })
    if (toasts.value.length > 5) toasts.value.shift()
    if (timeout > 0) setTimeout(() => dismiss(id), timeout)
    return id
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  function announce(text: string) {
    announcement.value = ''
    requestAnimationFrame(() => (announcement.value = text))
  }

  return {
    theme,
    resolvedTheme,
    outlineVisible,
    documentView,
    sidebarCollapsed,
    sidebarOpen,
    sidebarIsDrawer,
    sidebarVisible,
    toggleSidebar,
    outlineDrawerOpen,
    toasts,
    announcement,
    applyTheme,
    toast,
    dismiss,
    announce,
  }
})
