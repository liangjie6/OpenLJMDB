<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '@/components/base/AppIcon.vue'
import DropdownMenu, { type MenuItem } from '@/components/base/DropdownMenu.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import type { TreeNode } from '@/api/types'
import { useTreeStore } from '@/stores/tree'
import { useHealthStore } from '@/stores/health'
import { useDocStatusStore } from '@/stores/docStatus'
import { readLocal, writeLocal } from '@/utils/storage'
import { validateTitle } from '@/utils/text'

/**
 * 文档树（WAI-ARIA tree）：↑↓ 移动焦点，→ 展开 / 进入子节点，← 折叠 / 回到父节点，
 * Home / End，Enter 打开，F2 重命名，Shift+F10 打开节点菜单。
 * 首版不提供拖拽、移动或重排；节点按创建位置展示。
 */
const props = defineProps<{
  kbId: string
  currentDocId: string | null
  onCreate: (parentId: string | null) => Promise<void>
  onRename: (node: TreeNode, title: string) => Promise<void>
  onRemove: (node: TreeNode) => Promise<void>
  onExport: (node: TreeNode) => void
  onCopyLink: (node: TreeNode) => void
}>()

interface Row {
  node: TreeNode
  depth: number
  hasChildren: boolean
  expanded: boolean
  setSize: number
  pos: number
  parentId: string | null
}

const router = useRouter()
const tree = useTreeStore()
const health = useHealthStore()
const docStatus = useDocStatusStore()

const storageKey = computed(() => `tree-expanded:${health.instanceId}:${props.kbId}`)
const expanded = ref<Set<string>>(new Set(readLocal<string[]>(storageKey.value, [])))
const focusedId = ref<string | null>(null)
const renamingId = ref<string | null>(null)
const renameValue = ref('')
const renameError = ref('')
const renameBusy = ref(false)
const treeEl = ref<HTMLElement | null>(null)
const menus = new Map<string, InstanceType<typeof DropdownMenu>>()

const kbTree = computed(() => tree.trees[props.kbId])
const maxDepth = computed(() => health.limits.tree_max_depth)

watch(storageKey, (key) => {
  expanded.value = new Set(readLocal<string[]>(key, []))
})

function persistExpanded() {
  // 只记忆仍存在的节点，删除后清除失效状态
  const nodes = kbTree.value?.nodes ?? {}
  writeLocal(storageKey.value, Array.from(expanded.value).filter((id) => nodes[id]))
}

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const t = kbTree.value
  if (!t) return out
  const walk = (parentId: string | null, depth: number) => {
    const kids = tree.childrenOf(props.kbId, parentId)
    kids.forEach((node, i) => {
      const hasChildren = (t.children[node.id]?.length ?? 0) > 0
      const isOpen = hasChildren && expanded.value.has(node.id)
      out.push({ node, depth, hasChildren, expanded: isOpen, setSize: kids.length, pos: i + 1, parentId })
      if (isOpen && depth < 64) walk(node.id, depth + 1)
    })
  }
  walk(null, 1)
  return out
})

const tabStopId = computed(() => {
  const ids = rows.value.map((r) => r.node.id)
  if (focusedId.value && ids.includes(focusedId.value)) return focusedId.value
  if (props.currentDocId && ids.includes(props.currentDocId)) return props.currentDocId
  return ids[0] ?? null
})

/** 打开文档时展开其祖先节点 */
watch(
  () => [props.currentDocId, kbTree.value?.loadedAt] as const,
  () => {
    if (!props.currentDocId) return
    const ancestors = tree.ancestors(props.kbId, props.currentDocId)
    let changed = false
    for (const a of ancestors) {
      if (!expanded.value.has(a.id)) {
        expanded.value.add(a.id)
        changed = true
      }
    }
    if (changed) {
      expanded.value = new Set(expanded.value)
      persistExpanded()
    }
  },
  { immediate: true },
)

function toggle(id: string, open?: boolean) {
  const next = new Set(expanded.value)
  const shouldOpen = open ?? !next.has(id)
  if (shouldOpen) next.add(id)
  else next.delete(id)
  expanded.value = next
  persistExpanded()
}

