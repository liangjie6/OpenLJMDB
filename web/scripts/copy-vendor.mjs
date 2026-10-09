#!/usr/bin/env node
/**
 * 将锁定版本的 Vditor 运行期资源复制到 public/vendor/vditor，
 * 保证编辑器、公式、Mermaid、代码高亮、图标、内容主题都由本程序提供，不依赖 CDN。
 *
 * 只复制首版承诺的能力（Lute、KaTeX、Mermaid、highlight.js、flowchart、i18n、图标、内容主题、表情图片）。
 * 不复制：
 *   - mathjax：首版公式引擎固定为 KaTeX；
 *   - plantuml：Vditor 的 PlantUML 渲染会请求外部 plantuml.com 服务器，违反离线约束；
 *   - echarts / graphviz / markmap / abcjs / smiles-drawer / wavedrom：不在首版语法契约内，
 *     前端会把这些代码块降级为源码显示（见 src/editor/vditorSetup.ts）。
 */
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'))
const expectedVersion = pkg.dependencies.vditor
const srcRoot = join(root, 'node_modules', 'vditor')
const destRoot = join(root, 'public', 'vendor', 'vditor')

if (!existsSync(join(srcRoot, 'package.json'))) {
  console.error('[vendor] 未找到 node_modules/vditor，请先执行 npm ci')
  process.exit(1)
}
const installed = JSON.parse(readFileSync(join(srcRoot, 'package.json'), 'utf8')).version
if (installed !== expectedVersion) {
  console.error(`[vendor] node_modules/vditor 版本 ${installed} 与锁定版本 ${expectedVersion} 不一致，请执行 npm ci`)
  process.exit(1)
}

const ENTRIES = [
  'dist/js/lute/lute.min.js',
  'dist/js/katex',
  'dist/js/mermaid/mermaid.min.js',
  'dist/js/highlight.js/highlight.min.js',
  'dist/js/highlight.js/third-languages.js',
  'dist/js/highlight.js/LICENSE',
  'dist/js/highlight.js/styles/github.min.css',
  'dist/js/highlight.js/styles/github-dark.min.css',
  'dist/js/flowchart.js/flowchart.min.js',
  'dist/js/i18n/zh_CN.js',
  'dist/js/i18n/en_US.js',
  'dist/js/icons/ant.js',
  'dist/js/icons/material.js',
  'dist/css/content-theme',
  'dist/images/emoji',
  'dist/images/img-loading.svg',
  'LICENSE',
]

const stampFile = join(destRoot, 'VENDOR.json')
if (existsSync(stampFile)) {
  try {
    const stamp = JSON.parse(readFileSync(stampFile, 'utf8'))
    if (stamp.version === expectedVersion && JSON.stringify(stamp.entries) === JSON.stringify(ENTRIES)) {
      const missing = ENTRIES.filter((entry) => !existsSync(join(destRoot, entry)))
      if (missing.length === 0) {
        console.log(`[vendor] vditor@${expectedVersion} 资源已就绪`)
        process.exit(0)
      }
    }
  } catch {
    // 重新复制
  }
}

rmSync(destRoot, { recursive: true, force: true })
mkdirSync(destRoot, { recursive: true })

let totalBytes = 0
const files = []
const walk = (path) => {
  const stats = statSync(path)
  if (stats.isDirectory()) {
    for (const name of readdirSync(path)) walk(join(path, name))
  } else {
    totalBytes += stats.size
    files.push(relative(destRoot, path))
  }
}

for (const entry of ENTRIES) {
  const from = join(srcRoot, entry)
  if (!existsSync(from)) {
    console.error(`[vendor] 缺少资源 ${entry}，Vditor 版本结构可能已变化`)
    process.exit(1)
  }
  const to = join(destRoot, entry)
  mkdirSync(dirname(to), { recursive: true })
  cpSync(from, to, { recursive: true })
  walk(to)
}

writeFileSync(
  stampFile,
  JSON.stringify({ name: 'vditor', version: expectedVersion, entries: ENTRIES, files: files.sort(), total_bytes: totalBytes }, null, 2),
)
console.log(`[vendor] 已复制 vditor@${expectedVersion} 运行期资源 ${files.length} 个文件，共 ${(totalBytes / 1024 / 1024).toFixed(1)} MiB`)
