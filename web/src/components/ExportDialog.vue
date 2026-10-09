<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import { api } from '@/api'
import { isApiError } from '@/api/http'
import { asArray, asObject, num, str } from '@/api/normalize'
import type { ExportRequest, Job } from '@/api/types'
import { waitForJob } from '@/services/jobs'
import { formatBytes } from '@/utils/format'
import { triggerDownload } from '@/utils/download'
import { uuid } from '@/utils/uuid'

/**
 * 内容导出（不是完整备份）：默认带资源 ZIP；纯 Markdown 明确标出不含附件的限制。
 * 准备阶段显示进度，完成后由浏览器下载；开始下载不等于用户已妥善保管文件。
 */
const props = defineProps<{
  open: boolean
  scope: 'document' | 'knowledge_base'
  targetId: string
  name: string
  /** 当前文档有尚未保存到服务的修改 */
  unsaved?: boolean
}>()

const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

type FormatKey = 'markdown-zip' | 'html-zip' | 'markdown-plain'
const format = ref<FormatKey>('markdown-zip')
const stage = ref<'options' | 'running' | 'done' | 'failed'>('options')
const job = ref<Job | null>(null)
const result = ref<Record<string, unknown> | null>(null)
const error = ref<unknown>(null)
const unknownResult = ref(false)
let idempotencyKey = uuid()
let abort: AbortController | null = null

const OPTIONS = computed(() => [
  {
    key: 'markdown-zip' as const,
    label: 'Markdown + 资源（ZIP）',
    desc:
      props.scope === 'knowledge_base'
        ? '包含有效文档树、Markdown 正文、引用的图片与附件和清单；内部链接与资源路径改写为包内相对路径，可重新导入本产品保留标题与树结构。'
        : '包含正文、引用的图片与附件和清单，适合迁移或离开应用后阅读。',
  },
  {
    key: 'html-zip' as const,
    label: 'HTML + 资源（ZIP，可离线打开）',
    desc: '解压后打开入口页面即可在断网电脑上阅读；携带本地样式、公式与图表所需资源。',
  },
  ...(props.scope === 'document'
    ? [
        {
          key: 'markdown-plain' as const,
          label: '纯 Markdown（单个 .md 文件）',
          desc: '只包含标题和正文文本。本地图片与附件不会随文件携带，离开应用后可能无法显示。',
        },
      ]
    : []),
])

watch(
  () => props.open,
  (open) => {
    if (open) {
      stage.value = 'options'
      format.value = 'markdown-zip'
      job.value = null
      result.value = null
      error.value = null
      unknownResult.value = false
      idempotencyKey = uuid()
    } else {
      abort?.abort()
    }
  },
)

onBeforeUnmount(() => abort?.abort())

function requestBody(): ExportRequest {
  return {
    scope: props.scope,
    id: props.targetId,
    format: format.value === 'html-zip' ? 'html' : 'markdown',
    include_attachments: format.value !== 'markdown-plain',
  }
}

async function start() {
  stage.value = 'running'
  error.value = null
  abort = new AbortController()
  try {
    let response
    try {
      response = await api.exports.start(requestBody(), idempotencyKey)
    } catch (e) {
      // 结果未知：使用同一幂等键查询 / 重试一次，不重复创建任务
      if (isApiError(e) && (e.kind === 'network' || e.kind === 'timeout')) {
        unknownResult.value = true
        response = await api.exports.start(requestBody(), idempotencyKey)
      } else {
        throw e
      }
    }
    unknownResult.value = false
    if (response.job) {
      const final = await waitForJob(response.job, { signal: abort.signal, onUpdate: (j) => (job.value = j) })
      job.value = final
      if (final.state !== 'succeeded') {
        throw new Error(final.error_summary || (final.state === 'cancelled' ? '导出已取消' : '导出失败'))
      }
      result.value = final.result
    } else {
      result.value = response.result
    }
    stage.value = 'done'
  } catch (e) {
    if (isApiError(e) && e.kind === 'aborted') return
    error.value = e
    stage.value = 'failed'
  }
}

const downloadUrl = computed(() => {
  const r = asObject(result.value)
  return job.value?.download_url ?? str(r, 'download_url') ?? (job.value ? api.jobs.downloadUrl(job.value.id) : null)
})

const resultInfo = computed(() => {
  const r = asObject(result.value)
  return {
    fileName: str(r, 'file_name', 'filename'),
    size: num(r, 'size_bytes', 'size'),
    documents: num(r, 'document_count'),
    attachments: num(r, 'attachment_count'),
    unresolved: asArray(r.unresolved_links).map((v) => (typeof v === 'string' ? v : str(asObject(v), 'title', 'target', 'url', 'id') ?? '')),
    external: asArray(r.external_resources ?? r.external_images).map((v) =>
      typeof v === 'string' ? v : str(asObject(v), 'url', 'src') ?? '',
    ),
    warnings: asArray(r.warnings).map((v) => (typeof v === 'string' ? v : str(asObject(v), 'message') ?? '')),
  }
})

function download() {
  if (downloadUrl.value) triggerDownload(downloadUrl.value, resultInfo.value.fileName ?? undefined)
}

const PHASE: Record<string, string> = {
  queued: '排队中',
  collecting: '收集文档与资源',
  rendering: '渲染 HTML',
  packaging: '打包',
  writing: '写入文件',
}
</script>

