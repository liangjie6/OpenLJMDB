<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import DropdownMenu, { type MenuItem } from '@/components/base/DropdownMenu.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import ExportDialog from '@/components/ExportDialog.vue'
import KbFormDialog from '@/components/KbFormDialog.vue'
import DocTree from '@/components/workspace/DocTree.vue'
import DocumentPane from '@/components/workspace/DocumentPane.vue'
import { isApiError } from '@/api/http'
import type { DocumentDetail, TreeNode } from '@/api/types'
import { confirmDialog } from '@/composables/useDialogs'
import { renameDocument } from '@/services/rename'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useTreeStore } from '@/stores/tree'
import { useUiStore } from '@/stores/ui'
import { useHealthStore } from '@/stores/health'
import { formatDateTime } from '@/utils/format'
import { documentPath, markdownDocLink } from '@/utils/links'

const route = useRoute()
const router = useRouter()
const kbs = useKnowledgeBaseStore()
const tree = useTreeStore()
const ui = useUiStore()
const health = useHealthStore()

const paneRef = ref<InstanceType<typeof DocumentPane> | null>(null)
const docKbId = ref<string | null>(null)
const docMissing = ref(false)
const kbFormOpen = ref(false)
const exportTarget = ref<{ scope: 'document' | 'knowledge_base'; id: string; name: string } | null>(null)
const exportDialogOpen = computed({
  get: () => !!exportTarget.value,
  set: (val) => { if (!val) exportTarget.value = null }
})
const kbLoadError = ref<unknown>(null)

const docId = computed(() => (route.name === 'doc' ? String(route.params.docId).toLowerCase() : null))
const routeKbId = computed(() => (route.name === 'kb' ? String(route.params.kbId) : null))
const kbId = computed(() => routeKbId.value ?? docKbId.value)
const kb = computed(() => kbs.byId(kbId.value))
const kbTree = computed(() => (kbId.value ? tree.trees[kbId.value] : undefined))
const searchQuery = computed(() => (typeof route.query.q === 'string' && route.query.q.trim() ? route.query.q : null))
const initialMode = computed<'read' | 'edit'>(() => (route.query.mode === 'edit' ? 'edit' : 'read'))
const rootDocs = computed(() => (kbId.value ? tree.childrenOf(kbId.value, null) : []))
const documentViews = [
  { value: 'grid', label: '宫格视图', icon: 'grid' },
  { value: 'list', label: '列表视图', icon: 'list' },
  { value: 'compact', label: '紧凑视图', icon: 'compact' },
] as const
const kbMissing = computed(() => !!routeKbId.value && kbs.loaded && !kb.value && !kbs.loading)

async function ensureKbs() {
  if (kbs.loaded) return
  try {
    await kbs.load()
    kbLoadError.value = null
  } catch (error) {
    kbLoadError.value = error
  }
}
void ensureKbs()

watch(
  kbId,
  (id) => {
    if (id) void tree.load(id).catch(() => undefined)
  },
  { immediate: true },
)

watch(docId, () => {
  docMissing.value = false
  ui.sidebarOpen = false
})

watch(
  () => [route.name, kb.value?.name] as const,
  () => {
    if (route.name === 'kb') document.title = `${kb.value?.name ?? '知识库'} - OpenLJMDB`
  },
  { immediate: true },
)

function onDocLoaded(doc: DocumentDetail) {
  docKbId.value = doc.knowledge_base_id
  docMissing.value = false
  // 去掉一次性的 mode 参数，刷新页面时不重复进入编辑
  if (route.query.mode) {
    const query = { ...route.query }
    delete query.mode
    void router.replace({ path: route.path, query })
  }
}

async function createDoc(parentId: string | null) {
  const id = kbId.value
  if (!id) return
  try {
    const created = await tree.createDocument(id, parentId)
    await router.push({ path: documentPath(created.id), query: { mode: 'edit' } })
  } catch (error) {
    // 创建失败不在树中留下看似成功的节点
    ui.toast({
      kind: 'error',
      message: '新建文档失败',
      detail: error instanceof Error ? error.message : String(error),
      action: { label: '重试', handler: () => void createDoc(parentId) },
    })
  }
}

