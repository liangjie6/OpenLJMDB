<script setup lang="ts">
import { computed } from 'vue'
import { collapseEqual, lineDiff } from '@/utils/diff'

/** 行级差异：旧（左标签）→ 新（右标签）。新增 / 删除同时用符号和文字说明，不只依赖颜色 */
const props = withDefaults(
  defineProps<{ oldText: string; newText: string; oldLabel: string; newLabel: string; context?: number }>(),
  { context: 3 },
)

const diff = computed(() => lineDiff(props.oldText, props.newText))
const rows = computed(() => collapseEqual(diff.value.lines, props.context))
</script>

<template>
  <div class="diff-view">
    <div class="diff-summary">
      <template v-if="diff.identical">两份内容的正文完全相同。</template>
      <template v-else>
        从“{{ oldLabel }}”到“{{ newLabel }}”：
        <span class="ins-count">新增 {{ diff.inserted }} 行</span>，
        <span class="del-count">删除 {{ diff.deleted }} 行</span>
      </template>
    </div>
    <div v-if="!diff.identical" class="diff-table" role="table" :aria-label="`${oldLabel} 与 ${newLabel} 的差异`">
      <template v-for="(row, i) in rows" :key="i">
        <div v-if="row.kind === 'gap'" class="diff-gap" role="row">
          <span role="cell">… 省略 {{ row.count }} 行相同内容 …</span>
        </div>
        <div v-else class="diff-row" :class="`diff-${row.kind}`" role="row">
          <span class="diff-no" role="cell">{{ row.oldNo ?? '' }}</span>
          <span class="diff-no" role="cell">{{ row.newNo ?? '' }}</span>
          <span class="diff-sign" role="cell" aria-hidden="true">{{
            row.kind === 'insert' ? '+' : row.kind === 'delete' ? '−' : ' '
          }}</span>
          <span class="diff-text" role="cell"
            ><span v-if="row.kind !== 'equal'" class="sr-only">{{ row.kind === 'insert' ? '新增：' : '删除：' }}</span
            >{{ row.text || ' ' }}</span
          >
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.diff-summary {
  margin-bottom: 8px;
  color: var(--text-2);
}

.ins-count {
  color: var(--diff-insert-strong);
}

.del-count {
  color: var(--diff-delete-strong);
}

.diff-table {
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.55;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: auto;
  max-height: 52vh;
  background: var(--bg);
}

.diff-row {
  display: grid;
  grid-template-columns: 44px 44px 18px 1fr;
}

.diff-no {
  padding: 0 6px;
  text-align: right;
  color: var(--text-3);
  background: var(--bg-soft);
  user-select: none;
}

.diff-sign {
  text-align: center;
  user-select: none;
}

.diff-text {
  padding-right: 10px;
  white-space: pre-wrap;
  word-break: break-word;
}

.diff-insert {
  background: var(--diff-insert);
}

.diff-insert .diff-sign {
  color: var(--diff-insert-strong);
  font-weight: 700;
}

.diff-delete {
  background: var(--diff-delete);
}

.diff-delete .diff-sign {
  color: var(--diff-delete-strong);
  font-weight: 700;
}

.diff-gap {
  padding: 2px 10px;
  color: var(--text-3);
  background: var(--bg-muted);
  text-align: center;
}
</style>
