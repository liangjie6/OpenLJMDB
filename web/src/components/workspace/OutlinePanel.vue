<script setup lang="ts">
import { computed } from 'vue'
import type { OutlineItem } from '@/editor/outline'

const props = defineProps<{ items: OutlineItem[]; activeIndex: number }>()
const emit = defineEmits<{ (e: 'navigate', item: OutlineItem): void; (e: 'close'): void }>()

const minLevel = computed(() => props.items.reduce((min, item) => Math.min(min, item.level), 6))
</script>

<template>
  <nav class="outline-panel" aria-label="大纲">
    <div class="outline-header">
      <h2 class="outline-title">大纲</h2>
      <button type="button" class="btn btn-icon btn-sm" aria-label="隐藏大纲" title="隐藏大纲" @click="emit('close')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M6 6l12 12M18 6 6 18" />
        </svg>
      </button>
    </div>
    <p v-if="items.length === 0" class="outline-empty">正文中还没有标题。使用“# 标题”即可生成大纲。</p>
    <ol v-else class="outline-list">
      <li v-for="(item, index) in items" :key="item.key" :style="{ paddingLeft: `${(item.level - minLevel) * 12}px` }">
        <button
          type="button"
          class="outline-link"
          :class="{ active: index === activeIndex }"
          :aria-current="index === activeIndex ? 'location' : undefined"
          :title="item.text"
          @click="emit('navigate', item)"
        >
          {{ item.text }}
        </button>
      </li>
    </ol>
  </nav>
</template>

<style scoped>
.outline-panel {
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
}

.outline-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 12px 6px 16px;
}

.outline-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-2);
}

.outline-empty {
  padding: 4px 16px;
  font-size: 12px;
  color: var(--text-3);
}

.outline-list {
  list-style: none;
  margin: 0;
  padding: 0 8px 24px;
  overflow: auto;
}

.outline-link {
  display: block;
  width: 100%;
  padding: 3px 8px;
  border: 0;
  border-left: 2px solid transparent;
  background: none;
  color: var(--text-2);
  font: inherit;
  font-size: 13px;
  line-height: 1.5;
  text-align: left;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
}

.outline-link:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.outline-link.active {
  color: var(--primary-text);
  border-left-color: var(--primary);
  background: var(--primary-soft);
}
</style>