function focusRow(id: string | null | undefined) {
  if (!id) return
  focusedId.value = id
  void nextTick(() => treeEl.value?.querySelector<HTMLElement>(`[data-id="${id}"]`)?.focus())
}

function open(node: TreeNode) {
  if (renamingId.value === node.id) return
  void router.push(`/documents/${node.id}`)
}

function statusLabel(id: string): { text: string; tone: string } | null {
  const entry = docStatus.entries[id]
  if (!entry) return null
  switch (entry.state) {
    case 'saving':
      return { text: '保存中', tone: 'busy' }
    case 'retry_wait':
    case 'paused':
      return { text: '保存失败', tone: 'error' }
    case 'conflict':
      return { text: '冲突', tone: 'error' }
    case 'blocked':
      return { text: '已停止保存', tone: 'error' }
    default:
      return entry.dirty ? { text: '未保存', tone: 'pending' } : null
  }
}

function menuItems(row: Row): MenuItem[] {
  const atLimit = row.depth >= maxDepth.value
  const writable = health.canWrite
  return [
    {
      key: 'child',
      label: '新建子文档',
      icon: 'file-plus',
      disabled: atLimit || !writable,
      hint: atLimit ? `已达到最大层级 ${maxDepth.value}，可新建同级文档` : !writable ? '当前不可写入' : undefined,
    },
    { key: 'sibling', label: '新建同级文档', icon: 'plus', disabled: !writable },
    { key: 'rename', label: '重命名', icon: 'edit', disabled: !writable, hint: 'F2' },
    { key: 'copy-link', label: '复制内部链接', icon: 'link' },
    { key: 'export', label: '导出…', icon: 'export' },
    { key: 'delete', label: '删除（移入回收站）', icon: 'trash', danger: true, disabled: !writable, separatorBefore: true },
  ]
}

async function onMenu(row: Row, key: string) {
  switch (key) {
    case 'child':
      toggle(row.node.id, true)
      await props.onCreate(row.node.id)
      break
    case 'sibling':
      await props.onCreate(row.parentId)
      break
    case 'rename':
      startRename(row.node)
      break
    case 'copy-link':
      props.onCopyLink(row.node)
      break
    case 'export':
      props.onExport(row.node)
      break
    case 'delete':
      await props.onRemove(row.node)
      break
  }
}

function startRename(node: TreeNode) {
  if (!health.canWrite) return
  renamingId.value = node.id
  renameValue.value = node.title
  renameError.value = ''
  void nextTick(() => {
    const input = treeEl.value?.querySelector<HTMLInputElement>(`[data-rename="${node.id}"]`)
    input?.focus()
    input?.select()
  })
}

function cancelRename() {
  const id = renamingId.value
  renamingId.value = null
  renameError.value = ''
  focusRow(id)
}

async function commitRename(node: TreeNode) {
  if (renamingId.value !== node.id || renameBusy.value) return
  const value = renameValue.value.trim()
  if (value === node.title) {
    cancelRename()
    return
  }
  const invalid = validateTitle(value, health.limits.title_max_chars)
  if (invalid) {
    renameError.value = invalid
    return
  }
  renameBusy.value = true
  try {
    await props.onRename(node, value)
    renamingId.value = null
    renameError.value = ''
    focusRow(node.id)
  } catch (error) {
    // 失败时保留编辑值并标识尚未保存
    renameError.value = `未保存：${error instanceof Error ? error.message : String(error)}`
  } finally {
    renameBusy.value = false
  }
}

