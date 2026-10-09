import type { ApiMeta } from './types'

/** 所有接口前缀；前端使用相对路径，不固化主机与端口 */
export const API_BASE = '/api/v1'

export type ApiErrorKind = 'http' | 'network' | 'timeout' | 'aborted' | 'parse'

/** 可自动重试的服务端错误码（有界退避） */
const RETRYABLE_CODES = new Set(['DATABASE_BUSY', 'MAINTENANCE_MODE'])

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: Record<string, unknown>
  readonly requestId: string | null
  readonly kind: ApiErrorKind

  constructor(init: {
    status: number
    code: string
    message: string
    details?: Record<string, unknown>
    requestId?: string | null
    kind: ApiErrorKind
  }) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.details = init.details ?? {}
    this.requestId = init.requestId ?? null
    this.kind = init.kind
  }

  /** 服务不可达、超时、短暂数据库繁忙、维护和网关类错误可自动重试 */
  get retryable(): boolean {
    if (this.kind === 'network' || this.kind === 'timeout') return true
    if (this.kind === 'aborted' || this.kind === 'parse') return false
    if (RETRYABLE_CODES.has(this.code)) return true
    return this.status === 502 || this.status === 503 || this.status === 504 || this.status === 500
  }

  is(code: string): boolean {
    return this.code === code
  }
}

export function isApiError(value: unknown): value is ApiError {
  return value instanceof ApiError
}

export function isAbortError(value: unknown): boolean {
  return (isApiError(value) && value.kind === 'aborted') || (value instanceof DOMException && value.name === 'AbortError')
}

let currentEpoch: string | null = null

/**
 * 页面加载时从 health 取得的数据世代。所有写请求携带该值；
 * 世代变化后不自动替换为新值重发（见 03 文档第 7 节）。
 */
export function setRequestEpoch(epoch: string | null): void {
  currentEpoch = epoch
}

export function getRequestEpoch(): string | null {
  return currentEpoch
}

export interface RequestOptions {
  query?: Record<string, string | number | boolean | null | undefined>
  body?: unknown
  /** multipart 等非 JSON 正文 */
  rawBody?: BodyInit
  headers?: Record<string, string>
  signal?: AbortSignal
  idempotencyKey?: string
  timeoutMs?: number
  keepalive?: boolean
}

export interface ApiResponse<T> {
  data: T
  meta: ApiMeta
  status: number
}

const WRITE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

export function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = path.startsWith('/api/') ? path : `${API_BASE}${path}`
  if (!query) return url
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `${url}?${qs}` : url
}

function headerValue(headers: Headers, name: string): string | null {
  try {
    return headers.get(name)
  } catch {
    return null
  }
}

export function errorFromPayload(status: number, payload: unknown, headers?: Headers): ApiError {
  const body = (payload ?? {}) as Record<string, unknown>
  const err = (body.error ?? {}) as Record<string, unknown>
  const code = typeof err.code === 'string' && err.code ? err.code : status >= 500 ? 'INTERNAL_ERROR' : `HTTP_${status}`
  const message =
    typeof err.message === 'string' && err.message ? err.message : defaultMessageForStatus(status)
  const details = err.details && typeof err.details === 'object' ? (err.details as Record<string, unknown>) : {}
  const requestId =
    (typeof body.request_id === 'string' ? body.request_id : null) ?? (headers ? headerValue(headers, 'X-Request-Id') : null)
  return new ApiError({ status, code, message, details, requestId, kind: 'http' })
}

function defaultMessageForStatus(status: number): string {
  switch (status) {
    case 400:
      return '请求参数无效'
    case 404:
      return '请求的内容不存在'
    case 409:
      return '数据已被其他操作修改'
    case 413:
      return '内容超出允许的大小'
    case 415:
      return '不支持的文件类型'
    case 422:
      return '请求无法处理'
    case 503:
      return '服务暂时不可用'
    case 507:
      return '存储空间不足'
    default:
      return status >= 500 ? '服务内部错误' : `请求失败（HTTP ${status}）`
  }
}

/**
 * 统一请求入口：拼接 /api/v1、附加 X-Data-Epoch 与 Idempotency-Key、解析统一响应与错误格式。
 */
