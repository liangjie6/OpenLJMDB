<script setup lang="ts">
import { computed } from 'vue'
import { highlightSegments } from '@/utils/text'

/** 按码点区间高亮纯文本；以文本插值渲染，片段不会被当作 HTML 执行 */
const props = defineProps<{ text: string; ranges: ReadonlyArray<readonly [number, number]> }>()
const segments = computed(() => highlightSegments(props.text, props.ranges))
</script>

<template>
  <span class="highlighted-text"
    ><template v-for="(seg, i) in segments" :key="i"
      ><mark v-if="seg.hit">{{ seg.text }}</mark
      ><template v-else>{{ seg.text }}</template></template
    ></span
  >
</template>