function onRowKey(event: KeyboardEvent, row: Row) {
  if (event.target !== event.currentTarget) return
  const list = rows.value
  const index = list.findIndex((r) => r.node.id === row.node.id)
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      focusRow(list[index + 1]?.node.id)
      break
    case 'ArrowUp':
      event.preventDefault()
      focusRow(list[index - 1]?.node.id)
      break
    case 'ArrowRight':
      event.preventDefault()
      if (row.hasChildren && !row.expanded) toggle(row.node.id, true)
      else if (row.expanded) focusRow(list[index + 1]?.node.id)
      break
    case 'ArrowLeft':
      event.preventDefault()
      if (row.expanded) toggle(row.node.id, false)
      else focusRow(row.parentId)
      break
    case 'Home':
      event.preventDefault()
      focusRow(list[0]?.node.id)
      break
    case 'End':
      event.preventDefault()
      focusRow(list[list.length - 1]?.node.id)
      break
    case 'Enter':
      event.preventDefault()
      open(row.node)
      break
    case 'F2':
      event.preventDefault()
      startRename(row.node)
      break
    case 'Delete':
      event.preventDefault()
      void props.onRemove(row.node)
      break
    case 'ContextMenu':
      event.preventDefault()
      void menus.get(row.node.id)?.openMenu('first')
      break
    case 'F10':
      if (event.shiftKey) {
        event.preventDefault()
        void menus.get(row.node.id)?.openMenu('first')
      }
      break
  }
}

function setMenuRef(id: string, el: unknown) {
  if (el) menus.set(id, el as InstanceType<typeof DropdownMenu>)
  else menus.delete(id)
}

function reload() {
  void tree.load(props.kbId, true).catch(() => undefined)
}

defineExpose({ startRename: (id: string) => {
  const node = tree.node(props.kbId, id)
  if (node) startRename(node)
}, focusRow })
</script>

<template>
  <div class="doc-tree-wrap">
    <ErrorBlock
      v-if="kbTree?.error"
      :error="kbTree.error"
      action="加载文档树"
      :safety="kbTree.loaded ? '下面显示的是上次成功加载的文档树，可能已过期。' : undefined"
      compact
      :on-retry="reload"
    />
    <div v-if="!kbTree || (!kbTree.loaded && kbTree.loading)" class="tree-skeleton" aria-busy="true" aria-label="正在加载文档树">
      <div v-for="i in 6" :key="i" class="skeleton" :style="{ width: `${50 + ((i * 17) % 40)}%` }" />
    </div>
    <div v-else-if="kbTree.loaded && rows.length === 0" class="tree-empty">
      <p>还没有文档</p>
      <button type="button" class="btn btn-sm btn-primary" :disabled="!health.canWrite" @click="onCreate(null)">
        <AppIcon name="plus" :size="14" />新建第一篇文档
      </button>
      <RouterLink :to="{ path: '/import', query: { kb: kbId } }" class="btn btn-sm">导入 Markdown</RouterLink>
    </div>
    <div v-else ref="treeEl" class="doc-tree" role="tree" aria-label="文档树" aria-describedby="tree-help">
      <div
        v-for="row in rows"
        :key="row.node.id"
        role="treeitem"
        class="tree-row"
        :class="{ current: row.node.id === currentDocId, renaming: renamingId === row.node.id }"
        :data-id="row.node.id"
        :aria-level="row.depth"
        :aria-setsize="row.setSize"
        :aria-posinset="row.pos"
        :aria-expanded="row.hasChildren ? row.expanded : undefined"
        :aria-selected="row.node.id === currentDocId"
        :aria-current="row.node.id === currentDocId ? 'page' : undefined"
        :tabindex="tabStopId === row.node.id ? 0 : -1"
        :style="{ paddingLeft: `${4 + (row.depth - 1) * 16}px` }"
        @click="open(row.node)"
        @keydown="onRowKey($event, row)"
        @focus="focusedId = row.node.id"
      >
        <button
          v-if="row.hasChildren"
          type="button"
          class="tree-toggle"
          tabindex="-1"
          :aria-label="row.expanded ? `折叠 ${row.node.title}` : `展开 ${row.node.title}`"
          @click.stop="toggle(row.node.id)"
        >
          <AppIcon :name="row.expanded ? 'chevron-down' : 'chevron-right'" :size="14" />
        </button>
        <span v-else class="tree-toggle-spacer" aria-hidden="true" />
        <AppIcon name="file-text" :size="15" class="tree-icon" />
        <template v-if="renamingId === row.node.id">
          <span class="rename-box" @click.stop>
            <input
              v-model="renameValue"
              :data-rename="row.node.id"
              class="input rename-input"
              :aria-label="`重命名 ${row.node.title}`"
              :aria-invalid="renameError ? 'true' : undefined"
              :disabled="renameBusy"
              @keydown.enter.prevent="commitRename(row.node)"
              @keydown.esc.prevent.stop="cancelRename"
              @blur="commitRename(row.node)"
            />
            <span v-if="renameError" class="rename-error" role="alert">{{ renameError }}</span>
          </span>
        </template>
        <span v-else class="tree-title" :title="row.node.title">{{ row.node.title || '无标题' }}</span>
        <span v-if="statusLabel(row.node.id)" class="tree-badge" :class="`badge-${statusLabel(row.node.id)!.tone}`">
          <span class="dot" aria-hidden="true" />{{ statusLabel(row.node.id)!.text }}
        </span>
        <span v-else-if="docStatus.draftCounts[row.node.id]" class="tree-badge badge-draft" title="此文档有未处理的本地草稿">草稿</span>
        <span class="tree-menu" @click.stop>
          <DropdownMenu
            :ref="(el) => setMenuRef(row.node.id, el)"
            :items="menuItems(row)"
            :label="`《${row.node.title}》的操作`"
            :tabindex="-1"
            small
            @select="onMenu(row, $event)"
          />
        </span>
      </div>
    </div>
    <p id="tree-help" class="tree-help">↑↓ 移动 · ←→ 折叠/展开 · Enter 打开 · F2 重命名 · Shift+F10 菜单</p>
  </div>
