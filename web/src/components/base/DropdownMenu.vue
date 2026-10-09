<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import AppIcon from './AppIcon.vue'

export interface MenuItem {
  key: string
  label: string
  icon?: string
  danger?: boolean
  disabled?: boolean
  /** 禁用原因或补充说明 */
  hint?: string
  separatorBefore?: boolean
}

/**
 * 可键盘操作的菜单按钮（WAI-ARIA menu button）：
 * Enter / Space / ↓ 打开并聚焦首项，↑↓ Home End 移动，Esc 关闭并返回按钮，Tab 关闭。
 */
const props = withDefaults(
  defineProps<{
    items: MenuItem[]
    label: string
    icon?: string
    text?: string
    align?: 'left' | 'right'
    buttonClass?: string
    small?: boolean
    tabindex?: number
  }>(),
  { icon: 'more', text: undefined, align: 'right', buttonClass: 'btn btn-icon', small: false, tabindex: undefined },
)

const emit = defineEmits<{ (e: 'select', key: string): void; (e: 'open'): void }>()

const open = ref(false)
const position = ref({ left: '0px', top: '0px', maxHeight: '0px' })
const buttonEl = ref<HTMLButtonElement | null>(null)
const menuEl = ref<HTMLElement | null>(null)
const menuId = `menu-${Math.random().toString(36).slice(2, 9)}`

const enabledIndexes = computed(() => props.items.map((item, i) => (item.disabled ? -1 : i)).filter((i) => i >= 0))

function itemEls(): HTMLElement[] {
  return Array.from(menuEl.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])
}

function focusItem(index: number) {
  const els = itemEls()
  els[index]?.focus()
}

function positionMenu() {
  const button = buttonEl.value!
  const menu = menuEl.value!
  const rect = button.getBoundingClientRect()
  const gap = 4
  const margin = 8
  const below = window.innerHeight - rect.bottom - gap - margin
  const above = rect.top - gap - margin
  const up = menu.scrollHeight > below && above > below
  const height = Math.min(menu.scrollHeight, up ? above : below)
  const left = props.align === 'right' ? rect.right - menu.offsetWidth : rect.left
  position.value = {
    left: `${Math.max(margin, Math.min(left, window.innerWidth - menu.offsetWidth - margin))}px`,
    top: `${up ? rect.top - gap - height : rect.bottom + gap}px`,
    maxHeight: `${Math.max(0, up ? above : below)}px`,
  }
}

function onScroll(event: Event) {
  if (menuEl.value?.contains(event.target as Node)) return
  positionMenu()
}

async function openMenu(focus: 'first' | 'last' | 'none' = 'first') {
  if (open.value) return
  emit('open')
  open.value = true
  await nextTick()
  positionMenu()
  window.addEventListener('resize', positionMenu)
  document.addEventListener('scroll', onScroll, true)
  document.addEventListener('pointerdown', onOutside, true)
  if (focus === 'first') focusItem(enabledIndexes.value[0] ?? 0)
  if (focus === 'last') focusItem(enabledIndexes.value[enabledIndexes.value.length - 1] ?? 0)
}

function close(returnFocus = true) {
  if (!open.value) return
  open.value = false
  document.removeEventListener('pointerdown', onOutside, true)
  window.removeEventListener('resize', positionMenu)
  document.removeEventListener('scroll', onScroll, true)
  if (returnFocus) buttonEl.value?.focus()
}

function onOutside(event: Event) {
  const target = event.target as Node
  if (menuEl.value?.contains(target) || buttonEl.value?.contains(target)) return
  close(false)
}

function toggle() {
  if (open.value) close()
  else void openMenu('first')
}

function onButtonKey(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    void openMenu('first')
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    void openMenu('last')
  }
}

function onMenuKey(event: KeyboardEvent) {
  const els = itemEls()
  const current = els.indexOf(document.activeElement as HTMLElement)
  const enabled = enabledIndexes.value
  const pos = enabled.indexOf(current)
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    focusItem(enabled[(pos + 1) % enabled.length] ?? 0)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    focusItem(enabled[(pos - 1 + enabled.length) % enabled.length] ?? 0)
  } else if (event.key === 'Home') {
    event.preventDefault()
    focusItem(enabled[0] ?? 0)
  } else if (event.key === 'End') {
    event.preventDefault()
    focusItem(enabled[enabled.length - 1] ?? 0)
  } else if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    close()
  } else if (event.key === 'Tab') {
    close(false)
  }
}

function choose(item: MenuItem) {
  if (item.disabled) return
  close()
  emit('select', item.key)
}

onBeforeUnmount(() => close(false))

defineExpose({ openMenu, close })
</script>

<template>
  <div class="dropdown" :class="{ open }">
    <button
      ref="buttonEl"
      type="button"
      :class="[buttonClass, { 'btn-sm': small }]"
      :tabindex="tabindex"
      :aria-label="text ? undefined : label"
      :title="label"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-controls="open ? menuId : undefined"
      @click.stop="toggle"
      @keydown="onButtonKey"
    >
      <AppIcon v-if="icon" :name="icon" />
      <span v-if="text">{{ text }}</span>
      <AppIcon v-if="text" name="chevron-down" :size="14" />
    </button>
    <Teleport to="body">
      <ul
        v-if="open"
        :id="menuId"
        ref="menuEl"
        class="dropdown-menu"
        :style="position"
        role="menu"
        :aria-label="label"
        @keydown="onMenuKey"
      >
        <template v-for="item in items" :key="item.key">
          <li v-if="item.separatorBefore" class="separator" role="separator" />
          <li
            role="menuitem"
            :tabindex="item.disabled ? undefined : -1"
            :aria-disabled="item.disabled ? 'true' : undefined"
            class="dropdown-item"
            :class="{ danger: item.danger, disabled: item.disabled }"
            :title="item.hint"
            @click.stop="choose(item)"
            @keydown.enter.prevent="choose(item)"
            @keydown.space.prevent="choose(item)"
          >
            <AppIcon v-if="item.icon" :name="item.icon" />
            <span class="dropdown-label">
              {{ item.label }}
              <span v-if="item.hint && item.disabled" class="dropdown-hint">{{ item.hint }}</span>
            </span>
          </li>
        </template>
      </ul>
    </Teleport>
  </div>
</template>

<style scoped>
.dropdown {
  position: relative;
  display: inline-flex;
}

.dropdown-menu {
  position: fixed;
  overflow-y: auto;
  z-index: 60;
  width: max-content;
  min-width: 180px;
  max-width: min(300px, calc(100vw - 16px));
  margin: 0;
  padding: 4px;
  list-style: none;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow-md);
}

.dropdown-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  color: var(--text);
  line-height: 1.45;
}

.dropdown-item :deep(.app-icon) {
  margin-top: 2px;
  color: var(--text-2);
}

.dropdown-item:hover:not(.disabled),
.dropdown-item:focus {
  background: var(--bg-hover);
  outline: none;
}

.dropdown-item:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: -2px;
}

.dropdown-item.danger,
.dropdown-item.danger :deep(.app-icon) {
  color: var(--danger);
}

.dropdown-item.disabled {
  cursor: not-allowed;
  color: var(--text-3);
}

.dropdown-label {
  display: flex;
  flex-direction: column;
}

.dropdown-hint {
  font-size: 12px;
  color: var(--text-3);
}

.separator {
  height: 1px;
  margin: 4px 2px;
  background: var(--border);
}
</style>
