<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseDialog from './BaseDialog.vue'
import AppIcon from './AppIcon.vue'
import { dialogState, resolveDialog } from '@/composables/useDialogs'

const acknowledged = ref(false)
const options = computed(() => dialogState.options)

watch(
  () => dialogState.open,
  (open) => {
    if (open) acknowledged.value = false
  },
)

function onClose() {
  if (options.value) resolveDialog(options.value.cancelKey)
}

function needsAck(key: string): boolean {
  const ack = options.value?.acknowledge
  return !!ack && ack.actionKeys.includes(key) && !acknowledged.value
}

const toneIcon = computed(() => (options.value?.tone === 'danger' || options.value?.tone === 'warning' ? 'alert' : 'info'))
</script>

<template>
  <BaseDialog
    v-if="options"
    :open="dialogState.open"
    :title="options.title"
    size="sm"
    initial-focus="[data-dialog-autofocus]"
    @close="onClose"
  >
    <div class="choice" :class="`tone-${options.tone ?? 'info'}`">
      <AppIcon :name="toneIcon" :size="20" class="choice-icon" />
      <div class="choice-body">
        <p class="choice-message">{{ options.message }}</p>
        <ul v-if="options.details?.length" class="choice-details">
          <li v-for="(line, i) in options.details" :key="i">{{ line }}</li>
        </ul>
        <label v-if="options.acknowledge" class="checkbox choice-ack">
          <input v-model="acknowledged" type="checkbox" />
          <span>{{ options.acknowledge.label }}</span>
        </label>
      </div>
    </div>
    <template #footer>
      <button
        v-for="action in options.actions"
        :key="action.key"
        type="button"
        class="btn"
        :class="{ 'btn-primary': action.kind === 'primary', 'btn-danger': action.kind === 'danger' }"
        :disabled="needsAck(action.key)"
        :data-dialog-autofocus="action.autofocus ? '' : undefined"
        @click="resolveDialog(action.key)"
      >
        {{ action.label }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.choice {
  display: flex;
  gap: 12px;
}

.choice-icon {
  margin-top: 2px;
  color: var(--info);
}

.tone-warning .choice-icon {
  color: var(--warning);
}

.tone-danger .choice-icon {
  color: var(--danger);
}

.choice-body {
  flex: 1;
  min-width: 0;
}

.choice-message {
  white-space: pre-line;
}

.choice-details {
  margin: 10px 0 0;
  padding-left: 18px;
  color: var(--text-2);
}

.choice-details li + li {
  margin-top: 4px;
}

.choice-ack {
  margin-top: 14px;
}
</style>
