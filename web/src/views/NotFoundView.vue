<script setup lang="ts">
import { useRouter } from 'vue-router'
import EmptyState from '@/components/base/EmptyState.vue'

const router = useRouter()
</script>

<template>
  <div class="not-found">
    <EmptyState icon="alert" title="页面不存在" description="你访问的页面不存在，或链接已失效。">
      <button type="button" class="btn" @click="router.back()">返回上一页</button>
      <RouterLink class="btn btn-primary" to="/">前往首页</RouterLink>
    </EmptyState>
  </div>
</template>

<style scoped>
.not-found {
  display: grid;
  place-items: center;
  min-height: 100vh;
  padding: 20px;
}
</style>
