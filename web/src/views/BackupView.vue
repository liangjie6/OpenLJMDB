<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import EmptyState from '@/components/base/EmptyState.vue'
import { api, type JobOrResult } from '@/api'
import { isApiError } from '@/api/http'
import { asObject, normalizeBackup, num, str } from '@/api/normalize'
import type { BackupInfo, BackupValidation, DatasetSummary, Job } from '@/api/types'
import { choose } from '@/composables/useDialogs'
import { waitForJob } from '@/services/jobs'
import { tabBus } from '@/services/broadcast'
import { useHealthStore } from '@/stores/health'
import { useDraftsStore } from '@/stores/drafts'
import { formatBytes, formatFullTime } from '@/utils/format'
import { triggerDownload } from '@/utils/download'
import { uuid } from '@/utils/uuid'

/**
 * 完整备份与恢复（与内容导出分开）：
 * 备份包含知识库、文档、附件、历史、回收站及可迁移设置；不包含浏览器本地草稿。
 * 恢复先预检，确认后替换当前数据；恢复前自动建立回退备份，失败时保持原数据可用。
 */
const health = useHealthStore()
const drafts = useDraftsStore()

const backups = ref<BackupInfo[]>([])
const listLoading = ref(false)
const listError = ref<unknown>(null)

const backupJob = ref<Job | null>(null)
const backupRunning = ref(false)
const backupError = ref<unknown>(null)
const backupResult = ref<BackupInfo | null>(null)

const validating = ref(false)
const validateProgress = ref(0)
const validateError = ref<unknown>(null)
const validation = ref<BackupValidation | null>(null)
const validationSource = ref('')
const acknowledged = ref(false)

const restoreJob = ref<Job | null>(null)
const restoreRunning = ref(false)
const restoreError = ref<unknown>(null)
const restoreDone = ref(false)
const restoreTransient = ref('')

let abort: AbortController | null = null
onBeforeUnmount(() => abort?.abort())

const busy = computed(() => backupRunning.value || validating.value || restoreRunning.value)

async function loadList() {
  listLoading.value = true
  listError.value = null
  try {
    const page = await api.backups.list(1, 50)
    backups.value = [...page.items].sort((a, b) => b.created_at - a.created_at)
  } catch (e) {
    listError.value = e
  } finally {
    listLoading.value = false
  }
}

onMounted(() => {
  void loadList()
  void drafts.refresh()
})

const PHASE: Record<string, string> = {
  queued: '排队中',
  waiting: '等待进行中的写入完成',
  maintenance: '进入维护状态',
  snapshot: '生成数据库一致快照',
  database: '生成数据库一致快照',
  attachments: '归档附件',
  checksum: '计算校验和',
  verify: '校验',
  validating: '校验备份',
  rollback_backup: '创建当前数据的回退备份',
  pre_restore_backup: '创建当前数据的回退备份',
  switching: '切换数据',
  swap: '切换数据',
  reopen: '重新打开数据库',
  reopening: '重新打开数据库',
  finalizing: '收尾',
}

function phaseText(job: Job | null): string {
  if (!job) return '准备中'
  return job.phase ? (PHASE[job.phase] ?? job.phase) : job.state === 'queued' ? '排队中' : '处理中'
}

async function createBackup() {
  const draftCount = drafts.instanceDrafts.length
  if (draftCount > 0) {
    const key = await choose({
      title: '存在未保存到服务的编辑',
      message: `本浏览器中有 ${draftCount} 份本地草稿尚未确认保存。浏览器草稿不属于完整备份，备份只包含服务端已保存的内容。`,
      cancelKey: 'cancel',
      tone: 'warning',
      actions: [
        { key: 'cancel', label: '先去处理草稿', autofocus: true },
        { key: 'continue', label: '仍然创建备份', kind: 'primary' },
      ],
    })
    if (key !== 'continue') return
  }
  backupRunning.value = true
  backupError.value = null
  backupResult.value = null
  backupJob.value = null
  abort = new AbortController()
  const key = uuid()
  try {
    let response: JobOrResult
    try {
      response = await api.backups.create(key)
    } catch (e) {
      if (isApiError(e) && (e.kind === 'network' || e.kind === 'timeout')) response = await api.backups.create(key)
      else throw e
    }
    let resultObj: Record<string, unknown> | null = response.result
    if (response.job) {
      const final = await waitForJob(response.job, { signal: abort.signal, onUpdate: (j) => (backupJob.value = j) })
      backupJob.value = final
      if (final.state !== 'succeeded') throw new Error(final.error_summary || '备份失败，临时文件不会被当作成功备份')
      resultObj = final.result
    }
    const r = asObject(resultObj)
    const info = normalizeBackup(Object.keys(asObject(r.backup)).length ? r.backup : r)
    backupResult.value = { ...info, download_url: info.download_url ?? backupJob.value?.download_url ?? (backupJob.value ? api.jobs.downloadUrl(backupJob.value.id) : null) }
    void loadList()
  } catch (e) {
    if (!(isApiError(e) && e.kind === 'aborted')) backupError.value = e
  } finally {
    backupRunning.value = false
  }
}