async function renameDoc(node: TreeNode, title: string) {
  const id = kbId.value
  if (!id) return
  if (node.id === docId.value && paneRef.value) {
    await paneRef.value.renameFromTree(node, title)
  } else {
    await renameDocument(node.id, title)
  }
  tree.setTitle(id, node.id, title)
  kbs.touch(id, {})
  ui.toast({ kind: 'success', message: `已重命名为《${title}》` })
}

async function removeDoc(node: TreeNode) {
  const id = kbId.value
  if (!id) return
  await tree.load(id, true).catch(() => undefined)
  const descendants = tree.descendantIds(id, node.id).length
  const ok = await confirmDialog({
    title: '移入回收站',
    message: descendants
      ? `将《${node.title}》及其 ${descendants} 篇子文档（共 ${descendants + 1} 篇）作为一个删除批次移入回收站。`
      : `将《${node.title}》移入回收站。`,
    details: [
      '正文、历史版本和附件引用都会保留，可在回收站按批次整体恢复。',
      '删除后这些文档不会出现在文档树、搜索和导出中；指向它们的内部链接会显示已删除。',
    ],
    confirmText: '移入回收站',
    danger: true,
  })
  if (!ok) return
  const current = docId.value
  const affectsCurrent = !!current && (current === node.id || tree.ancestors(id, current).some((a) => a.id === node.id))
  if (affectsCurrent && paneRef.value) {
    const result = await paneRef.value.prepareLeave()
    if (result === false) return
  }
  const parentId = tree.node(id, node.id)?.parent_id ?? null
  try {
    const result = await tree.deleteDocument(id, node.id)
    ui.toast({
      kind: 'success',
      message: `已移入回收站：${result.document_count || descendants + 1} 篇文档`,
      action: { label: '前往回收站', handler: () => void router.push('/trash') },
    })
    if (affectsCurrent) {
      paneRef.value?.approveLeave()
      await router.replace(parentId && tree.node(id, parentId) ? documentPath(parentId) : `/knowledge-bases/${id}`)
    }
  } catch (error) {
    if (isApiError(error) && error.code === 'TREE_REVISION_CONFLICT') {
      await tree.load(id, true).catch(() => undefined)
      ui.toast({ kind: 'warning', message: '文档树已在其他页面更新并已刷新，请重新确认删除范围' })
    } else {
      ui.toast({ kind: 'error', message: '删除失败，文档未被移动', detail: error instanceof Error ? error.message : String(error) })
    }
  }
}

function removeCurrent() {
  const id = kbId.value
  const current = docId.value
  if (!id || !current) return
  const node = tree.node(id, current)
  if (node) void removeDoc(node)
}

async function copyLink(node: TreeNode) {
  try {
    await navigator.clipboard.writeText(markdownDocLink(node.title, node.id))
    ui.toast({ kind: 'success', message: '已复制内部链接' })
  } catch {
    ui.toast({ kind: 'error', message: '复制失败' })
  }
}

const kbMenu = computed<MenuItem[]>(() =>
  kbs.sorted.map((item) => ({ key: item.id, label: item.name, icon: item.id === kbId.value ? 'check' : 'book' })),
)

function switchKb(id: string) {
  if (id !== kbId.value) void router.push(`/knowledge-bases/${id}`)
}

