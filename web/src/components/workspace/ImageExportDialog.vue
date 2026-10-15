<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import BaseDialog from '@/components/base/BaseDialog.vue'
import AppIcon from '@/components/base/AppIcon.vue'
import ErrorBlock from '@/components/base/ErrorBlock.vue'
import MarkdownViewer from './MarkdownViewer.vue'
import type { DocumentDetail } from '@/api/types'
import { clearHighlights } from '@/editor/searchHighlight'
import {
  BACKGROUND_THEMES,
  applyThemeVariables,
  exportMultiPageZip,
  exportSinglePageImage,
  type ImageBackgroundTheme,
} from '@/services/imageExport'
import { PAGE_PRESETS, type PageFormatPreset } from '@/utils/pagination'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  open: boolean
  doc: DocumentDetail
  currentTitle: string
  currentMarkdown: string
  isEditing: boolean
}>()

const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()
const mode = ref<'single' | 'multi'>('single')
const selectedThemeId = ref('pure-white')
const selectedPresetId = ref<'a4' | 'card' | 'compact'>('a4')
const stage = ref<'options' | 'rendering' | 'done' | 'failed'>('options')
const progress = ref(0)
const progressMessage = ref('')
const error = ref<unknown>(null)
const imageWarnings = ref<string[]>([])
const pageCount = ref(1)
const viewerRef = ref<InstanceType<typeof MarkdownViewer> | null>(null)
const viewerReady = ref(false)
const viewerVersion = ref(0)
const renderError = ref('')
const measuredHeight = ref(0)
const snapshot = ref({ title: '', markdown: '', metaText: '', isEditing: false })
let abortController: AbortController | null = null
let runToken = 0

const selectedTheme = computed<ImageBackgroundTheme>(() =>
  BACKGROUND_THEMES.find((theme) => theme.id === selectedThemeId.value) ?? BACKGROUND_THEMES[0]!,
)
const selectedPreset = computed<PageFormatPreset>(() =>
  PAGE_PRESETS.find((preset) => preset.id === selectedPresetId.value) ?? PAGE_PRESETS[0]!,
)
const presetNames = { a4: 'A4 纵向', card: '手机卡片（9:16）', compact: '方正卡片（3:4）' }
const themeHints: Record<string, string> = {
  'pure-white': '简洁白底',
  'warm-paper': '柔和米黄',
  'mint-fresh': '清新浅绿',
  'gradient-dawn': '蓝紫渐变',
  midnight: '深色背景',
}

function invalidateExport() {
  runToken++
  abortController?.abort()
  abortController = null
}

function resetPreview() {
  viewerReady.value = false
  renderError.value = ''
  measuredHeight.value = 0
  viewerVersion.value++
}

watch(() => props.open, (open) => {
  invalidateExport()
  if (!open) return
  snapshot.value = {
    title: props.currentTitle,
    markdown: props.currentMarkdown,
    metaText: `更新于 ${formatDateTime(props.doc.updated_at)} · 修订 r${props.doc.revision}`,
    isEditing: props.isEditing,
  }
  stage.value = 'options'
  progress.value = 0
  error.value = null
  imageWarnings.value = []
  pageCount.value = 1
  resetPreview()
}, { immediate: true, flush: 'sync' })

// Recreate the preview even when two backgrounds use the same light content theme.
watch(selectedThemeId, resetPreview, { flush: 'sync' })
onBeforeUnmount(invalidateExport)

async function onViewerRendered() {
  const token = runToken
  const version = viewerVersion.value
  await nextTick()
  if (!props.open || token !== runToken || version !== viewerVersion.value) return
  const viewer = viewerRef.value
  const root = viewer?.getRoot()
  if (!root || viewer?.isRendering()) return
  applyThemeVariables(root, selectedTheme.value)
  clearHighlights(root)
  root.querySelectorAll('.ljmdb-code-copy, .ljmdb-code-toolbar textarea').forEach((el) => el.remove())
  viewerReady.value = true
  measuredHeight.value = root.scrollHeight || root.offsetHeight
}

function onRenderError(message: string) {
  viewerReady.value = false
  renderError.value = message || '正文预览未能完成，请重试。'
}

