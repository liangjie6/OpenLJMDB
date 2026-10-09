<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import HighlightedText from '@/components/base/HighlightedText.vue'
import { api } from '@/api'
import { isAbortError, isApiError } from '@/api/http'
import type { SearchPage } from '@/api/types'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useHealthStore } from '@/stores/health'
import { formatDateTime } from '@/utils/format'
import { codePointLength, findTermRanges, splitSearchTerms } from '@/utils/text'

/**
 * 搜索：全部或指定知识库，默认不含回收站与历史。查询与筛选保存在地址中，返回后继续浏览结果。
 * 新查询会取消旧请求，慢响应不会覆盖最新结果；搜索失败与“零结果”分开展示。
 */
const route = useRoute()
const router = useRouter()
const kbs = useKnowledgeBaseStore()
const health = useHealthStore()

const PAGE_SIZE = 20
const input = ref('')
const scope = ref('')
const inputError = ref('')
const result = ref<SearchPage | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)
let controller: AbortController | null = null
let seq = 0

const q = computed(() => (typeof route.query.q === 'string' ? route.query.q : ''))
const kbParam = computed(() => (typeof route.query.kb === 'string' ? route.query.kb : ''))
const page = computed(() => Math.max(1, Number(route.query.page) || 1))
const terms = computed(() => splitSearchTerms(q.value))
const totalPages = computed(() => (result.value ? Math.max(1, Math.ceil(result.value.total / PAGE_SIZE)) : 1))
const scopeName = computed(() => (kbParam.value ? (kbs.byId(kbParam.value)?.name ?? '指定知识库') : '全部知识库'))
const duplicateTitles = computed(() => {
  const counts = new Map<string, number>()
  for (const hit of result.value?.items ?? []) counts.set(hit.title, (counts.get(hit.title) ?? 0) + 1)
  return counts
})

const MODE_LABEL: Record<string, string> = {
  fts: '全文索引',
  like: '中文子串匹配',
  substring: '中文子串匹配',
  fallback: '中文子串匹配',
  mixed: '全文索引 + 子串匹配',
  hybrid: '全文索引 + 子串匹配',
}

function validate(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return '请输入搜索关键词'
  const max = health.limits.search_query_max_chars
  if (codePointLength(trimmed) > max) return `关键词最多 ${max} 个字符`
  return ''
}

async function run() {
  input.value = q.value
  scope.value = kbParam.value
  controller?.abort()
  const token = ++seq
  if (!q.value.trim()) {
    result.value = null
    error.value = null
    loading.value = false
    return
  }
  const problem = validate(q.value)
  if (problem) {
    inputError.value = problem
    result.value = null
    return
  }
  controller = new AbortController()
  loading.value = true
  error.value = null
  try {
    const data = await api.search(
      { q: q.value.trim(), knowledgeBaseId: kbParam.value || null, page: page.value, pageSize: PAGE_SIZE },
      controller.signal,
    )
    if (token === seq) result.value = data
  } catch (e) {
    if (isAbortError(e) || token !== seq) return
    error.value = e
    result.value = null
  } finally {
    if (token === seq) loading.value = false
  }
}

function submit() {
  const problem = validate(input.value)
  inputError.value = problem
  if (problem) return
  void router.push({ name: 'search', query: { q: input.value.trim(), kb: scope.value || undefined } })
}

function goPage(next: number) {
  void router.push({ name: 'search', query: { ...route.query, page: next > 1 ? String(next) : undefined } })
}

function openHit(id: string) {
  void router.push({ path: `/documents/${id}`, query: { q: q.value.trim() } })
}

watch(() => route.fullPath, () => {
  if (route.name === 'search') void run()
})

onMounted(() => {
  void kbs.load().catch(() => undefined)
  void run()
})

onBeforeUnmount(() => controller?.abort())

const unavailable = computed(() => isApiError(error.value) && (error.value.status === 503 || error.value.code === 'MAINTENANCE_MODE'))
</script>

