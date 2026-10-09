<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import type { KnowledgeBase } from '@/api/types'
import { useKnowledgeBaseStore } from '@/stores/knowledgeBases'
import { useHealthStore } from '@/stores/health'
import { codePointLength, validateTitle } from '@/utils/text'
import { uuid } from '@/utils/uuid'

/** 新建 / 修改知识库：名称必填（去除两端空白后校验），描述选填；提交期间禁用，失败保留输入 */
const props = defineProps<{ open: boolean; knowledgeBase?: KnowledgeBase | null }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void; (e: 'saved', kb: KnowledgeBase): void }>()

const kbs = useKnowledgeBaseStore()
const health = useHealthStore()
const name = ref('')
const description = ref('')
const nameError = ref('')
const error = ref<unknown>(null)
const busy = ref(false)
let idempotencyKey = uuid()

const editing = computed(() => !!props.knowledgeBase)
const maxChars = computed(() => health.limits.title_max_chars)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    name.value = props.knowledgeBase?.name ?? ''
    description.value = props.knowledgeBase?.description ?? ''
    nameError.value = ''
    error.value = null
    idempotencyKey = uuid()
  },
  { immediate: true },
)

async function submit() {
  const invalid = validateTitle(name.value, maxChars.value)
  if (invalid) {
    nameError.value = invalid.replace('标题', '名称')
    return
  }
  busy.value = true
  error.value = null
  try {
    const input = { name: name.value.trim(), description: description.value }
    const kb = props.knowledgeBase
      ? await kbs.update(props.knowledgeBase.id, input)
      : await kbs.create(input, idempotencyKey)
    emit('saved', kb)
    emit('update:open', false)
  } catch (e) {
    error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <BaseDialog
    :open="open"
    :title="editing ? '修改知识库信息' : '新建知识库'"
    :persistent="busy"
    initial-focus="#kb-name"
    @update:open="emit('update:open', $event)"
  >
    <form id="kb-form" @submit.prevent="submit">
      <div class="field">
        <label for="kb-name">名称</label>
        <input
          id="kb-name"
          v-model="name"
          class="input"
          :aria-invalid="nameError ? 'true' : undefined"
          aria-describedby="kb-name-hint"
          :disabled="busy"
          @input="nameError = ''"
        />
        <span v-if="nameError" class="field-error" role="alert">{{ nameError }}</span>
        <span id="kb-name-hint" class="field-hint">
          必填，最多 {{ maxChars }} 个字符（当前 {{ codePointLength(name.trim()) }}）。允许与其他知识库同名，可用描述区分。
        </span>
      </div>
      <div class="field">
        <label for="kb-desc">描述（选填）</label>
        <textarea id="kb-desc" v-model="description" class="textarea" rows="3" :disabled="busy" />
      </div>
      <ErrorBlock v-if="error" :error="error" :action="editing ? '保存知识库信息' : '创建知识库'" safety="输入内容已保留，可以修改后重试。" />
    </form>
    <template #footer>
      <button type="button" class="btn" :disabled="busy" @click="emit('update:open', false)">取消</button>
      <button type="submit" form="kb-form" class="btn btn-primary" :disabled="busy || !health.canWrite">
        <span v-if="busy" class="spinner" aria-hidden="true" />{{ editing ? '保存' : '创建' }}
      </button>
    </template>
  </BaseDialog>
</template>
