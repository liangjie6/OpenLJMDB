<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import DiffView from '@/components/base/DiffView.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import type { DocumentDetail } from '@/api/types'
import { formatDateTime, formatFullTime } from '@/utils/format'
import { validateTitle } from '@/utils/text'
import { useHealthStore } from '@/stores/health'

/**
 * 比较与合并：用于 409 修订冲突和“草稿基础修订不匹配”。
 * 两份内容都保留；提供使用远端、另存本地为新文档、导出本地和提交人工合并结果。
 * 禁止自动用最新 revision 覆盖，提交合并结果时以当前远端修订为基线重新校验。
 */
const props = defineProps<{
  open: boolean
  mode: 'conflict' | 'draft'
  local: { title: string; markdown: string; label: string; time: number | null }
  server: DocumentDetail | null
  busy?: boolean
  serverError?: string | null
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'use-server'): void
  (e: 'save-as-new'): void
  (e: 'export-local'): void
  (e: 'submit', merged: { title: string; markdown: string }): void
  (e: 'refetch'): void
}>()

const health = useHealthStore()
const tab = ref<'diff' | 'merge'>('diff')
const mergedTitle = ref('')
const mergedMarkdown = ref('')
const titleError = ref('')

watch(
  () => props.open,
  (open) => {
    if (open) {
      tab.value = 'diff'
      mergedTitle.value = props.local.title
      mergedMarkdown.value = props.local.markdown
      titleError.value = ''
    }
  },
  { immediate: true },
)

const title = computed(() => (props.mode === 'conflict' ? '版本冲突：比较并处理' : '本地草稿与服务端版本不一致'))
const titleDiffers = computed(() => props.server !== null && props.server.title !== props.local.title)

function fill(source: 'local' | 'server') {
  if (source === 'local') {
    mergedTitle.value = props.local.title
    mergedMarkdown.value = props.local.markdown
  } else if (props.server) {
    mergedTitle.value = props.server.title
    mergedMarkdown.value = props.server.markdown
  }
}

function submit() {
  const invalid = validateTitle(mergedTitle.value, health.limits.title_max_chars)
  if (invalid) {
    titleError.value = invalid
    tab.value = 'merge'
    return
  }
  emit('submit', { title: mergedTitle.value.trim(), markdown: mergedMarkdown.value })
}
</script>

<template>
  <BaseDialog :open="open" :title="title" size="xl" @update:open="emit('update:open', $event)">
    <div class="merge">
      <div class="notice notice-warning">
        <AppIcon name="alert" class="notice-icon" :size="18" />
        <div class="notice-body">
          <template v-if="mode === 'conflict'">
            另一页面（或另一次操作）已把此文档保存为修订
            <strong>r{{ server?.revision ?? '?' }}</strong>
            <template v-if="server">（{{ formatDateTime(server.updated_at) }}）</template>
            ，你的修改是基于较早的版本做出的。为避免静默覆盖，自动保存已暂停。两份内容都已保留，请选择如何处理。
          </template>
          <template v-else>
            本地草稿保存于 {{ formatFullTime(local.time) }}，它基于的修订与服务端当前修订
            <strong>r{{ server?.revision ?? '?' }}</strong> 不一致。请比较后选择保留哪份内容，或手动合并。
          </template>
        </div>
      </div>

      <div v-if="!server" class="notice notice-danger">
        <AppIcon name="alert" class="notice-icon" :size="18" />
        <div class="notice-body">
          无法获取服务端当前内容{{ serverError ? `：${serverError}` : '' }}。本地内容仍完整保留。
          <button type="button" class="btn btn-sm" @click="emit('refetch')">重新获取</button>
        </div>
      </div>

      <div class="btn-group merge-tabs" role="group" aria-label="视图">
        <button type="button" class="btn btn-sm" :aria-pressed="tab === 'diff'" @click="tab = 'diff'">比较差异</button>
        <button type="button" class="btn btn-sm" :aria-pressed="tab === 'merge'" @click="tab = 'merge'">手动合并</button>
      </div>

      <section v-if="tab === 'diff'" aria-label="差异">
        <dl v-if="server" class="dl title-compare">
          <dt>服务端标题</dt>
          <dd>{{ server.title }}</dd>
          <dt>{{ local.label }}标题</dt>
          <dd>
            {{ local.title }}
            <span v-if="titleDiffers" class="badge badge-warning">标题不同</span>
          </dd>
        </dl>
        <DiffView
          v-if="server"
          :old-text="server.markdown"
          :new-text="local.markdown"
          :old-label="`服务端 r${server.revision}`"
          :new-label="local.label"
        />
      </section>

      <section v-else class="merge-edit" aria-label="合并编辑">
        <p class="muted small">在下面编辑最终内容。提交时会以服务端当前修订 r{{ server?.revision ?? '?' }} 为基线保存；如果期间又有新的保存，会再次提示冲突。</p>
        <div class="row wrap">
          <button type="button" class="btn btn-sm" @click="fill('local')">填入{{ local.label }}</button>
          <button type="button" class="btn btn-sm" :disabled="!server" @click="fill('server')">填入服务端内容</button>
        </div>
        <div class="field">
          <label for="merge-title">标题</label>
          <input
            id="merge-title"
            v-model="mergedTitle"
            class="input"
            :aria-invalid="titleError ? 'true' : undefined"
            @input="titleError = ''"
          />
          <span v-if="titleError" class="field-error" role="alert">{{ titleError }}</span>
        </div>
        <div class="field">
          <label for="merge-body">正文（Markdown）</label>
          <textarea id="merge-body" v-model="mergedMarkdown" class="textarea mono merge-textarea" spellcheck="false" />
        </div>
      </section>
    </div>
    <template #footer>
      <button type="button" class="btn" @click="emit('export-local')">
        <AppIcon name="download" :size="14" />导出{{ local.label }}
      </button>
      <button type="button" class="btn" :disabled="busy || !health.canWrite" @click="emit('save-as-new')">
        另存{{ local.label }}为新文档
      </button>
      <button type="button" class="btn btn-danger-text" :disabled="busy || !server" @click="emit('use-server')">
        使用服务端版本
      </button>
      <span class="spacer" />
      <button type="button" class="btn" @click="emit('update:open', false)">稍后处理</button>
      <button type="button" class="btn btn-primary" :disabled="busy || !server || !health.canWrite" @click="submit">
        {{ tab === 'merge' ? '提交合并结果' : `保留${local.label}并保存` }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.merge {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.merge-tabs {
  align-self: flex-start;
}

.title-compare {
  margin-bottom: 10px;
}

.merge-textarea {
  min-height: 42vh;
}

.spacer {
  flex: 1;
}
</style>
