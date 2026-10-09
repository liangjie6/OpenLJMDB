<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppTopBar from '@/components/AppTopBar.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import { useSettingsStore } from '@/stores/settings'
import { useHealthStore } from '@/stores/health'

const preferences = useSettingsStore()
const health = useHealthStore()

const settings = computed(() => preferences.settings)
const loading = ref(false)
const error = ref<unknown>(null)
const saving = ref(false)
const saveError = ref<unknown>(null)
const saveSuccess = ref(false)

const formData = ref({
  locale: 'zh-CN',
  timezone: 'Asia/Shanghai',
  theme: 'auto' as 'light' | 'dark' | 'auto',
  import_auto_classify: false,
})

const hasChanges = computed(() => {
  if (!settings.value) return false
  return (
    formData.value.locale !== settings.value.locale ||
    formData.value.timezone !== settings.value.timezone ||
    formData.value.theme !== (settings.value.theme ?? 'auto') ||
    formData.value.import_auto_classify !== settings.value.import_auto_classify
  )
})

async function load() {
  loading.value = true
  error.value = null
  try {
    const data = await preferences.load()
    formData.value = {
      locale: data.locale!,
      timezone: data.timezone!,
      theme: data.theme ?? 'auto',
      import_auto_classify: data.import_auto_classify,
    }
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  saveError.value = null
  saveSuccess.value = false
  try {
    await preferences.update(formData.value)
    saveSuccess.value = true
    setTimeout(() => (saveSuccess.value = false), 3000)
  } catch (e) {
    saveError.value = e
  } finally {
    saving.value = false
  }
}

function reset() {
  if (settings.value) {
    formData.value = {
      locale: settings.value.locale!,
      timezone: settings.value.timezone!,
      theme: settings.value.theme ?? 'auto',
      import_auto_classify: settings.value.import_auto_classify,
    }
  }
}

onMounted(() => void load())
</script>

<template>
  <div>
    <AppTopBar />
    <main id="main" class="page page-narrow">
      <header class="page-header">
        <div>
          <h1>设置</h1>
        </div>
      </header>

      <ErrorBlock v-if="error" :error="error" action="加载设置" :on-retry="load" />
      <p v-else-if="loading" class="muted">正在加载设置…</p>

      <form v-else-if="settings" class="settings-form" @submit.prevent="save">
        <section class="card section">
          <h2>语言与时区</h2>
          <div class="field">
            <label for="locale">界面语言</label>
            <select id="locale" v-model="formData.locale" class="select">
              <option value="zh-CN">简体中文（中国大陆）</option>
            </select>
          </div>
          <div class="field">
            <label for="timezone">时区</label>
            <select id="timezone" v-model="formData.timezone" class="select">
              <option value="Asia/Shanghai">中国标准时间 (UTC+8)</option>
              <option value="UTC">协调世界时 (UTC)</option>
              <option value="America/New_York">美国东部时间</option>
              <option value="America/Los_Angeles">美国太平洋时间</option>
              <option value="Europe/London">欧洲伦敦时间</option>
            </select>
          </div>
        </section>

        <section class="card section">
          <h2>外观</h2>
          <div class="field">
            <label for="theme">主题</label>
            <select id="theme" v-model="formData.theme" class="select">
              <option value="auto">跟随系统</option>
              <option value="light">浅色</option>
              <option value="dark">深色</option>
            </select>
          </div>
        </section>

        <section class="card section">
          <h2>导入</h2>
          <label class="row" for="import-auto-classify">
            <input id="import-auto-classify" v-model="formData.import_auto_classify" type="checkbox" />
            导入自动分类
          </label>
          <p class="field-hint">导入 Markdown 时，根据完整笔记内容选择最适合的知识库；确认前可逐篇修改。ZIP 保留目录结构，不参与自动分类。</p>
          <p class="field-hint">开启后，笔记正文和知识库名称、描述将发送至分类服务。</p>
          <p v-if="!settings.import_classifier_configured" class="field-hint">分类服务尚未配置，导入时可手动选择位置。</p>
        </section>

        <section class="card section">
          <h2>系统信息</h2>
          <dl class="dl">
            <dt>应用版本</dt>
            <dd>{{ health.health?.version ?? '—' }}</dd>
            <dt>数据库结构版本</dt>
            <dd>{{ health.health?.schema_version ?? '—' }}</dd>
            <dt>数据实例 ID</dt>
            <dd class="mono">{{ health.instanceId || '—' }}</dd>
            <dt>数据目录</dt>
            <dd class="break-all">{{ settings.runtime.data_dir ?? '—' }}</dd>
            <dt>服务地址</dt>
            <dd class="break-all">{{ health.health?.runtime?.base_url ?? '' }}</dd>
          </dl>
        </section>

        <ErrorBlock v-if="saveError" :error="saveError" action="保存设置" compact />
        <div v-if="saveSuccess" class="notice notice-success" role="status">
          <AppIcon name="check" class="notice-icon" :size="18" />
          <div class="notice-body">设置已保存</div>
        </div>

        <div class="row actions">
          <button type="button" class="btn" :disabled="!hasChanges || saving" @click="reset">重置</button>
          <button type="submit" class="btn btn-primary" :disabled="!hasChanges || saving || !health.canWrite">
            <span v-if="saving" class="spinner" aria-hidden="true" />保存
          </button>
        </div>
      </form>
    </main>
  </div>
</template>

<style scoped>
.settings-form {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.section {
  padding: 20px;
}

.section h2 {
  font-size: 16px;
  margin-bottom: 14px;
}

.actions {
  justify-content: flex-end;
}

.dl {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 8px 16px;
}

.dl dt {
  color: var(--text-2);
}

.dl dd {
  word-break: break-word;
}

.mono {
  font-family: var(--font-mono);
  font-size: 13px;
}

.break-all {
  word-break: break-all;
}
</style>
