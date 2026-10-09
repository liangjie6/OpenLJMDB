import { defineStore } from 'pinia'
import { reactive } from 'vue'
import { api } from '@/api'
import { ApiError, isApiError } from '@/api/http'
import type { TreeData, TreeNode } from '@/api/types'
import { tabBus } from '@/services/broadcast'
import { editorSessionId } from '@/services/session'
import { uuid } from '@/utils/uuid'
import { useHealthStore } from './health'
import { useKnowledgeBaseStore } from './knowledgeBases'

export interface KbTree {
  kbId: string
  treeRevision: number
  nodes: Record<string, TreeNode>
  /** parent_id（根为 ''）→ 排好序的子节点 ID */
  children: Record<string, string[]>
  loaded: boolean
  loading: boolean
  error: ApiError | null
  loadedAt: number
}

const ROOT = ''

function compareNodes(a: TreeNode, b: TreeNode): number {
  // 稳定排序 sort_order ASC, id ASC（03 文档 4.3）
  return a.sort_order - b.sort_order || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
}

function buildTree(target: KbTree, data: TreeData) {
  const nodes: Record<string, TreeNode> = {}
  for (const node of data.nodes) nodes[node.id] = node
  const children: Record<string, TreeNode[]> = {}
  for (const node of data.nodes) {
    // 父节点缺失的节点挂到根上显示，避免不可见
    const parentKey = node.parent_id && nodes[node.parent_id] ? node.parent_id : ROOT
    ;(children[parentKey] ??= []).push(node)
  }
  const sortedChildren: Record<string, string[]> = {}
  for (const [key, list] of Object.entries(children)) sortedChildren[key] = list.sort(compareNodes).map((n) => n.id)
  target.nodes = nodes
  target.children = sortedChildren
  target.treeRevision = data.tree_revision
  target.loaded = true
  target.loadedAt = Date.now()
}