function downloadBackup(info: BackupInfo) {
  const url = info.download_url ?? `/api/v1/backups/${encodeURIComponent(info.id)}/download`
  triggerDownload(url, info.file_name ?? undefined)
}

async function finishValidation(response: JobOrResult) {
  let resultObj: Record<string, unknown> | null = response.result
  if (response.job) {
    const final = await waitForJob(response.job, { signal: abort?.signal, onUpdate: (j) => (validateProgress.value = j.progress) })
    if (final.state !== 'succeeded' && !final.result) throw new Error(final.error_summary || '备份校验失败')
    resultObj = final.result
  }
  validation.value = api.backups.parseValidation(resultObj)
  acknowledged.value = false
}

async function validateFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  ;(event.target as HTMLInputElement).value = ''
  if (!file) return
  validationSource.value = `上传的文件：${file.name}（${formatBytes(file.size)}）`
  await runValidation(() => api.backups.validateUpload(file, { onProgress: (l, t) => (validateProgress.value = t ? Math.round((l / t) * 100) : 0) }))
}

async function validateExisting(info: BackupInfo) {
  validationSource.value = `本机备份：${info.file_name ?? formatFullTime(info.created_at)}`
  await runValidation(() => api.backups.validateExisting(info.id))
}

async function runValidation(start: () => Promise<JobOrResult>) {
  validating.value = true
  validateError.value = null
  validation.value = null
  restoreError.value = null
  restoreDone.value = false
  validateProgress.value = 0
  abort = new AbortController()
  try {
    await finishValidation(await start())
  } catch (e) {
    if (!(isApiError(e) && e.kind === 'aborted')) validateError.value = e
  } finally {
    validating.value = false
  }
}

const compareRows = computed(() => {
  const v = validation.value
  if (!v) return []
  const cur: DatasetSummary | null = v.current
  const rows: { label: string; current: string; backup: string }[] = []
  const fmt = (n: number | null | undefined) => (n === null || n === undefined ? '—' : n.toLocaleString('zh-CN'))
  rows.push({ label: '数据时间', current: formatFullTime(cur?.created_at ?? null), backup: formatFullTime(v.backup.created_at) })
  rows.push({ label: '知识库', current: fmt(cur?.knowledge_base_count), backup: fmt(v.backup.knowledge_base_count) })
  rows.push({ label: '文档', current: fmt(cur?.document_count), backup: fmt(v.backup.document_count) })
  rows.push({ label: '附件', current: fmt(cur?.attachment_count), backup: fmt(v.backup.attachment_count) })
  rows.push({ label: '历史版本', current: fmt(cur?.history_count), backup: fmt(v.backup.history_count) })
  rows.push({ label: '回收站批次', current: fmt(cur?.trash_batch_count), backup: fmt(v.backup.trash_batch_count) })
  rows.push({ label: '数据库结构版本', current: fmt(cur?.schema_version ?? health.health?.schema_version), backup: fmt(v.backup.schema_version) })
  rows.push({ label: '应用版本', current: cur?.app_version ?? health.health?.version ?? '—', backup: v.backup.app_version ?? '—' })
  return rows
})

const sameInstance = computed(() => !!validation.value?.backup.instance_id && validation.value.backup.instance_id === health.instanceId)