<template>
  <BaseDialog
    :open="open"
    :title="scope === 'knowledge_base' ? `导出知识库：${name}` : `导出文档：${name}`"
    size="lg"
    :persistent="stage === 'running'"
    @update:open="emit('update:open', $event)"
  >
    <template v-if="stage === 'options'">
      <div class="notice notice-info export-note">
        <AppIcon name="info" class="notice-icon" :size="18" />
        <div class="notice-body">
          内容导出用于阅读和格式交换，不包含历史版本、回收站、全局设置和浏览器草稿。需要迁移完整数据请使用“备份与恢复”。
        </div>
      </div>
      <div v-if="unsaved" class="notice notice-warning export-note">
        <AppIcon name="alert" class="notice-icon" :size="18" />
        <div class="notice-body">当前文档有尚未保存到服务的修改；导出使用服务端已保存的内容。</div>
      </div>
      <fieldset class="formats">
        <legend class="field-label">导出格式</legend>
        <label v-for="option in OPTIONS" :key="option.key" class="radio-card">
          <input v-model="format" type="radio" name="export-format" :value="option.key" />
          <span>
            <strong>{{ option.label }}</strong>
            <span class="radio-desc">{{ option.desc }}</span>
          </span>
        </label>
      </fieldset>
    </template>

    <template v-else-if="stage === 'running'">
      <p role="status">
        <span class="spinner" aria-hidden="true" />
        {{ unknownResult ? '提交结果未知，正在查询导出任务…' : `正在准备导出：${job?.phase ? (PHASE[job.phase] ?? job.phase) : '处理中'}` }}
      </p>
      <div class="progress" :class="{ indeterminate: !job || job.progress === 0 }" role="progressbar" :aria-valuenow="job?.progress ?? 0" aria-valuemin="0" aria-valuemax="100" aria-label="导出进度">
        <span :style="{ width: `${job?.progress ?? 0}%` }" />
      </div>
    </template>

    <template v-else-if="stage === 'done'">
      <div class="notice notice-success">
        <AppIcon name="check" class="notice-icon" :size="18" />
        <div class="notice-body">
          <div class="notice-title">导出文件已准备好</div>
          <div>
            {{ resultInfo.fileName ?? '导出文件' }}
            <template v-if="resultInfo.size !== null"> · {{ formatBytes(resultInfo.size) }}</template>
            <template v-if="resultInfo.documents !== null"> · {{ resultInfo.documents }} 篇文档</template>
            <template v-if="resultInfo.attachments !== null"> · {{ resultInfo.attachments }} 个附件</template>
          </div>
          <div class="muted small">点击下载后由浏览器保存文件，请确认文件已保存到预期位置。</div>
        </div>
      </div>
      <div v-if="resultInfo.unresolved.length || resultInfo.external.length || resultInfo.warnings.length" class="report">
        <h3>依赖报告</h3>
        <p v-if="resultInfo.unresolved.length" class="small">
          以下 {{ resultInfo.unresolved.length }} 个内部链接指向本次导出范围之外的文档，离线时无法跳转：
        </p>
        <ul v-if="resultInfo.unresolved.length" class="small">
          <li v-for="(link, i) in resultInfo.unresolved" :key="`u${i}`">{{ link }}</li>
        </ul>
        <p v-if="resultInfo.external.length" class="small">以下外部资源未随包携带，需要联网访问：</p>
        <ul v-if="resultInfo.external.length" class="small break-all">
          <li v-for="(url, i) in resultInfo.external" :key="`e${i}`">{{ url }}</li>
        </ul>
        <ul v-if="resultInfo.warnings.length" class="small">
          <li v-for="(w, i) in resultInfo.warnings" :key="`w${i}`">{{ w }}</li>
        </ul>
      </div>
    </template>

    <template v-else>
      <ErrorBlock :error="error" action="导出" safety="原文档未被修改，导出选项已保留，可以重试。" :on-retry="start" />
    </template>

    <template #footer>
      <template v-if="stage === 'options'">
        <button type="button" class="btn" @click="emit('update:open', false)">取消</button>
        <button type="button" class="btn btn-primary" @click="start">开始导出</button>
      </template>
      <template v-else-if="stage === 'done'">
        <button type="button" class="btn" @click="emit('update:open', false)">关闭</button>
        <button type="button" class="btn btn-primary" :disabled="!downloadUrl" @click="download">
          <AppIcon name="download" :size="14" />下载
        </button>
      </template>
      <template v-else-if="stage === 'failed'">
        <button type="button" class="btn" @click="stage = 'options'">返回选项</button>
        <button type="button" class="btn" @click="emit('update:open', false)">关闭</button>
      </template>
    </template>
  </BaseDialog>
</template>

<style scoped>
.export-note {
  margin-bottom: 12px;
}

.formats {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  border: 0;
}

.formats legend {
  margin-bottom: 8px;
}

.radio-desc {
  display: block;
  margin-top: 2px;
  font-size: 12.5px;
  color: var(--text-3);
}

.report {
  margin-top: 14px;
}

.report h3 {
  font-size: 14px;
  margin-bottom: 6px;
}

.report ul {
  margin: 4px 0 10px;
  padding-left: 18px;
}
</style>