async function startExport() {
  if (stage.value === 'rendering') return
  await nextTick()
  const viewer = viewerRef.value
  const root = viewer?.getRoot()
  if (!viewerReady.value || !root || viewer?.isRendering()) {
    error.value = new Error('正文预览尚未准备好，请返回选项稍候重试。')
    stage.value = 'failed'
    return
  }
  const token = ++runToken
  const controller = new AbortController()
  abortController?.abort()
  abortController = controller
  const isCurrent = () => props.open && token === runToken && !controller.signal.aborted
  stage.value = 'rendering'
  error.value = null
  imageWarnings.value = []
  progress.value = 5
  progressMessage.value = '正在准备文字与图片…'
  const common = {
    sourceElement: root,
    title: snapshot.value.title,
    theme: selectedTheme.value,
    metaText: snapshot.value.metaText,
    signal: controller.signal,
    onProgress: (value: number, message: string) => {
      if (!isCurrent()) return
      progress.value = value
      progressMessage.value = message
    },
  }
  try {
    if (mode.value === 'single') {
      const result = await exportSinglePageImage(common)
      if (!isCurrent()) return
      imageWarnings.value = result.corsWarnings
      pageCount.value = 1
    } else {
      const result = await exportMultiPageZip({ ...common, formatPreset: selectedPreset.value })
      if (!isCurrent()) return
      imageWarnings.value = result.corsWarnings
      pageCount.value = result.pageCount
    }
    stage.value = 'done'
  } catch (cause) {
    if (!isCurrent()) return
    error.value = cause
    stage.value = 'failed'
  } finally {
    if (token === runToken) abortController = null
  }
}

function closeDialog() {
  invalidateExport()
  emit('update:open', false)
}
</script>