async function startRestore() {
  const v = validation.value
  if (!v || !v.valid || !acknowledged.value) return
  restoreRunning.value = true
  restoreError.value = null
  restoreJob.value = null
  restoreTransient.value = ''
  abort = new AbortController()
  const key = uuid()
  try {
    let response: JobOrResult
    try {
      response = await api.backups.restore(v.validated_backup_id, v.confirmation_token, key)
    } catch (e) {
      if (isApiError(e) && (e.kind === 'network' || e.kind === 'timeout')) {
        response = await api.backups.restore(v.validated_backup_id, v.confirmation_token, key)
      } else throw e
    }
    if (response.job) {
      // HTTP 202 只代表已受理；数据库切换期间查询可能暂时失败，继续轮询
      const final = await waitForJob(response.job, {
        signal: abort.signal,
        intervalMs: 1500,
        onUpdate: (j) => {
          restoreJob.value = j
          restoreTransient.value = ''
        },
        onTransientError: (error) => {
          restoreTransient.value = `正在切换数据，暂时无法查询状态（${error.message}），继续等待…`
        },
      })
      restoreJob.value = final
      if (final.state !== 'succeeded') {
        const r = asObject(final.result)
        const rolled = r.rolled_back === true || str(r, 'rollback') === 'succeeded'
        throw new Error(`${final.error_summary || '恢复失败'}${rolled ? '。已回退，当前数据保持可用。' : ''}`)
      }
    }
    restoreDone.value = true
    health.notifyRestoreCompleted()
    tabBus.post({ type: 'drafts-changed', instance_id: health.instanceId, session_id: 'restore' })
  } catch (e) {
    if (!(isApiError(e) && e.kind === 'aborted')) restoreError.value = e
  } finally {
    restoreRunning.value = false
    void health.refresh()
  }
}

const restoreRollback = computed(() => {
  const r = asObject(restoreJob.value?.result)
  return {
    backupPath: str(r, 'rollback_backup', 'pre_restore_backup', 'rollback_backup_path'),
    rolledBack: r.rolled_back === true,
    documents: num(r, 'document_count'),
  }
})

