import type { Limits } from '@/api/types'

/** 02 文档第 9 节的初始保护值；服务端返回实际生效限制时以服务端为准 */
export const DEFAULT_LIMITS: Limits = {
  title_max_chars: 200,
  markdown_max_bytes: 10 * 1024 * 1024,
  upload_max_bytes: 50 * 1024 * 1024,
  zip_max_bytes: 500 * 1024 * 1024,
  zip_max_expanded_bytes: 1024 * 1024 * 1024,
  zip_max_entries: 10_000,
  tree_max_depth: 32,
  search_query_max_chars: 200,
  page_size_max: 100,
}

export function mergeLimits(...sources: Array<Partial<Limits> | null | undefined>): Limits {
  const result: Limits = { ...DEFAULT_LIMITS }
  for (const src of sources) {
    if (!src) continue
    for (const [key, value] of Object.entries(src)) {
      if (typeof value === 'number' && value > 0) (result as unknown as Record<string, number>)[key] = value
    }
  }
  return result
}
