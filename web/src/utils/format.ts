import { useSettingsStore } from '@/stores/settings'

function toDate(ms: number | null | undefined): Date | null {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms <= 0) return null
  return new Date(ms)
}

function timeParts(date: Date) {
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone: useSettingsStore().timezone,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
    hourCycle: 'h23', timeZoneName: 'longOffset',
  })
  return Object.fromEntries(formatter.formatToParts(date).map(({ type, value }) => [type, value])) as Record<string, string>
}

/** 设置时区的“时:分:秒” */
export function formatClock(ms: number | null | undefined): string {
  const d = toDate(ms)
  if (!d) return '—'
  const p = timeParts(d)
  return `${p.hour}:${p.minute}:${p.second}`
}

/** 设置时区日期时间；同一天只显示时间 */
export function formatDateTime(ms: number | null | undefined): string {
  const d = toDate(ms)
  if (!d) return '—'
  const p = timeParts(d)
  const now = timeParts(new Date())
  const day = `${p.year}-${p.month}-${p.day}`
  const today = `${now.year}-${now.month}-${now.day}`
  const time = `${p.hour}:${p.minute}`
  if (day === today) return `今天 ${time}`
  const yesterday = new Date(`${today}T00:00:00Z`)
  yesterday.setUTCDate(yesterday.getUTCDate() - 1)
  if (day === yesterday.toISOString().slice(0, 10)) return `昨天 ${time}`
  const date = p.year === now.year ? `${Number(p.month)}月${Number(p.day)}日` : day
  return `${date} ${time}`
}

/** 完整可辨识时间（含秒和时区偏移），用于备份、错误详情和技术信息 */
export function formatFullTime(ms: number | null | undefined): string {
  const d = toDate(ms)
  if (!d) return '—'
  const p = timeParts(d)
  const zone = p.timeZoneName === 'GMT' ? 'UTC+00:00' : p.timeZoneName!.replace('GMT', 'UTC')
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}:${p.second} ${zone}`
}

export function formatRelative(ms: number | null | undefined, now = Date.now()): string {
  const d = toDate(ms)
  if (!d) return '—'
  const diff = Math.round((now - d.getTime()) / 1000)
  if (diff < 10) return '刚刚'
  if (diff < 60) return `${diff} 秒前`
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return formatDateTime(ms)
}

export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

export function formatCount(n: number | null | undefined, unit = ''): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return '—'
  return `${n.toLocaleString('zh-CN')}${unit}`
}