function reload() {
  window.location.reload()
}
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page page-narrow">
      <header class="page-header">
        <div>
          <h1>备份与恢复</h1>
        </div>
      </header>

      <!-- 创建备份 -->
      <section class="card block">
        <h2>创建完整备份</h2>
        <ul class="muted small scope">
          <li>包含：全部知识库与文档（保留原始 ID）、附件、历史版本、回收站、可迁移的设置，以及版本清单与校验和。</li>
          <li>不包含：浏览器中的本地草稿（创建前请先保存或处理）、运行日志、机器相关的启动配置（数据目录、端口）。</li>
          <li>备份时会短暂暂停写入以生成一致的数据库快照；正在编辑的内容会保留在本地草稿，结束后自动继续保存。</li>
          <li>程序运行时不要直接复制数据目录作为备份；完全退出程序后复制整个数据目录才是可靠的手工备份。</li>
        </ul>
        <div class="row actions">
          <button type="button" class="btn btn-primary" :disabled="busy || !health.canWrite" @click="createBackup">
            <AppIcon name="archive" :size="15" />创建完整备份
          </button>
        </div>
        <div v-if="backupRunning" class="running" role="status">
          <p><span class="spinner" aria-hidden="true" /> 正在创建备份：{{ phaseText(backupJob) }}</p>
          <div class="progress" :class="{ indeterminate: !backupJob?.progress }" role="progressbar" :aria-valuenow="backupJob?.progress ?? 0" aria-valuemin="0" aria-valuemax="100" aria-label="备份进度">
            <span :style="{ width: `${backupJob?.progress ?? 0}%` }" />
          </div>
        </div>
        <ErrorBlock v-if="backupError" :error="backupError" action="创建备份" safety="当前数据不受影响；失败的临时文件不会显示为可用备份。" :on-retry="createBackup" />
        <div v-if="backupResult" class="notice notice-success">
          <AppIcon name="check" class="notice-icon" :size="18" />
          <div class="notice-body">
            <div class="notice-title">备份已完成并通过校验</div>
            <dl class="dl small">
              <dt>文件</dt>
              <dd>{{ backupResult.file_name ?? backupResult.id }}</dd>
              <dt>生成时间</dt>
              <dd>{{ formatFullTime(backupResult.created_at) }}</dd>
              <dt>大小</dt>
              <dd>{{ formatBytes(backupResult.size_bytes) }}</dd>
              <template v-if="backupResult.document_count !== null">
                <dt>内容</dt>
                <dd>{{ backupResult.document_count }} 篇文档 · {{ backupResult.attachment_count ?? '—' }} 个附件</dd>
              </template>
              <template v-if="backupResult.sha256">
                <dt>SHA-256</dt>
                <dd class="mono">{{ backupResult.sha256 }}</dd>
              </template>
            </dl>
            <div class="row wrap">
              <button type="button" class="btn btn-sm" @click="downloadBackup(backupResult)"><AppIcon name="download" :size="14" />下载备份</button>
              <span class="muted small">下载由浏览器执行，请确认文件已保存到安全位置（最好是另一块磁盘）。</span>
            </div>
          </div>
        </div>
      </section>

      <!-- 备份列表 -->
      <section class="card block">
        <h2>本机备份</h2>
        <ErrorBlock v-if="listError" :error="listError" action="加载备份列表" :on-retry="loadList" />
        <p v-else-if="listLoading && backups.length === 0" class="muted">正在加载…</p>
        <EmptyState v-else-if="backups.length === 0" icon="archive" title="还没有备份" description="建议在大量整理资料前后各创建一次完整备份。" />
        <div v-else class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th scope="col">时间</th>
                <th scope="col">大小</th>
                <th scope="col">内容</th>
                <th scope="col">版本</th>
                <th scope="col"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="b in backups" :key="b.id">
                <td class="nowrap">{{ formatFullTime(b.created_at) }}</td>
                <td class="nowrap">{{ formatBytes(b.size_bytes) }}</td>
                <td class="small">{{ b.document_count ?? '—' }} 篇 · {{ b.attachment_count ?? '—' }} 附件</td>
                <td class="small">应用 {{ b.app_version ?? '—' }} · 结构 {{ b.schema_version ?? '—' }}</td>
                <td class="nowrap">
                  <button type="button" class="btn btn-sm" @click="downloadBackup(b)">下载</button>
                  <button type="button" class="btn btn-sm" :disabled="busy" @click="validateExisting(b)">校验并恢复…</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- 恢复 -->
      <section class="card block">
        <h2>从备份恢复</h2>
        <p class="muted small">
          恢复会用备份内容替换当前全部数据。会先校验格式、校验和、数据库完整性、附件与版本兼容性，并在确认前展示两份数据的差异；校验失败不会覆盖当前数据。
        </p>
        <div class="row wrap actions start">
          <label class="btn" :class="{ disabled: busy }">
            <AppIcon name="upload" :size="15" />选择备份文件并校验
            <input type="file" class="sr-only" accept=".zip,.ljmdb,.ljmdb-backup,.tar,application/zip,application/octet-stream" :disabled="busy" @change="validateFile" />
          </label>
          <span class="muted small">或在上方列表中选择本机备份。</span>
        </div>

        <div v-if="validating" class="running" role="status">
          <p><span class="spinner" aria-hidden="true" /> 正在校验备份（{{ validationSource }}）…</p>
          <div class="progress" :class="{ indeterminate: !validateProgress }"><span :style="{ width: `${validateProgress}%` }" /></div>
        </div>
        <ErrorBlock v-if="validateError" :error="validateError" action="校验备份" safety="当前数据未被修改。" />

        <template v-if="validation && !restoreDone">
          <div class="notice" :class="validation.valid ? 'notice-info' : 'notice-danger'">
            <AppIcon :name="validation.valid ? 'check' : 'alert'" class="notice-icon" :size="18" />
            <div class="notice-body">
              <div class="notice-title">{{ validation.valid ? '备份校验通过' : '备份未通过校验，不能用于恢复' }}</div>
              <div class="small">{{ validationSource }}</div>
              <ul v-if="validation.issues.length" class="small">
                <li v-for="(issue, i) in validation.issues" :key="i"><code v-if="issue.path">{{ issue.path }}</code> {{ issue.message }}</li>
              </ul>
            </div>
          </div>
          <table v-if="validation.valid" class="table compare">
            <thead>
              <tr>
                <th scope="col">项目</th>
                <th scope="col">当前数据</th>
                <th scope="col">备份内容（恢复后）</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in compareRows" :key="row.label" :class="{ differs: row.current !== row.backup }">
                <th scope="row">{{ row.label }}</th>
                <td>{{ row.current }}</td>
                <td>{{ row.backup }}</td>
              </tr>
            </tbody>
          </table>
          <template v-if="validation.valid">
            <div class="notice notice-warning">
              <AppIcon name="alert" class="notice-icon" :size="18" />
              <div class="notice-body">
                <div class="notice-title">当前数据将替换为备份内容</div>
                <ul class="small">
                  <li>恢复前会自动创建当前数据的回退备份；恢复失败时保持原数据可用。</li>
                  <li>恢复期间服务停止写入，所有页面会锁定修改操作。完成后需要重新加载页面。</li>
                  <li v-if="drafts.instanceDrafts.length">本浏览器中有 {{ drafts.instanceDrafts.length }} 份未处理的本地草稿，请先处理；恢复后它们不会自动提交，需要重新比较。</li>
                  <li>请先处理其他标签页中未保存的编辑。{{ sameInstance ? '备份来自同一数据实例，' : '' }}恢复后旧页面的写入会因数据世代变化被拒绝。</li>
                </ul>
              </div>
            </div>
            <label class="checkbox ack">
              <input v-model="acknowledged" type="checkbox" :disabled="restoreRunning" />
              <span>我已处理未保存的编辑，确认用此备份替换当前全部数据</span>
            </label>
            <div class="row actions">
              <button type="button" class="btn" :disabled="restoreRunning" @click="validation = null">取消</button>
              <button type="button" class="btn btn-danger" :disabled="!acknowledged || restoreRunning || !health.canWrite" @click="startRestore">
                开始恢复
              </button>
            </div>
          </template>
        </template>

        <div v-if="restoreRunning" class="running" role="status" aria-live="polite">
          <p><span class="spinner" aria-hidden="true" /> 正在恢复：{{ phaseText(restoreJob) }}。请勿关闭程序，此阶段不能中途取消。</p>
          <div class="progress" :class="{ indeterminate: !restoreJob?.progress }"><span :style="{ width: `${restoreJob?.progress ?? 0}%` }" /></div>
          <p v-if="restoreTransient" class="muted small">{{ restoreTransient }}</p>
        </div>
        <ErrorBlock
          v-if="restoreError"
          :error="restoreError"
          action="恢复备份"
          safety="如果恢复在切换前失败，当前数据保持不变；切换中失败会按回退记录恢复原数据。请保留诊断信息。"
        />
        <div v-if="restoreDone" class="notice notice-success">
          <AppIcon name="check" class="notice-icon" :size="18" />
          <div class="notice-body">
            <div class="notice-title">恢复完成，需要重新加载页面</div>
            <p class="small">
              服务已重新打开数据库并生成新的数据世代。旧页面的写入会被拒绝；重新加载后，浏览器本地草稿需要重新比较后再决定取舍，不会自动提交。
              <template v-if="restoreRollback.backupPath">恢复前的数据已保存为回退备份：{{ restoreRollback.backupPath }}。</template>
            </p>
            <button type="button" class="btn btn-primary" @click="reload">重新加载页面</button>
          </div>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.block {
  padding: 20px;
  margin-bottom: 18px;
}

.block h2 {
  font-size: 16px;
  margin-bottom: 10px;
}

.scope {
  margin: 0;
  padding-left: 18px;
}

.scope li + li {
  margin-top: 4px;
}

.actions {
  justify-content: flex-end;
  margin-top: 14px;
}

.actions.start {
  justify-content: flex-start;
}

.running {
  margin-top: 14px;
}

.running p {
  margin-bottom: 8px;
}

.notice {
  margin-top: 14px;
}

.notice ul {
  margin: 4px 0 0;
  padding-left: 18px;
}

.table-wrap {
  overflow-x: auto;
}

.compare {
  margin-top: 14px;
}

.compare tr.differs td {
  font-weight: 600;
}

.ack {
  margin-top: 14px;
}

label.btn.disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

label.btn:focus-within {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
</style>
