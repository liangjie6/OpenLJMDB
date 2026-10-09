<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import DropdownMenu, { type MenuItem } from '@/components/base/DropdownMenu.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import ExportDialog from '@/components/ExportDialog.vue'
import KbFormDialog from '@/components/KbFormDialog.vue'
import { api } from '@/api'
import type { KnowledgeBase } from '@/api/types'
import { confirmDialog } from '@/composables/useDialogs'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useHealthStore } from '@/stores/health'
import { useDraftsStore } from '@/stores/drafts'
import { useTreeStore } from '@/stores/tree'
import { useUiStore } from '@/stores/ui'
import { formatDateTime } from '@/utils/format'

const router = useRouter()
const kbs = useKnowledgeBaseStore()
const health = useHealthStore()
const drafts = useDraftsStore()
const tree = useTreeStore()
const ui = useUiStore()

const formOpen = ref(false)
const editing = ref<KnowledgeBase | null>(null)
const exportTarget = ref<KnowledgeBase | null>(null)
const exportDialogOpen = ref(false)
const firstLoad = ref(true)

async function load() {
  try {
    await kbs.load()
  } catch {
    // 错误由 kbs.error 展示；保留上次数据
  } finally {
    firstLoad.value = false
  }
}

onMounted(() => {
  void load()
  void drafts.refresh()
})

function openCreate() {
  editing.value = null
  formOpen.value = true
}

function onSaved(kb: KnowledgeBase) {
  if (!editing.value) void router.push(`/knowledge-bases/${kb.id}`)
  else ui.toast({ kind: 'success', message: '知识库信息已保存' })
}

async function remove(kb: KnowledgeBase) {
  let treeRevision = kb.tree_revision
  let count = kb.document_count
  try {
    const data = await api.knowledgeBases.tree(kb.id)
    treeRevision = data.tree_revision
    count = data.nodes.length
  } catch {
    // 使用列表中的数量
  }
  const ok = await confirmDialog({
    title: '将知识库移入回收站',
    message: `将知识库《${kb.name}》整库移入回收站，包含 ${count} 篇文档。`,
    details: [
      '文档树、历史版本和附件引用都会保留，可在回收站整库恢复。',
      '删除后不会出现在知识库列表、搜索和导出选择中。',
    ],
    confirmText: '移入回收站',
    danger: true,
  })
  if (!ok) return
  try {
    await kbs.remove(kb, treeRevision)
    tree.invalidate(kb.id)
    ui.toast({ kind: 'success', message: `知识库《${kb.name}》已移入回收站`, action: { label: '前往回收站', handler: () => void router.push('/trash') } })
  } catch (error) {
    ui.toast({ kind: 'error', message: '删除知识库失败', detail: error instanceof Error ? error.message : String(error) })
  }
}

const MENU: MenuItem[] = [
  { key: 'open', label: '打开', icon: 'book' },
  { key: 'edit', label: '修改信息', icon: 'edit' },
  { key: 'import', label: '导入到此知识库', icon: 'import' },
  { key: 'export', label: '导出知识库…', icon: 'export' },
  { key: 'delete', label: '移入回收站', icon: 'trash', danger: true, separatorBefore: true },
]

function onMenu(kb: KnowledgeBase, key: string) {
  if (key === 'open') void router.push(`/knowledge-bases/${kb.id}`)
  else if (key === 'edit') {
    editing.value = kb
    formOpen.value = true
  } else if (key === 'import') void router.push({ path: '/import', query: { kb: kb.id } })
  else if (key === 'export') { exportTarget.value = kb; exportDialogOpen.value = true }
  else if (key === 'delete') void remove(kb)
}

watch(exportDialogOpen, (open) => {
  if (!open) exportTarget.value = null
})

