<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './base/AppIcon.vue'
import { useHealthStore } from '@/stores/health'
import { useDraftsStore } from '@/stores/drafts'
import { formatDateTime } from '@/utils/format'

const health = useHealthStore()
const drafts = useDraftsStore()

const MAINTENANCE_KIND: Record<string, string> = {
  backup: '正在创建完整备份',
  restore: '正在恢复完整备份',
  migration: '正在升级数据库',
  migrate: '正在升级数据库',
  reindex: '正在重建搜索索引',
  cleanup: '正在清理附件',
}

const maintenanceText = computed(() => {
  const m = health.maintenance
  if (!m) return ''
  return m.message || (m.kind ? (MAINTENANCE_KIND[m.kind] ?? `维护中（${m.kind}）`) : '系统维护中')
})

const dbProblem = computed(() => {
  const db = health.health?.database
  return db && !['ok', 'ready', 'healthy', 'unknown', 'normal'].includes(db) ? db : null
})

function reload() {
  window.location.reload()
}
</script>

<template>
  <div class="global-banners">
    <div v-if="health.epochChanged" class="banner banner-danger" role="alert">
      <AppIcon name="alert" :size="18" />
      <div class="banner-body">
        <strong>{{ health.restoreCompleted ? '完整备份已恢复，需要重新加载页面' : health.instanceChanged ? '当前地址已对应另一套数据' : '数据已被恢复或替换' }}</strong>
        <span>
          本页面已暂停全部写入，不会用旧页面内容覆盖恢复后的数据。未保存的编辑仍保留在浏览器本地草稿中，重新加载后需要确认取舍。
        </span>
      </div>
      <button type="button" class="btn btn-sm btn-danger" @click="reload">重新加载页面</button>
    </div>
    <div v-else-if="health.status === 'disconnected'" class="banner banner-warning" role="alert">
      <AppIcon name="cloud-off" :size="18" />
      <div class="banner-body">
        <strong>连接中断：无法连接本地服务</strong>
        <span>
          请确认程序仍在运行。编辑内容会保留在浏览器本地草稿中，重新连接后会校验修订再继续保存。
          <template v-if="health.lastOkAt">最近一次连接成功：{{ formatDateTime(health.lastOkAt) }}。</template>
        </span>
      </div>
      <button type="button" class="btn btn-sm" @click="health.refresh()">重试连接</button>
    </div>
    <div v-if="!health.epochChanged && health.maintenance" class="banner banner-info" role="status">
      <AppIcon name="lock" :size="18" />
      <div class="banner-body">
        <strong>{{ maintenanceText }}，写入已暂时暂停</strong>
        <span>正在编辑的内容保留在本地草稿中，维护结束后自动继续保存。</span>
      </div>
    </div>
    <div v-if="!health.epochChanged && health.status === 'ok' && !health.writable" class="banner banner-danger" role="alert">
      <AppIcon name="alert" :size="18" />
      <div class="banner-body">
        <strong>数据目录当前不可写，所有保存已暂停</strong>
        <span>请检查数据目录权限或磁盘空间（路径见“设置”）；编辑内容保留在本地草稿中。</span>
      </div>
    </div>
    <div v-if="!health.epochChanged && dbProblem" class="banner banner-warning" role="status">
      <AppIcon name="database" :size="18" />
      <div class="banner-body">
        <strong>数据库状态：{{ dbProblem }}</strong>
        <span>部分操作可能失败；请查看“设置”中的数据库状态与日志位置。</span>
      </div>
    </div>
    <div v-if="drafts.available === false" class="banner banner-warning" role="status">
      <AppIcon name="draft" :size="18" />
      <div class="banner-body">
        <strong>本地草稿保护不可用</strong>
        <span>{{ drafts.errorMessage }}。尚未保存到服务的内容在页面关闭或异常时可能丢失，可在编辑页使用“导出草稿”手动保留。</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.global-banners:empty {
  display: none;
}

.banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}

.banner > .app-icon {
  margin-top: 2px;
}

.banner-body {
  flex: 1;
  display: flex;
  flex-wrap: wrap;
  gap: 2px 10px;
}

.banner-danger {
  background: var(--danger-soft);
  border-color: color-mix(in srgb, var(--danger) 30%, transparent);
}

.banner-danger > .app-icon {
  color: var(--danger);
}

.banner-warning {
  background: var(--warning-bg);
  border-color: var(--warning-border);
}

.banner-warning > .app-icon {
  color: var(--warning);
}

.banner-info {
  background: var(--info-bg);
  border-color: var(--info-border);
}

.banner-info > .app-icon {
  color: var(--info);
}
</style>