<template>
  <BaseDialog
    :open="open"
    title="导出为图片"
    size="xl"
    :persistent="stage === 'rendering'"
    @update:open="closeDialog"
  >
    <div class="image-export-container">
      <div v-if="stage === 'rendering'" class="export-running-block" role="status" aria-live="polite">
        <div class="running-header">
          <span class="spinner" aria-hidden="true" />
          <span>{{ progressMessage || '正在准备文字与图片…' }}</span>
        </div>
        <div class="progress" role="progressbar" :aria-valuenow="progress" aria-valuemin="0" aria-valuemax="100" aria-label="图片生成进度">
          <span :style="{ width: `${progress}%` }" />
        </div>
        <p class="muted small">生成期间请保持页面开启。</p>
      </div>

      <div v-else-if="stage === 'done'" class="export-result">
        <div class="notice notice-success">
          <AppIcon name="check" class="notice-icon" :size="18" />
          <div class="notice-body">
            <div class="notice-title">图片已生成，浏览器已开始下载</div>
            <p class="small">
              {{ mode === 'single' ? '已生成一张 PNG 长图。' : `已生成 ${pageCount} 张图片，并打包为 ZIP。` }}
              请确认文件已保存到预期位置。
            </p>
          </div>
        </div>
        <div v-if="imageWarnings.length" class="notice notice-warning">
          <AppIcon name="alert" class="notice-icon" :size="18" />
          <div class="notice-body">
            <div class="notice-title">部分图片未能载入</div>
            <p class="small">以下图片已用提示框代替，其他内容已正常导出。</p>
            <ul class="small break-all image-warnings">
              <li v-for="(url, index) in imageWarnings" :key="index">{{ url }}</li>
            </ul>
          </div>
        </div>
      </div>

      <ErrorBlock
        v-else-if="stage === 'failed'"
        :error="error"
        action="图片导出"
        safety="可以返回选项调整后重试。"
        :on-retry="startExport"
      />

      <!-- Keep the rendered content alive while an export runs or is retried. -->
      <div v-show="stage === 'options'" class="export-options">
        <div v-if="snapshot.isEditing" class="notice notice-info editing-notice">
          <AppIcon name="edit" class="notice-icon" :size="16" />
          <div class="notice-body">导出使用打开此窗口时的编辑内容。</div>
        </div>
        <div class="export-main-layout">
          <div class="export-config-panel">
            <fieldset class="export-field">
              <legend class="field-label">导出方式</legend>
              <div class="mode-selector">
                <label class="option-card" :class="{ active: mode === 'single' }">
                  <input v-model="mode" type="radio" name="export-mode" value="single" />
                  <span><strong>单张长图（PNG）</strong><span class="option-desc">整篇内容保存在一张图片中。</span></span>
                </label>
                <label class="option-card" :class="{ active: mode === 'multi' }">
                  <input v-model="mode" type="radio" name="export-mode" value="multi" />
                  <span><strong>多页图片（ZIP）</strong><span class="option-desc">按所选比例分页，打包下载。</span></span>
                </label>
              </div>
            </fieldset>
            <p v-if="mode === 'single' && measuredHeight > 8000" class="notice notice-info small height-hint">
              内容较长，建议选择多页图片。
            </p>
            <fieldset v-if="mode === 'multi'" class="export-field">
              <legend class="field-label">页面比例</legend>
              <div class="preset-selector">
                <label v-for="preset in PAGE_PRESETS" :key="preset.id" class="option-card" :class="{ active: selectedPresetId === preset.id }">
                  <input v-model="selectedPresetId" type="radio" name="page-preset" :value="preset.id" />
                  <span>{{ presetNames[preset.id] }}</span>
                </label>
              </div>
              <p class="field-hint">实际页数会在导出完成后显示。</p>
            </fieldset>
            <fieldset class="export-field">
              <legend class="field-label">背景风格</legend>
              <div class="theme-cards-grid">
                <label v-for="theme in BACKGROUND_THEMES" :key="theme.id" class="theme-card" :class="{ active: selectedThemeId === theme.id }">
                  <input v-model="selectedThemeId" type="radio" name="bg-theme" :value="theme.id" class="sr-only" />
                  <span class="theme-color-swatch" :style="{ background: theme.swatchBg, borderColor: theme.swatchBorder }" />
                  <span class="theme-meta"><strong>{{ theme.name }}</strong><span class="option-desc">{{ themeHints[theme.id] }}</span></span>
                </label>
              </div>
            </fieldset>
          </div>
          <div class="export-preview-panel">
            <div class="preview-toolbar">
              <span>内容预览</span>
              <span class="muted small">{{ selectedTheme.name }}</span>
            </div>
            <div v-if="renderError" class="notice notice-danger preview-error" role="alert">
              <div class="notice-body">{{ renderError }} <button type="button" class="btn btn-sm" @click="resetPreview">重试预览</button></div>
            </div>
            <div class="preview-scroll-wrapper" :aria-busy="!viewerReady">
              <div class="export-paper-preview" :style="{ background: selectedTheme.bg, color: selectedTheme.text, borderColor: selectedTheme.border }">
                <div class="preview-paper-header" :style="{ borderColor: selectedTheme.border }">
                  <h1>{{ snapshot.title || '无标题' }}</h1>
                  <div class="small" :style="{ color: selectedTheme.textSecondary }">{{ snapshot.metaText }}</div>
                </div>
                <MarkdownViewer
                  v-if="open"
                  :key="viewerVersion"
                  ref="viewerRef"
                  :markdown="snapshot.markdown"
                  :theme="selectedTheme.viewerTheme"
                  @rendered="onViewerRendered"
                  @render-error="onRenderError"
                />
                <div class="preview-paper-footer" :style="{ borderColor: selectedTheme.border, color: selectedTheme.textSecondary }">
                  <span>OpenLJMDB</span><span>{{ mode === 'single' ? '单张长图' : presetNames[selectedPreset.id] }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
    <template #footer>
      <template v-if="stage === 'options'">
        <button type="button" class="btn" @click="closeDialog">取消</button>
        <button type="button" class="btn btn-primary" :disabled="!viewerReady" @click="startExport">
          <AppIcon name="download" :size="14" />{{ viewerReady ? (mode === 'single' ? '导出长图（PNG）' : '导出多页（ZIP）') : '正在准备预览…' }}
        </button>
      </template>
      <template v-else-if="stage === 'done'">
        <button type="button" class="btn" @click="stage = 'options'">重新配置</button>
        <button type="button" class="btn btn-primary" @click="closeDialog">完成</button>
      </template>
      <template v-else-if="stage === 'failed'">
        <button type="button" class="btn" @click="stage = 'options'">返回选项</button>
        <button type="button" class="btn" @click="closeDialog">关闭</button>
      </template>
      <button v-else type="button" class="btn" @click="closeDialog">取消导出</button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.image-export-container { min-width: 0; }
.export-options { display: flex; flex-direction: column; gap: 16px; }
.export-main-layout { display: grid; grid-template-columns: 280px minmax(0, 1fr); gap: 20px; }
.export-config-panel { display: flex; flex-direction: column; gap: 18px; min-width: 0; }
.export-field { border: 0; margin: 0; padding: 0; min-width: 0; }
.export-field legend { padding: 0; margin-bottom: 8px; }
.mode-selector, .preset-selector, .theme-cards-grid { display: grid; gap: 8px; }
.option-card, .theme-card { display: flex; align-items: flex-start; gap: 10px; border: 1px solid var(--border); border-radius: var(--radius); padding: 10px 12px; cursor: pointer; }
.option-card input { flex: 0 0 auto; margin-top: 4px; }
.option-card strong, .theme-card strong { font-size: 13px; }
.option-card.active, .theme-card.active { border-color: var(--primary); background: var(--primary-soft); }
.option-card:focus-within, .theme-card:has(input:focus-visible) { outline: 2px solid var(--primary); outline-offset: 2px; }
.option-desc { display: block; margin-top: 3px; font-size: 12px; color: var(--text-3); line-height: 1.5; }
.theme-card { position: relative; align-items: center; }
.theme-card:hover { border-color: var(--primary); }
.theme-color-swatch { width: 28px; height: 28px; border-radius: 50%; border: 1px solid; flex: 0 0 auto; }
.theme-meta { min-width: 0; }
.height-hint { margin: 0; }
.export-preview-panel { display: flex; flex-direction: column; min-width: 0; height: 610px; border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden; background: var(--bg-muted); }
.preview-toolbar { display: flex; flex: 0 0 auto; align-items: center; justify-content: space-between; padding: 10px 14px; border-bottom: 1px solid var(--border); background: var(--bg-elevated); font-size: 13px; }
.preview-scroll-wrapper { min-height: 0; overflow: auto; padding: 18px; }
.export-paper-preview { width: 100%; box-sizing: border-box; padding: 28px; border: 1px solid; border-radius: var(--radius); box-shadow: var(--shadow-sm); overflow-wrap: anywhere; }
.preview-paper-header { margin-bottom: 24px; padding-bottom: 14px; border-bottom: 1px solid; }
.preview-paper-header h1 { font-size: 24px; line-height: 1.4; margin: 0 0 8px; }
.preview-paper-footer { display: flex; justify-content: space-between; gap: 12px; margin-top: 28px; padding-top: 12px; border-top: 1px solid; font-size: 12px; }
.export-paper-preview :deep(.ljmdb-code-copy), .export-paper-preview :deep(.ljmdb-code-toolbar textarea) { display: none !important; }
.export-paper-preview :deep(mark.ljmdb-hit) { color: inherit; background: transparent; outline: none; }
.preview-error { margin: 12px; }
.export-running-block { display: flex; flex-direction: column; align-items: center; gap: 18px; padding: 64px 16px; }
.running-header { display: flex; align-items: center; gap: 10px; }
.export-running-block .progress { width: min(440px, 90%); }
.export-result { display: flex; flex-direction: column; gap: 16px; padding: 16px 0; }
.image-warnings { margin: 8px 0 0; padding-left: 20px; }
@media (max-width: 850px) {
  .export-main-layout { grid-template-columns: minmax(0, 1fr); }
  .export-preview-panel { height: 440px; }
  .theme-cards-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .preview-scroll-wrapper { padding: 12px; }
  .export-paper-preview { padding: 20px; }
}
@media (max-width: 480px) {
  .theme-cards-grid { grid-template-columns: minmax(0, 1fr); }
  .preview-paper-header h1 { font-size: 20px; }
  .export-paper-preview { padding: 16px; }
}
</style>