const draftCount = computed(() => drafts.instanceDrafts.length)
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page">
      <header class="page-header">
        <div>
          <h1>知识库</h1>
        </div>
        <span class="spacer" />
        <div class="row wrap">
          <RouterLink class="btn" to="/import"><AppIcon name="import" :size="15" />导入</RouterLink>
          <button type="button" class="btn btn-primary" :disabled="!health.canWrite" @click="openCreate">
            <AppIcon name="plus" :size="15" />新建知识库
          </button>
        </div>
      </header>

      <div v-if="draftCount > 0" class="notice notice-info home-notice">
        <AppIcon name="draft" class="notice-icon" :size="18" />
        <div class="notice-body">
          本浏览器中有 {{ draftCount }} 份尚未确认保存的本地草稿。打开对应文档时会提示恢复，也可以
          <RouterLink to="/drafts">集中查看</RouterLink>。
        </div>
      </div>

      <ErrorBlock
        v-if="kbs.error"
        :error="kbs.error"
        action="加载知识库列表"
        :safety="kbs.loaded ? '下面显示的是上次成功加载的列表，可能已过期。' : '数据没有被修改。'"
        :on-retry="load"
      />

      <div v-if="firstLoad && !kbs.loaded && !kbs.error" class="kb-grid" aria-busy="true">
        <div v-for="i in 3" :key="i" class="kb-card skeleton-card">
          <div class="skeleton" style="height: 18px; width: 50%" />
          <div class="skeleton" style="height: 12px; width: 80%; margin-top: 14px" />
        </div>
        <span class="sr-only" role="status">正在加载知识库…</span>
      </div>

      <EmptyState
        v-else-if="kbs.loaded && kbs.sorted.length === 0"
        icon="book"
        title="还没有知识库"
        description="知识库用来按主题组织文档树。创建一个知识库开始记录，或导入已有的 Markdown / ZIP 资料。"
      >
        <button type="button" class="btn btn-primary" :disabled="!health.canWrite" @click="openCreate">
          <AppIcon name="plus" :size="15" />新建知识库
        </button>
        <RouterLink class="btn" to="/import">导入 Markdown</RouterLink>
      </EmptyState>

      <ul v-else class="kb-grid list-plain" aria-label="知识库列表">
        <li v-for="kb in kbs.sorted" :key="kb.id" class="kb-card">
          <div class="kb-card-head">
            <RouterLink :to="`/knowledge-bases/${kb.id}`" class="kb-link">
              <span class="kb-icon" aria-hidden="true"><AppIcon name="book" :size="18" /></span>
              <span class="kb-name">{{ kb.name }}</span>
            </RouterLink>
            <DropdownMenu :items="MENU" :label="`知识库《${kb.name}》的操作`" small @select="onMenu(kb, $event)" />
          </div>
          <p class="kb-desc" :class="{ muted: !kb.description }">{{ kb.description || '暂无描述' }}</p>
          <p class="kb-meta">
            <span>{{ kb.document_count }} 篇文档</span>
            <span aria-hidden="true">·</span>
            <span>更新于 {{ formatDateTime(kb.updated_at) }}</span>
          </p>
        </li>
      </ul>
    </main>

    <KbFormDialog v-model:open="formOpen" :knowledge-base="editing" @saved="onSaved" />
    <ExportDialog
      v-if="exportTarget"
      v-model:open="exportDialogOpen"
      scope="knowledge_base"
      :target-id="exportTarget?.id"
      :name="exportTarget?.name"
    />
  </div>
</template>

<style scoped>
.spacer {
  flex: 1;
}

.home-notice {
  margin-bottom: 16px;
}

.kb-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 14px;
}

.kb-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px 16px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  transition: border-color 0.15s, box-shadow 0.15s;
}

.kb-card:hover {
  border-color: var(--border-strong);
  box-shadow: var(--shadow-sm);
}

.kb-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.kb-link {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  color: var(--text);
  font-weight: 600;
  font-size: 15px;
}

.kb-icon {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: var(--primary-soft);
  color: var(--primary-text);
  flex: none;
}

.kb-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-desc {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 2.9em;
  color: var(--text-2);
  font-size: 13px;
  white-space: pre-wrap;
}

.kb-meta {
  display: flex;
  gap: 6px;
  font-size: 12px;
  color: var(--text-3);
}

.skeleton-card {
  min-height: 110px;
}
</style>
