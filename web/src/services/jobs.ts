import { api } from '@/api'
import { ApiError, isApiError } from '@/api/http'
import type { Job } from '@/api/types'

export interface WaitJobOptions {
  signal?: AbortSignal
  onUpdate?: (job: Job) => void
  /** 轮询间隔（毫秒） */
  intervalMs?: number
  /** 连接中断、维护或数据库切换期间的暂时错误：继续轮询（恢复任务需要跨数据库替换继续查询） */
  onTransientError?: (error: ApiError, attempt: number) => void
}

const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(new ApiError({ status: 0, code: 'ABORTED', message: '已停止查询任务状态', kind: 'aborted' }))
      return
    }
    const timer = setTimeout(resolve, ms)
    signal?.addEventListener(
      'abort',
      () => {
        clearTimeout(timer)
        reject(new ApiError({ status: 0, code: 'ABORTED', message: '已停止查询任务状态', kind: 'aborted' }))
      },
      { once: true },
    )
  })

export function isTerminal(job: Job): boolean {
  return job.state === 'succeeded' || job.state === 'failed' || job.state === 'cancelled'
}

/**
 * 轮询任务直至结束。HTTP 202 只表示任务已受理，结果以任务状态为准。
 * 暂时性错误（网络、503、数据库繁忙）以有界退避继续查询，不重复提交任务。
 */
export async function waitForJob(initial: Job, options: WaitJobOptions = {}): Promise<Job> {
  let job = initial
  options.onUpdate?.(job)
  let transientAttempts = 0
  let notFoundAttempts = 0
  const base = options.intervalMs ?? 1000
  while (!isTerminal(job)) {
    const delay = transientAttempts > 0 ? Math.min(5000, base * 2 ** Math.min(transientAttempts, 3)) : base
    await sleep(delay, options.signal)
    try {
      job = await api.jobs.get(job.id, options.signal)
      transientAttempts = 0
      notFoundAttempts = 0
      options.onUpdate?.(job)
    } catch (error) {
      // 恢复切换数据库期间任务可能短暂查询不到；持续 404 则视为任务不存在
      if (isApiError(error) && error.status === 404 && ++notFoundAttempts > 15) throw error
      if (isApiError(error) && (error.retryable || error.status === 404) && error.kind !== 'aborted') {
        transientAttempts++
        options.onTransientError?.(error, transientAttempts)
        continue
      }
      throw error
    }
  }
  return job
}
