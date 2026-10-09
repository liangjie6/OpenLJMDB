<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from '@/components/base/AppIcon.vue'
import { useUploadStore, type UploadTask } from '@/stores/uploads'
import { useUiStore } from '@/stores/ui'
import { formatBytes } from '@/utils/format'

/** 当前文档的上传任务：逐项显示文件名、进度和状态；部分失败可单独重试 */
const props = defineProps<{ docId: string; ownerToken: string }>()

const uploads = useUploadStore()
const ui = useUiStore()

const tasks = computed(() => uploads.tasks.filter((t) => t.docId === props.docId))
const active = computed(() => tasks.value.filter((t) => t.state === 'queued' || t.state === 'uploading').length)

function percent(task: UploadTask) {
  return task.size > 0 ? Math.min(100, Math.round((task.loaded / task.size) * 100)) : 0
}

const STATE: Record<string, string> = {
  queued: '等待上传',
  uploading: '上传中',
  done: '已上传',
  failed: '上传失败',
  cancelled: '已取消',
}

async function copyMarkdown(task: UploadTask) {
  if (!task.markdown) return
  try {
    await navigator.clipboard.writeText(task.markdown)
    ui.toast({ kind: 'success', message: '已复制附件的 Markdown 引用' })
  } catch {
    ui.toast({ kind: 'error', message: '复制失败，请手动选择文本复制', detail: task.markdown })
  }
}

function insert(task: UploadTask) {
  if (!uploads.insertManually(task.id)) {
    ui.toast({ kind: 'warning', message: '编辑器当前不可用，无法插入；可复制 Markdown 引用后手动粘贴' })
  }
}
</script>

<template>
  <section v-if="tasks.length" class="upload-tray" aria-label="附件上传">
    <header class="tray-head">
      <strong>附件上传</strong>
      <span class="muted small">{{ active ? `${active} 个进行中` : '已完成' }}</span>
      <span class="spacer" />
      <button v-if="!active" type="button" class="btn btn-sm btn-ghost" @click="uploads.clearFinished()">清除已完成</button>
    </header>
    <ul class="list-plain tray-list">
      <li v-for="task in tasks" :key="task.id" class="tray-item" :class="`state-${task.state}`">
        <AppIcon :name="task.state === 'failed' ? 'alert' : task.state === 'done' ? 'check' : 'paperclip'" :size="15" />
        <div class="tray-body">
          <div class="row">
            <span class="ellipsis tray-name" :title="task.name">{{ task.name }}</span>
            <span class="muted small nowrap">{{ formatBytes(task.size) }}</span>
          </div>
          <div v-if="task.state === 'uploading' || task.state === 'queued'" class="progress" role="progressbar" :aria-valuenow="percent(task)" aria-valuemin="0" aria-valuemax="100" :aria-label="`${task.name} 上传进度`">
            <span :style="{ width: `${percent(task)}%` }" />
          </div>
          <div class="small" :class="{ 'field-error': task.state === 'failed' }" role="status">
            {{ STATE[task.state] }}<template v-if="task.state === 'uploading'"> {{ percent(task) }}%</template>
            <template v-if="task.error && task.state === 'failed'">：{{ task.error }}（正文未插入该附件）</template>
            <template v-if="task.state === 'done' && task.inserted"> · 已插入正文</template>
            <template v-if="task.state === 'done' && !task.inserted"> · 未插入正文（发起上传的编辑器已关闭）</template>
          </div>
        </div>
        <div class="tray-actions">
          <button v-if="task.state === 'queued' || task.state === 'uploading'" type="button" class="btn btn-sm" @click="uploads.cancel(task.id)">取消</button>
          <button v-if="task.state === 'failed' || task.state === 'cancelled'" type="button" class="btn btn-sm" @click="uploads.retry(task.id)">重试</button>
          <button v-if="task.state === 'done' && !task.inserted && task.ownerToken === ownerToken" type="button" class="btn btn-sm" @click="insert(task)">插入</button>
          <button v-if="task.state === 'done' && task.markdown" type="button" class="btn btn-sm btn-ghost" @click="copyMarkdown(task)">复制引用</button>
          <button
            v-if="task.state !== 'queued' && task.state !== 'uploading'"
            type="button"
            class="btn btn-icon btn-sm"
            :aria-label="`移除 ${task.name}`"
            @click="uploads.dismiss(task.id)"
          >
            <AppIcon name="close" :size="13" />
          </button>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.upload-tray {
  margin: 12px 0;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-soft);
}

.tray-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  border-bottom: 1px solid var(--border);
}

.tray-list {
  max-height: 220px;
  overflow: auto;
}

.tray-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 12px;
}

.tray-item + .tray-item {
  border-top: 1px solid var(--border);
}

.tray-item > .app-icon {
  margin-top: 3px;
  color: var(--text-3);
}

.state-failed > .app-icon {
  color: var(--danger);
}

.state-done > .app-icon {
  color: var(--success);
}

.tray-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.tray-name {
  flex: 1;
  min-width: 0;
}

.tray-actions {
  display: flex;
  gap: 4px;
  flex: none;
}

.spacer {
  flex: 1;
}
</style>
