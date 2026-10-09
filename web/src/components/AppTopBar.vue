<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import AppIcon from './base/AppIcon.vue'
import { useUiStore } from '@/stores/ui'
import { useHealthStore } from '@/stores/health'
import { codePointLength } from '@/utils/text'

const props = withDefaults(defineProps<{ kbId?: string | null; showSidebarToggle?: boolean }>(), {
  kbId: null,
  showSidebarToggle: false,
})

const route = useRoute()
const router = useRouter()
const ui = useUiStore()
const health = useHealthStore()
const query = ref(typeof route.query.q === 'string' && route.name === 'search' ? route.query.q : '')
const error = ref('')

watch(
  () => route.query.q,
  (q) => {
    if (route.name === 'search' && typeof q === 'string') query.value = q
  },
)

function submit() {
  const q = query.value.trim()
  if (!q) {
    error.value = '请输入搜索关键词'
    return
  }
  const max = health.limits.search_query_max_chars
  if (codePointLength(q) > max) {
    error.value = `关键词最多 ${max} 个字符`
    return
  }
  error.value = ''
  void router.push({ name: 'search', query: { q, kb: props.kbId ?? undefined } })
}

const NAV = [
  { to: '/import', icon: 'import', label: '导入' },
  { to: '/trash', icon: 'trash', label: '回收站' },
  { to: '/backup', icon: 'archive', label: '备份与恢复' },
  { to: '/attachments', icon: 'paperclip', label: '附件管理' },
  { to: '/drafts', icon: 'draft', label: '本地草稿' },
  { to: '/settings', icon: 'settings', label: '设置' },
]

function cycleTheme() {
  ui.theme = ui.resolvedTheme === 'dark' ? 'light' : 'dark'
}
</script>

<template>
  <header class="topbar">
    <button
      v-if="showSidebarToggle"
      type="button"
      class="btn btn-icon sidebar-toggle"
      :aria-label="ui.sidebarVisible ? '折叠侧边栏' : '展开侧边栏'"
      :title="ui.sidebarVisible ? '折叠侧边栏' : '展开侧边栏'"
      aria-controls="workspace-sidebar"
      :aria-expanded="ui.sidebarVisible"
      @click="ui.toggleSidebar()"
    >
      <AppIcon name="sidebar" :size="18" />
    </button>
    <RouterLink to="/" class="brand" aria-label="OpenLJMDB 知识库首页">
      <img class="brand-mark" src="/logo.png" alt="" width="44" height="44" />
      <span class="brand-text">OpenLJMDB</span>
    </RouterLink>
    <slot name="breadcrumb" />
    <div class="spacer" />
    <form class="topbar-search" role="search" @submit.prevent="submit">
      <label class="sr-only" for="global-search">{{ kbId ? '搜索（当前知识库）' : '搜索全部知识库' }}</label>
      <AppIcon name="search" class="search-icon" />
      <input
        id="global-search"
        v-model="query"
        class="input"
        type="search"
        :placeholder="kbId ? '搜索本知识库…' : '搜索全部知识库…'"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="error ? 'global-search-error' : undefined"
        autocomplete="off"
        @input="error = ''"
      />
      <span v-if="error" id="global-search-error" class="search-error" role="alert">{{ error }}</span>
    </form>
    <nav class="topbar-nav" aria-label="全局导航">
      <RouterLink
        v-for="item in NAV"
        :key="item.to"
        :to="item.to"
        class="btn btn-icon"
        :title="item.label"
        :aria-label="item.label"
        active-class="active"
      >
        <AppIcon :name="item.icon" :size="18" />
      </RouterLink>
      <button
        type="button"
        class="btn btn-icon"
        :aria-label="ui.resolvedTheme === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
        :title="ui.resolvedTheme === 'dark' ? '浅色主题' : '深色主题'"
        @click="cycleTheme"
      >
        <AppIcon :name="ui.resolvedTheme === 'dark' ? 'sun' : 'moon'" :size="18" />
      </button>
    </nav>
  </header>
</template>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: 40;
  display: flex;
  align-items: center;
  gap: 10px;
  height: var(--topbar-height);
  padding: 0 12px 0 14px;
  background: var(--bg);
  border-bottom: 1px solid var(--border);
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
  font-weight: 700;
  font-size: 15px;
  text-decoration: none;
  flex: none;
}

.brand-mark {
  display: block;
  width: 44px;
  height: 44px;
  object-fit: contain;
  flex: none;
}

.topbar-search {
  position: relative;
  width: min(320px, 34vw);
}

.topbar-search .input {
  padding-left: 30px;
  min-height: 32px;
  background: var(--bg-soft);
}

.search-icon {
  position: absolute;
  left: 9px;
  top: 8px;
  color: var(--text-3);
}

.search-error {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  padding: 2px 8px;
  font-size: 12px;
  color: var(--danger);
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  white-space: nowrap;
}

.topbar-nav {
  display: flex;
  gap: 2px;
}

.topbar-nav .active {
  color: var(--primary-text);
  background: var(--primary-soft);
}

.sidebar-toggle {
  flex: none;
}

@media (max-width: 720px) {
  .brand-text {
    display: none;
  }
  .topbar-search {
    width: auto;
    flex: 1;
  }
  .topbar-nav .btn:not(:last-child):not([href='/settings']) {
    display: none;
  }
}
</style>