async function removeKb() {
  const target = kb.value
  if (!target) return
  await tree.load(target.id, true).catch(() => undefined)
  const count = Object.keys(tree.trees[target.id]?.nodes ?? {}).length
  const ok = await confirmDialog({
    title: '将知识库移入回收站',
    message: `将知识库《${target.name}》整库移入回收站，包含 ${count} 篇文档。`,
    details: [
      '文档树、历史版本和附件引用都会保留，可在回收站整库恢复。',
      '删除后不会出现在知识库列表、搜索和导出选择中。',
    ],
    confirmText: '移入回收站',
    danger: true,
  })
  if (!ok) return
  if (paneRef.value) {
    const result = await paneRef.value.prepareLeave()
    if (result === false) return
    paneRef.value.approveLeave()
  }
  try {
    await kbs.remove(target, tree.trees[target.id]?.treeRevision ?? target.tree_revision)
    tree.invalidate(target.id)
    ui.toast({ kind: 'success', message: `知识库《${target.name}》已移入回收站`, action: { label: '前往回收站', handler: () => void router.push('/trash') } })
    await router.replace('/')
  } catch (error) {
    ui.toast({ kind: 'error', message: '删除知识库失败', detail: error instanceof Error ? error.message : String(error) })
  }
}

function onKbMenu(key: string) {
  if (!kb.value) return
  if (key === 'edit') kbFormOpen.value = true
  else if (key === 'export') exportTarget.value = { scope: 'knowledge_base', id: kb.value.id, name: kb.value.name }
  else if (key === 'import') void router.push({ path: '/import', query: { kb: kb.value.id } })
  else if (key === 'delete') void removeKb()
}

const kbActions: MenuItem[] = [
  { key: 'edit', label: '修改知识库信息', icon: 'edit' },
  { key: 'import', label: '导入到此知识库', icon: 'import' },
  { key: 'export', label: '导出知识库…', icon: 'export' },
  { key: 'delete', label: '移入回收站', icon: 'trash', danger: true, separatorBefore: true },
]
</script>

