<script setup lang="ts">
import AppIcon from './AppIcon.vue'

withDefaults(defineProps<{ icon?: string; title: string; description?: string }>(), {
  icon: 'file-text',
  description: undefined,
})
</script>

<template>
  <div class="empty-state">
    <div class="empty-icon"><AppIcon :name="icon" :size="34" :stroke-width="1.4" /></div>
    <h2 class="empty-title">{{ title }}</h2>
    <p v-if="description" class="empty-desc">{{ description }}</p>
    <div v-if="$slots.default" class="row wrap empty-actions">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 48px 16px;
  color: var(--text-2);
}

.empty-icon {
  display: grid;
  place-items: center;
  width: 64px;
  height: 64px;
  margin-bottom: 14px;
  border-radius: 50%;
  background: var(--bg-muted);
  color: var(--text-3);
}

.empty-title {
  font-size: 16px;
  color: var(--text);
}

.empty-desc {
  max-width: 460px;
  margin-top: 6px;
  color: var(--text-3);
}

.empty-actions {
  justify-content: center;
  margin-top: 18px;
}
</style>