export const useTreeStore = defineStore('tree', () => {
  const trees = reactive<Record<string, KbTree>>({})
  const pending = new Map<string, Promise<KbTree>>()

  function ensure(kbId: string): KbTree {
    if (!trees[kbId]) {
      trees[kbId] = {
        kbId,
        treeRevision: 0,
        nodes: {},
        children: {},
        loaded: false,
        loading: false,
        error: null,
        loadedAt: 0,
      }
    }
    return trees[kbId]!
  }

  async function load(kbId: string, force = false): Promise<KbTree> {
    const tree = ensure(kbId)
    if (tree.loaded && !force) return tree
    const existing = pending.get(kbId)
    if (existing) return existing
    const promise = (async () => {
      tree.loading = true
      try {
        buildTree(tree, await api.knowledgeBases.tree(kbId))
        tree.error = null
      } catch (error) {
        tree.error = isApiError(error) ? error : null
        throw error
      } finally {
        tree.loading = false
        pending.delete(kbId)
      }
      return tree
    })()
    pending.set(kbId, promise)
    return promise
  }

  function childrenOf(kbId: string, parentId: string | null): TreeNode[] {
    const tree = trees[kbId]
    if (!tree) return []
    return (tree.children[parentId ?? ROOT] ?? []).map((id) => tree.nodes[id]!).filter(Boolean)
  }

  function node(kbId: string, id: string): TreeNode | undefined {
    return trees[kbId]?.nodes[id]
  }

  /** 节点深度，根为 1 */
  function depthOf(kbId: string, id: string): number {
    const tree = trees[kbId]
    if (!tree) return 1
    let depth = 0
    let current: TreeNode | undefined = tree.nodes[id]
    const seen = new Set<string>()
    while (current && !seen.has(current.id)) {
      seen.add(current.id)
      depth++
      current = current.parent_id ? tree.nodes[current.parent_id] : undefined
    }
    return Math.max(1, depth)
  }

  function ancestors(kbId: string, id: string): TreeNode[] {
    const tree = trees[kbId]
    if (!tree) return []
    const result: TreeNode[] = []
    const seen = new Set<string>([id])
    let parentId = tree.nodes[id]?.parent_id ?? null
    while (parentId && tree.nodes[parentId] && !seen.has(parentId)) {
      seen.add(parentId)
      result.unshift(tree.nodes[parentId]!)
      parentId = tree.nodes[parentId]!.parent_id
    }
    return result
  }

  function descendantIds(kbId: string, id: string): string[] {
    const tree = trees[kbId]
    if (!tree) return []
    const result: string[] = []
    const stack = [...(tree.children[id] ?? [])]
    const seen = new Set<string>()
    while (stack.length) {
      const next = stack.pop()!
      if (seen.has(next)) continue
      seen.add(next)
      result.push(next)
      stack.push(...(tree.children[next] ?? []))
    }
    return result
  }

  function pathTitles(kbId: string, id: string): string[] {
    return [...ancestors(kbId, id).map((n) => n.title), node(kbId, id)?.title ?? '']
  }

  function notifyTreeChanged(kbId: string) {
    const health = useHealthStore()
    tabBus.post({ type: 'tree-changed', instance_id: health.instanceId, kb_id: kbId, session_id: editorSessionId })
  }

  /**
   * 新建文档（追加到目标同级末尾）。树版本过期时刷新后自动重试一次：
   * 用户意图（目标父节点）不变且父节点仍有效时重试是安全的。
   */
  async function createDocument(kbId: string, parentId: string | null, title = '无标题', markdown = '') {
    const health = useHealthStore()
    const maxDepth = health.limits.tree_max_depth
    const tree = await load(kbId)
    if (parentId && !tree.nodes[parentId]) await load(kbId, true)
    if (parentId && depthOf(kbId, parentId) + 1 > maxDepth) {
      throw new ApiError({
        status: 400,
        code: 'TREE_DEPTH_LIMIT',
        message: `文档树最多 ${maxDepth} 层，当前位置已到达上限。可以新建同级文档，或选择较浅的父文档。`,
        kind: 'http',
      })
    }
    for (let attempt = 0; attempt < 2; attempt++) {
      try {
        const doc = await api.knowledgeBases.createDocument(
          kbId,
          { parent_id: parentId, title, markdown, expected_tree_revision: ensure(kbId).treeRevision },
          uuid(),
        )
        await load(kbId, true).catch(() => undefined)
        useKnowledgeBaseStore().touch(kbId, { documentDelta: 1 })
        notifyTreeChanged(kbId)
        return doc
      } catch (error) {
        if (attempt === 0 && isApiError(error) && error.code === 'TREE_REVISION_CONFLICT') {
          await load(kbId, true)
          if (parentId && !ensure(kbId).nodes[parentId]) {
            throw new ApiError({
              status: 409,
              code: 'TREE_REVISION_CONFLICT',
              message: '目标父文档已在其他页面被删除，文档树已刷新',
              kind: 'http',
            })
          }
          continue
        }
        throw error
      }
    }
    throw new Error('unreachable')
  }

  async function deleteDocument(kbId: string, docId: string) {
    const tree = ensure(kbId)
    const result = await api.documents.remove(docId, tree.treeRevision)
    await load(kbId, true).catch(() => undefined)
    useKnowledgeBaseStore().touch(kbId, { documentDelta: -Math.max(1, result.document_count) })
    notifyTreeChanged(kbId)
    return result
  }

  /** 本页保存标题后同步树节点（服务端标题变化会递增 tree_revision，这里随后静默刷新） */
  function setTitle(kbId: string, docId: string, title: string) {
    const tree = trees[kbId]
    const target = tree?.nodes[docId]
    if (target && target.title !== title) {
      target.title = title
      notifyTreeChanged(kbId)
      void load(kbId, true).catch(() => undefined)
    }
  }

  function invalidate(kbId: string) {
    const tree = trees[kbId]
    if (tree) tree.loaded = false
  }

  tabBus.subscribe((message) => {
    if (message.type === 'tree-changed' && trees[message.kb_id]?.loaded) {
      void load(message.kb_id, true).catch(() => undefined)
    }
  })

  return {
    trees,
    ensure,
    load,
    childrenOf,
    node,
    depthOf,
    ancestors,
    descendantIds,
    pathTitles,
    createDocument,
    deleteDocument,
    setTitle,
    invalidate,
  }
})