<template>
  <div class="workspace">
    <AppTopBar :kb-id="kbId" show-sidebar-toggle />
    <div class="workspace-body">
      <aside v-show="ui.sidebarVisible" id="workspace-sidebar" class="sidebar" :class="{ open: ui.sidebarOpen }" aria-label="知识库与文档树">
        <div class="sidebar-head">
          <DropdownMenu
            v-if="kbs.sorted.length"
            :items="kbMenu"
            label="切换知识库"
            :text="kb?.name ?? '选择知识库'"
            icon="book"
            align="left"
            button-class="btn btn-ghost kb-switcher"
            @select="switchKb"
          />
          <span v-else class="kb-name-static">{{ kb?.name ?? '知识库' }}</span>
          <DropdownMenu v-if="kb" :items="kbActions" label="知识库操作" small @select="onKbMenu" />
        </div>
        <div class="sidebar-actions">
          <button type="button" class="btn btn-sm btn-primary new-doc" :disabled="!kbId || !health.canWrite" @click="createDoc(null)">
            <AppIcon name="plus" :size="14" />新建文档
          </button>
        </div>
        <DocTree
          v-if="kbId"
          :kb-id="kbId"
          :current-doc-id="docId"
          :on-create="createDoc"
          :on-rename="renameDoc"
          :on-remove="removeDoc"
          :on-export="(node) => (exportTarget = { scope: 'document', id: node.id, name: node.title })"
          :on-copy-link="copyLink"
        />
        <div v-else-if="!docMissing" class="sidebar-placeholder muted small">正在确定所属知识库…</div>
        <nav class="sidebar-foot" aria-label="知识库工具">
          <RouterLink :to="kbId ? { path: '/trash', query: { kb: kbId } } : '/trash'" class="foot-link">
            <AppIcon name="trash" :size="15" />回收站
          </RouterLink>
          <RouterLink :to="kbId ? { path: '/import', query: { kb: kbId } } : '/import'" class="foot-link">
            <AppIcon name="import" :size="15" />导入
          </RouterLink>
          <RouterLink to="/" class="foot-link"><AppIcon name="home" :size="15" />全部知识库</RouterLink>
        </nav>
      </aside>
      <div v-if="ui.sidebarIsDrawer && ui.sidebarOpen" class="sidebar-backdrop" @click="ui.sidebarOpen = false" />

      <main id="main" class="workspace-main" tabindex="-1">
        <DocumentPane
          v-if="docId"
          :key="docId"
          ref="paneRef"
          :doc-id="docId"
          :search-query="searchQuery"
          :initial-mode="initialMode"
          @loaded="onDocLoaded"
          @missing="docMissing = true"
          @create="createDoc"
          @delete="removeCurrent"
        />
        <div v-else class="kb-overview">
          <ErrorBlock v-if="kbLoadError && !kb" :error="kbLoadError" action="加载知识库" :on-retry="ensureKbs" />
          <EmptyState
            v-else-if="kbMissing"
            icon="book"
            title="知识库不存在或已移入回收站"
            description="可以在回收站中恢复已删除的知识库。"
          >
            <RouterLink class="btn" to="/trash">前往回收站</RouterLink>
            <RouterLink class="btn btn-primary" to="/">返回全部知识库</RouterLink>
          </EmptyState>
          <template v-else-if="kb">
            <header class="overview-head">
              <div>
                <h1>{{ kb.name }}</h1>
                <p v-if="kb.description" class="overview-desc">{{ kb.description }}</p>
                <p class="muted small">{{ kb.document_count }} 篇文档 · 更新于 {{ formatDateTime(kb.updated_at) }}</p>
              </div>
            </header>
            <EmptyState
              v-if="kbTree?.loaded && rootDocs.length === 0"
              icon="file-plus"
              title="这个知识库还没有文档"
              description="新建第一篇文档开始记录，或导入已有的 Markdown 文件 / ZIP 资料包。"
            >
              <button type="button" class="btn btn-primary" :disabled="!health.canWrite" @click="createDoc(null)">
                <AppIcon name="plus" :size="14" />新建第一篇文档
              </button>
              <RouterLink class="btn" :to="{ path: '/import', query: { kb: kb.id } }">导入 Markdown</RouterLink>
            </EmptyState>
            <section v-else-if="kbTree?.loaded" class="overview-docs">
              <div class="overview-docs-head">
                <h2 id="root-docs-heading">顶层文档</h2>
                <div class="btn-group document-view-switch" role="group" aria-label="文档视图">
                  <button
                    v-for="view in documentViews"
                    :key="view.value"
                    type="button"
                    class="btn btn-sm"
                    :title="view.label"
                    :aria-label="view.label"
                    :aria-pressed="ui.documentView === view.value"
                    aria-controls="root-docs"
                    @click="ui.documentView = view.value"
                  >
                    <AppIcon :name="view.icon" :size="16" />
                  </button>
                </div>
              </div>
              <ul id="root-docs" class="list-plain doc-cards" :class="`doc-cards--${ui.documentView}`" aria-labelledby="root-docs-heading">
                <li v-for="node in rootDocs" :key="node.id">
                  <RouterLink :to="documentPath(node.id)" class="doc-card">
                    <AppIcon name="file-text" :size="18" />
                    <span class="ellipsis doc-card-title" :title="node.title">{{ node.title }}</span>
                    <span v-if="node.updated_at" class="muted small nowrap doc-card-updated">{{ formatDateTime(node.updated_at) }}</span>
                  </RouterLink>
                </li>
              </ul>
              <p class="muted small">从左侧文档树选择文档，或使用“新建文档”。</p>
            </section>
          </template>
          <p v-else class="muted" role="status">正在加载知识库…</p>
        </div>
      </main>
    </div>

    <KbFormDialog v-model:open="kbFormOpen" :knowledge-base="kb ?? null" />
    <ExportDialog
      v-if="exportTarget"
      v-model:open="exportDialogOpen"
      :scope="exportTarget?.scope"
      :target-id="exportTarget?.id"
      :name="exportTarget?.name"
    />
  </div>
</template>

<style scoped>
.workspace {
  display: flex;
  flex-direction: column;
  height: 100vh;
}

.workspace-body {
  flex: 1;
  display: flex;
  min-height: 0;
  position: relative;
}

