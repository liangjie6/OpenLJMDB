import type { NavigationGuardWithThis } from 'vue-router'

type LeaveGuard = (to: { name: unknown; params: Record<string, string> }) => Promise<boolean> | boolean

const guards: LeaveGuard[] = []

/**
 * 注册路由离开守卫：返回 false 阻止导航。
 * 用于编辑器未保存时提示用户；组件卸载时应调用返回的清理函数。
 */
export function addLeaveGuard(guard: LeaveGuard): () => void {
  guards.push(guard)
  return () => {
    const index = guards.indexOf(guard)
    if (index > -1) guards.splice(index, 1)
  }
}

export const leaveGuard: NavigationGuardWithThis<undefined> = async (to) => {
  for (const guard of guards) {
    const result = await guard({ name: to.name, params: to.params as Record<string, string> })
    if (!result) return false
  }
  return true
}
