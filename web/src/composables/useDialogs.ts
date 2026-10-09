import { reactive } from 'vue'

export interface ChoiceAction {
  key: string
  label: string
  kind?: 'primary' | 'danger' | 'default'
  /** 默认聚焦（危险操作应把焦点放在取消或安全动作上） */
  autofocus?: boolean
}

export interface ChoiceOptions {
  title: string
  message: string
  details?: string[]
  actions: ChoiceAction[]
  /** Esc / 关闭按钮对应的动作 */
  cancelKey: string
  /** 需要勾选确认后才能执行的动作 */
  acknowledge?: { label: string; actionKeys: string[] }
  tone?: 'info' | 'warning' | 'danger'
}

interface DialogState {
  open: boolean
  options: ChoiceOptions | null
  resolve: ((key: string) => void) | null
}

export const dialogState = reactive<DialogState>({ open: false, options: null, resolve: null })

const queue: Array<{ options: ChoiceOptions; resolve: (key: string) => void }> = []

function showNext() {
  if (dialogState.open || queue.length === 0) return
  const next = queue.shift()!
  dialogState.options = next.options
  dialogState.resolve = next.resolve
  dialogState.open = true
}

export function resolveDialog(key: string) {
  const resolve = dialogState.resolve
  dialogState.open = false
  dialogState.resolve = null
  resolve?.(key)
  setTimeout(showNext, 0)
}

/** 多选项对话框：返回所选动作 key */
export function choose(options: ChoiceOptions): Promise<string> {
  return new Promise((resolve) => {
    queue.push({ options, resolve })
    showNext()
  })
}

export interface ConfirmOptions {
  title: string
  message: string
  details?: string[]
  confirmText?: string
  cancelText?: string
  danger?: boolean
  acknowledge?: string
}

/** 确认对话框：危险操作默认聚焦“取消” */
export async function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  const key = await choose({
    title: options.title,
    message: options.message,
    details: options.details,
    cancelKey: 'cancel',
    tone: options.danger ? 'danger' : 'info',
    acknowledge: options.acknowledge ? { label: options.acknowledge, actionKeys: ['confirm'] } : undefined,
    actions: [
      { key: 'cancel', label: options.cancelText ?? '取消', autofocus: !!options.danger },
      {
        key: 'confirm',
        label: options.confirmText ?? '确定',
        kind: options.danger ? 'danger' : 'primary',
        autofocus: !options.danger,
      },
    ],
  })
  return key === 'confirm'
}
