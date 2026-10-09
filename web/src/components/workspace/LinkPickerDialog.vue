<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import HighlightedText from '@/components/base/HighlightedText.vue'
import { api } from '@/api'
import { isAbortError } from '@/api/http'
import type { SearchHit } from '@/api/types'
import { findTermRanges, splitSearchTerms } from '@/utils/text'
import { markdownDocLink } from '@/utils/links'
import { useHealthStore } from '@/stores/health'

/**
 * 插入文档链接：按标题搜索目标，结果显示所属知识库和路径以区分同名文档；
 * 链接保存稳定文档 ID（/documents/{id}），展示文本可编辑。
 */
const props = defineProps<{ open: boolean; kbId: string | null; excludeId?: string | null }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void; (e: 'insert', markdown: string): void }>()

const health = useHealthStore()
const query = ref('')
const scopeCurrent = ref(true)
const results = ref<SearchHit[]>([])
const loading = ref(false)
const error = ref('')
const activeIndex = ref(-1)
const chosen = ref<SearchHit | null>(null)
const linkText = ref('')
let timer: ReturnType<typeof setTimeout> | null = null
let controller: AbortController | null = null

watch(
  () => props.open,
  (open) => {
    if (open) {
      query.value = ''
      results.value = []
      chosen.value = null
      linkText.value = ''
      error.value = ''
      activeIndex.value = -1
    }
  },
)

watch([query, scopeCurrent], () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(search, 250)
})

async function search() {
  const q = query.value.trim()
  controller?.abort()
  if (!q) {
    results.value = []
    loading.value = false
    return
  }
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const page = await api.search(
      { q, knowledgeBaseId: scopeCurrent.value ? props.kbId : null, page: 1, pageSize: 20 },
      controller.signal,
    )
    results.value = page.items.filter((hit) => hit.document_id !== props.excludeId)
    activeIndex.value = results.value.length ? 0 : -1
  } catch (e) {
    if (isAbortError(e)) return
    error.value = e instanceof Error ? e.message : String(e)
    results.value = []
  } finally {
    loading.value = false
  }
}

const terms = computed(() => splitSearchTerms(query.value))

function titleRanges(hit: SearchHit) {
  return hit.title_highlights.length ? hit.title_highlights : findTermRanges(hit.title, terms.value)
}

function choose(hit: SearchHit) {
  chosen.value = hit
  linkText.value = hit.title
  void nextTick(() => document.getElementById('link-text-input')?.focus())
}

function onKey(event: KeyboardEvent) {
  if (!results.value.length) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    activeIndex.value = (activeIndex.value + 1) % results.value.length
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    activeIndex.value = (activeIndex.value - 1 + results.value.length) % results.value.length
  } else if (event.key === 'Enter') {
    event.preventDefault()
    const hit = results.value[activeIndex.value]
    if (hit) choose(hit)
  }
}

function insert() {
  if (!chosen.value) return
  emit('insert', markdownDocLink(linkText.value.trim() || chosen.value.title, chosen.value.document_id))
  emit('update:open', false)
}
</script>

<template>
  <BaseDialog :open="open" title="插入文档链接" size="lg" initial-focus="#link-search-input" @update:open="emit('update:open', $event)">
    <template v-if="!chosen">
      <div class="field">
        <label for="link-search-input">搜索目标文档</label>
        <input
          id="link-search-input"
          v-model="query"
          class="input"
          type="search"
          placeholder="输入标题或正文关键词"
          autocomplete="off"
          role="combobox"
          aria-autocomplete="list"
          aria-controls="link-results"
          :aria-expanded="results.length > 0"
          :aria-activedescendant="activeIndex >= 0 ? `link-hit-${activeIndex}` : undefined"
          :maxlength="health.limits.search_query_max_chars * 2"
          @keydown="onKey"
        />
      </div>
      <label v-if="kbId" class="checkbox scope">
        <input v-model="scopeCurrent" type="checkbox" />
        <span>仅搜索当前知识库</span>
      </label>
      <p v-if="error" class="field-error" role="alert">搜索失败：{{ error }}</p>
      <p v-else-if="loading" class="muted" role="status">正在搜索…</p>
      <p v-else-if="query.trim() && results.length === 0" class="muted" role="status">没有找到匹配的文档。</p>
      <ul id="link-results" class="list-plain link-results" role="listbox" aria-label="搜索结果">
        <li
          v-for="(hit, index) in results"
          :id="`link-hit-${index}`"
          :key="hit.document_id"
          role="option"
          class="link-hit"
          :class="{ active: index === activeIndex }"
          :aria-selected="index === activeIndex"
          @click="choose(hit)"
          @mouseenter="activeIndex = index"
        >
          <div class="hit-title"><HighlightedText :text="hit.title" :ranges="titleRanges(hit)" /></div>
          <div class="hit-meta muted small">
            <span class="badge">{{ hit.knowledge_base_name || '知识库' }}</span>
            <span v-if="hit.path.length" class="ellipsis">{{ hit.path.join(' / ') }}</span>
          </div>
        </li>
      </ul>
    </template>
    <template v-else>
      <p class="chosen">
        目标：<strong>{{ chosen.title }}</strong>
        <span class="muted">（{{ chosen.knowledge_base_name }}{{ chosen.path.length ? ' / ' + chosen.path.join(' / ') : '' }}）</span>
      </p>
      <div class="field">
        <label for="link-text-input">链接文本</label>
        <input id="link-text-input" v-model="linkText" class="input" @keydown.enter.prevent="insert" />
        <span class="field-hint">链接使用稳定文档 ID，目标重命名后仍可跳转。</span>
      </div>
    </template>
    <template #footer>
      <button v-if="chosen" type="button" class="btn" @click="chosen = null">重新选择</button>
      <button type="button" class="btn" @click="emit('update:open', false)">取消</button>
      <button type="button" class="btn btn-primary" :disabled="!chosen" @click="insert">插入链接</button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.scope {
  margin-bottom: 10px;
}

.link-results {
  max-height: 46vh;
  overflow: auto;
}

.link-hit {
  padding: 8px 10px;
  border-radius: var(--radius);
  cursor: pointer;
}

.link-hit.active {
  background: var(--bg-hover);
  outline: 1px solid var(--border-strong);
}

.hit-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 2px;
  min-width: 0;
}

.chosen {
  margin-bottom: 12px;
}
</style>