.sidebar {
  width: var(--sidebar-width);
  flex: none;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--bg-sidebar);
  border-right: 1px solid var(--border);
}

.sidebar-head {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 10px 8px 4px 8px;
}

.sidebar-head :deep(.dropdown:first-child) {
  flex: 1;
  min-width: 0;
}

.sidebar-head :deep(.kb-switcher) {
  width: 100%;
  justify-content: flex-start;
  font-weight: 600;
  overflow: hidden;
}

.sidebar-head :deep(.kb-switcher span) {
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-name-static {
  flex: 1;
  padding: 0 8px;
  font-weight: 600;
}

.sidebar-actions {
  padding: 4px 12px 8px;
}

.new-doc {
  width: 100%;
}

.sidebar-placeholder {
  padding: 12px 16px;
  flex: 1;
}

.sidebar-foot {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 10px;
  padding: 8px 14px 12px;
  border-top: 1px solid var(--border);
}

.foot-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 13px;
  color: var(--text-2);
}

.workspace-main {
  flex: 1;
  min-width: 0;
  display: flex;
  outline: none;
}

.kb-overview {
  flex: 1;
  overflow: auto;
  padding: 32px 40px;
  max-width: 920px;
  margin: 0 auto;
}

.overview-head h1 {
  font-size: 26px;
}

.overview-desc {
  margin: 6px 0 4px;
  color: var(--text-2);
  white-space: pre-wrap;
}

.overview-docs {
  margin-top: 28px;
}

.overview-docs-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.overview-docs-head h2 {
  font-size: 15px;
}

.document-view-switch {
  flex: none;
}

.document-view-switch > .btn {
  width: 34px;
  height: 30px;
  padding: 0;
  justify-content: center;
}

.doc-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 240px), 1fr));
  gap: 8px;
  margin-bottom: 12px;
}

.doc-card {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--text);
  background: var(--bg-elevated);
}

.doc-card:hover {
  border-color: var(--primary);
  text-decoration: none;
}

.doc-card .ellipsis {
  flex: 1;
  min-width: 0;
}

.doc-cards--list,
.doc-cards--compact {
  grid-template-columns: minmax(0, 1fr);
  gap: 0;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.doc-cards--list > li + li,
.doc-cards--compact > li + li {
  border-top: 1px solid var(--border);
}

.doc-cards--list .doc-card,
.doc-cards--compact .doc-card {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  border: 0;
  border-radius: 0;
}

.doc-cards--list .doc-card {
  padding: 14px 16px;
}

.doc-cards--list .doc-card-title {
  white-space: normal;
  overflow-wrap: anywhere;
}

.doc-cards--compact .doc-card {
  gap: 8px;
  padding: 7px 12px;
  font-size: 13px;
}

.doc-cards--list .doc-card:hover,
.doc-cards--compact .doc-card:hover {
  background: var(--bg-hover);
}

.doc-card:focus-visible {
  outline-offset: -2px;
}

.sidebar-backdrop {
  display: none;
}

@media (max-width: 900px) {
  .sidebar {
    position: fixed;
    top: var(--topbar-height);
    left: 0;
    bottom: 0;
    z-index: 70;
    width: min(320px, 88vw);
    transform: translateX(-100%);
    transition: transform 0.2s;
    box-shadow: var(--shadow-lg);
    visibility: hidden;
  }
  .sidebar.open {
    transform: none;
    visibility: visible;
  }
  .sidebar-backdrop {
    display: block;
    position: fixed;
    inset: var(--topbar-height) 0 0 0;
    z-index: 69;
    background: var(--bg-overlay);
  }
  .kb-overview {
    padding: 20px 16px;
  }
}

@media (max-width: 480px) {
  .doc-cards--list .doc-card {
    grid-template-columns: 18px minmax(0, 1fr);
    gap: 4px 10px;
    padding: 12px;
  }

  .doc-cards--list .doc-card-updated {
    grid-column: 2;
  }
}
</style>
