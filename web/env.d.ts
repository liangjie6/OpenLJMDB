/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 开发时是否使用内存 Mock 后端（仅 `vite --mode mock`） */
  readonly VITE_MOCK?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