<template>
  <div>
    <AppTopBar :kb-id="kbParam || null" />
    <main id="main" class="page page-narrow">
      <header class="page-header">
        <h1>搜索</h1>
      </header>
      <form class="search-form" role="search" @submit.prevent="submit">
        <div class="field search-field">
          <label for="search-input" class="sr-only">搜索关键词</label>
          <input
            id="search-input"
            v-model="input"
            class="input"
            type="search"
            placeholder="输入标题或正文关键词"
            :aria-invalid="inputError ? 'true' : undefined"
            :aria-describedby="inputError ? 'search-input-error' : 'search-hint'"
            autocomplete="off"
            @input="inputError = ''"
          />
          <span v-if="inputError" id="search-input-error" class="field-error" role="alert">{{ inputError }}</span>
        </div>
        <div class="field">
          <label for="search-scope" class="sr-only">搜索范围</label>
          <select id="search-scope" v-model="scope" class="select">
            <option value="">全部知识库</option>
            <option v-for="kb in kbs.sorted" :key="kb.id" :value="kb.id">{{ kb.name }}</option>
          </select>
        </div>
        <button type="submit" class="btn btn-primary"><AppIcon name="search" :size="15" />搜索</button>
      </form>
      <p id="search-hint" class="muted small hint">
        搜索标题和正文，不包含回收站与历史版本。英文与代码词使用全文索引，中文短词按子串匹配；% 和 _ 按普通文字检索。
      </p>

      <template v-if="q.trim()">
        <div class="result-head" role="status">
          <template v-if="loading">正在搜索“{{ q }}”（{{ scopeName }}）…</template>
          <template v-else-if="result">
            在 <strong>{{ scopeName }}</strong> 中找到 {{ result.total }} 个结果
            <span v-if="result.search_mode" class="badge" title="实际使用的检索策略">{{ MODE_LABEL[result.search_mode] ?? result.search_mode }}</span>
          </template>
        </div>

        <ErrorBlock
          v-if="error"
          :error="error"
          action="搜索"
          :safety="unavailable ? '搜索暂不可用（可能正在维护或重建索引），其他文档操作不受影响。' : '这不是“没有结果”：搜索请求没有成功完成。'"
          :on-retry="run"
        />

        <EmptyState
          v-else-if="result && result.total === 0 && !loading"
          icon="search"
          :title="`没有找到与“${q}”匹配的文档`"
          description="可以尝试更短的关键词、换一种写法，或把范围改为全部知识库。回收站中的文档不会出现在搜索结果里。"
        />

        <ol v-else-if="result" class="list-plain results" :class="{ stale: loading }">
          <li v-for="hit in result.items" :key="hit.document_id" class="result">
            <a :href="`/documents/${hit.document_id}?q=${encodeURIComponent(q)}`" class="result-title" @click.prevent="openHit(hit.document_id)">
              <HighlightedText :text="hit.title || '无标题'" :ranges="hit.title_highlights.length ? hit.title_highlights : findTermRanges(hit.title, terms)" />
            </a>
            <div class="result-meta">
              <span class="badge">{{ hit.knowledge_base_name || kbs.byId(hit.knowledge_base_id)?.name || '知识库' }}</span>
              <span v-if="hit.path.length && (duplicateTitles.get(hit.title) ?? 0) > 1" class="ellipsis" title="同名文档的路径">
                {{ hit.path.join(' / ') }}
              </span>
              <span v-else-if="hit.path.length > 1" class="ellipsis">{{ hit.path.slice(0, -1).join(' / ') }}</span>
              <span v-if="hit.updated_at" class="nowrap">更新于 {{ formatDateTime(hit.updated_at) }}</span>
            </div>
            <p v-if="hit.snippet" class="result-snippet">
              <HighlightedText :text="hit.snippet" :ranges="hit.highlights.length ? hit.highlights : findTermRanges(hit.snippet, terms)" />
            </p>
          </li>
        </ol>

        <nav v-if="result && totalPages > 1" class="pager" aria-label="分页">
          <button type="button" class="btn btn-sm" :disabled="page <= 1" @click="goPage(page - 1)">上一页</button>
          <span>第 {{ page }} / {{ totalPages }} 页</span>
          <button type="button" class="btn btn-sm" :disabled="page >= totalPages" @click="goPage(page + 1)">下一页</button>
        </nav>
      </template>
      <EmptyState v-else icon="search" title="输入关键词开始搜索" description="可以搜索全部知识库，也可以只搜索某一个知识库。" />
    </main>
  </div>
</template>

<style scoped>
.search-form {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  flex-wrap: wrap;
}

.search-form .field {
  margin-bottom: 0;
}

.search-field {
  flex: 1;
  min-width: 240px;
}

.hint {
  margin: 8px 0 18px;
}

.result-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  color: var(--text-2);
}

.results.stale {
  opacity: 0.6;
}

.result {
  padding: 14px 0;
  border-bottom: 1px solid var(--border);
}

.result-title {
  font-size: 16px;
  font-weight: 600;
}

.result-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-3);
  min-width: 0;
}

.result-snippet {
  margin-top: 6px;
  color: var(--text-2);
  font-size: 13.5px;
  white-space: pre-wrap;
  word-break: break-word;
}

.pager {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  margin-top: 20px;
}
</style>
