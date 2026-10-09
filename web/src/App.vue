<script setup lang="ts">
import { onMounted, onBeforeUnmount } from 'vue'
import { RouterView } from 'vue-router'
import router from '@/router'
import { leaveGuard } from '@/router/leaveGuard'
import { useHealthStore } from '@/stores/health'
import { useUiStore } from '@/stores/ui'
import { useDraftsStore } from '@/stores/drafts'
import { useSettingsStore } from '@/stores/settings'
import DialogHost from '@/components/base/DialogHost.vue'
import ToastHost from '@/components/base/ToastHost.vue'

router.beforeEach(leaveGuard)

const health = useHealthStore()
const ui = useUiStore()
const drafts = useDraftsStore()

const preferences = useSettingsStore()
let healthInterval: ReturnType<typeof setInterval>

onMounted(() => {
  ui.applyTheme()
  void preferences.load().catch(() => ui.toast({ kind: 'error', message: '加载应用设置失败，请在设置页面重试' }))
  void health.refresh()
  void drafts.probe().then(() => drafts.refresh())
  healthInterval = setInterval(() => void health.refresh(), 15_000)
})
onBeforeUnmount(() => clearInterval(healthInterval))
</script>

<template>
  <div id="app" :data-theme="ui.resolvedTheme">
    <RouterView />
    <DialogHost />
    <ToastHost />
  </div>
</template>

<style>
@import 'vditor/dist/index.css';
@import '@/styles/base.css';
@import '@/styles/content.css';

#app {
  min-height: 100vh;
  background: var(--bg);
  color: var(--text);
}
</style>