</template>

<style scoped>
.doc-tree-wrap {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1;
}

.doc-tree {
  flex: 1;
  overflow: auto;
  padding: 2px 6px 12px;
}

.tree-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 32px;
  padding-right: 4px;
  border-radius: var(--radius);
  color: var(--text);
  cursor: pointer;
  user-select: none;
}

.tree-row:hover {
  background: var(--bg-hover);
}

.tree-row.current {
  background: var(--bg-active);
  color: var(--primary-text);
  font-weight: 500;
}

.tree-row:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: -2px;
}

.tree-toggle {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-3);
  cursor: pointer;
  flex: none;
}

.tree-toggle:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.tree-toggle-spacer {
  width: 20px;
  flex: none;
}

.tree-icon {
  color: var(--text-3);
}

.tree-row.current .tree-icon {
  color: var(--primary-text);
}

.tree-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 6px;
  font-size: 11px;
  line-height: 18px;
  border-radius: 999px;
  background: var(--bg-muted);
  color: var(--text-2);
  font-weight: 400;
  flex: none;
}

.tree-badge .dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.badge-error {
  color: var(--danger);
  background: var(--danger-soft);
}

.badge-busy {
  color: var(--info);
  background: var(--info-bg);
}

.badge-pending {
  color: var(--warning);
  background: var(--warning-bg);
}

.badge-draft {
  color: var(--info);
  background: var(--info-bg);
}

.tree-menu {
  opacity: 0;
  flex: none;
}

.tree-row:hover .tree-menu,
.tree-row:focus-within .tree-menu,
.tree-row.current .tree-menu,
.tree-menu:has(.dropdown.open) {
  opacity: 1;
}

.rename-box {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 2px 0;
}

.rename-input {
  min-height: 26px;
  padding: 2px 6px;
}

.rename-error {
  font-size: 12px;
  color: var(--danger);
  white-space: normal;
}

.tree-skeleton {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px 16px;
}

.tree-skeleton .skeleton {
  height: 14px;
}

.tree-empty {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  padding: 16px;
  color: var(--text-3);
}

.tree-help {
  padding: 6px 14px;
  font-size: 11px;
  color: var(--text-3);
  border-top: 1px solid var(--border);
}

@media (hover: none) {
  .tree-menu {
    opacity: 1;
  }
}
</style>