export async function request<T = unknown>(method: string, path: string, options: RequestOptions = {}): Promise<ApiResponse<T>> {
  const upper = method.toUpperCase()
  const headers: Record<string, string> = { Accept: 'application/json', ...options.headers }
  let body: BodyInit | undefined
  if (options.rawBody !== undefined) {
    body = options.rawBody
  } else if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(options.body)
  }
  if (WRITE_METHODS.has(upper) && currentEpoch) headers['X-Data-Epoch'] = currentEpoch
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey

  const controller = new AbortController()
  let timedOut = false
  const timeoutMs = options.timeoutMs ?? 30_000
  const timer = timeoutMs > 0 ? setTimeout(() => ((timedOut = true), controller.abort()), timeoutMs) : null
  const onAbort = () => controller.abort()
  if (options.signal) {
    if (options.signal.aborted) controller.abort()
    else options.signal.addEventListener('abort', onAbort, { once: true })
  }

  let response: Response
  try {
    response = await fetch(buildUrl(path, options.query), {
      method: upper,
      headers,
      body,
      signal: controller.signal,
      credentials: 'same-origin',
      keepalive: options.keepalive,
    })
  } catch (error) {
    if (timedOut) {
      throw new ApiError({ status: 0, code: 'TIMEOUT', message: '请求超时，服务未在预期时间内响应', kind: 'timeout' })
    }
    if (controller.signal.aborted) {
      throw new ApiError({ status: 0, code: 'ABORTED', message: '请求已取消', kind: 'aborted' })
    }
    throw new ApiError({
      status: 0,
      code: 'NETWORK_ERROR',
      message: '无法连接本地服务，请确认程序仍在运行',
      details: { cause: String(error) },
      kind: 'network',
    })
  } finally {
    if (timer) clearTimeout(timer)
    options.signal?.removeEventListener('abort', onAbort)
  }

  if (response.status === 204) {
    return { data: null as T, meta: {}, status: 204 }
  }

  const text = await response.text().catch(() => '')
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      if (response.ok) {
        throw new ApiError({
          status: response.status,
          code: 'INVALID_RESPONSE',
          message: '服务返回了无法解析的内容',
          kind: 'parse',
        })
      }
    }
  }

  if (!response.ok) {
    throw errorFromPayload(response.status, payload, response.headers)
  }

  const envelope = (payload ?? {}) as Record<string, unknown>
  const hasEnvelope = payload !== null && typeof payload === 'object' && !Array.isArray(payload) && 'data' in envelope
  return {
    data: (hasEnvelope ? envelope.data : payload) as T,
    meta: (hasEnvelope && envelope.meta && typeof envelope.meta === 'object' ? envelope.meta : {}) as ApiMeta,
    status: response.status,
  }
}

export interface UploadOptions {
  headers?: Record<string, string>
  idempotencyKey?: string
  onProgress?: (loaded: number, total: number) => void
  signal?: AbortSignal
}

/**
 * multipart 上传。fetch 不能报告上传进度，因此使用 XMLHttpRequest。
 */
export function uploadForm<T = unknown>(path: string, form: FormData, options: UploadOptions = {}): Promise<ApiResponse<T>> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', buildUrl(path))
    xhr.setRequestHeader('Accept', 'application/json')
    if (currentEpoch) xhr.setRequestHeader('X-Data-Epoch', currentEpoch)
    if (options.idempotencyKey) xhr.setRequestHeader('Idempotency-Key', options.idempotencyKey)
    for (const [key, value] of Object.entries(options.headers ?? {})) xhr.setRequestHeader(key, value)

    const onAbort = () => xhr.abort()
    if (options.signal) {
      if (options.signal.aborted) {
        reject(new ApiError({ status: 0, code: 'ABORTED', message: '上传已取消', kind: 'aborted' }))
        return
      }
      options.signal.addEventListener('abort', onAbort, { once: true })
    }
    const cleanup = () => options.signal?.removeEventListener('abort', onAbort)

    xhr.upload.onprogress = (event) => {
      if (options.onProgress) options.onProgress(event.loaded, event.lengthComputable ? event.total : 0)
    }
    xhr.onerror = () => {
      cleanup()
      reject(new ApiError({ status: 0, code: 'NETWORK_ERROR', message: '上传中断：无法连接本地服务', kind: 'network' }))
    }
    xhr.onabort = () => {
      cleanup()
      reject(new ApiError({ status: 0, code: 'ABORTED', message: '上传已取消', kind: 'aborted' }))
    }
    xhr.onload = () => {
      cleanup()
      let payload: unknown = null
      if (xhr.responseText) {
        try {
          payload = JSON.parse(xhr.responseText)
        } catch {
          payload = null
        }
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(errorFromPayload(xhr.status, payload))
        return
      }
      const envelope = (payload ?? {}) as Record<string, unknown>
      const hasEnvelope = payload !== null && typeof payload === 'object' && 'data' in envelope
      resolve({
        data: (hasEnvelope ? envelope.data : payload) as T,
        meta: (hasEnvelope && envelope.meta && typeof envelope.meta === 'object' ? envelope.meta : {}) as ApiMeta,
        status: xhr.status,
      })
    }
    xhr.send(form)
  })
}
