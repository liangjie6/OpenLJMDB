# OpenLJMDB Web 前端

基于 Vue 3 + TypeScript + Vite 的 Markdown 知识库前端应用。

## 技术栈

- **框架**: Vue 3 (Composition API)
- **状态管理**: Pinia
- **路由**: Vue Router 4
- **编辑器**: Vditor 3.11.3（IR 即时渲染模式）
- **Markdown 渲染**: Vditor / Lute，阅读内容通过 DOMPurify 净化
- **公式与图表**: KaTeX、Mermaid，运行资源随项目本地提供
- **构建工具**: Vite
- **类型检查**: TypeScript

## 开发

推荐使用 Node.js 24 LTS，也可使用 Node.js 22.x（22.12.0 或更新版本）。以下命令在 `web/` 目录执行；前端开发服务器默认位于 `http://localhost:5173`，通过 `/api` 代理访问 `http://localhost:8080` 的后端。

```bash
# 安装依赖
npm ci

# 首次开发或升级依赖后，生成 Vditor 本地运行资源
npm run prebuild

# 启动开发服务器（需要后端在 localhost:8080 运行）
npm run dev

# 类型检查
npm run type-check

# 运行前端测试
npm test

# 构建生产版本（自动生成本地运行资源并检查类型）
npm run build

# 预览静态生产构建
npm run preview
```

`public/vendor/vditor/` 是生成目录，不纳入 Git；`npm run dev` 不会自动生成它。完整生产应用请从项目根目录使用后端的 `--web-dir ./web/dist` 参数提供页面与 API，详见[项目 README](../README.md)。

## 项目结构

```
src/
├── api/              # API 客户端与类型定义
├── components/       # Vue 组件
│   ├── base/        # 基础 UI 组件
│   └── workspace/   # 工作区组件（编辑器、文档树等）
├── composables/      # 组合式函数
├── editor/           # Vditor 配置、代码块、正文目录与搜索高亮
├── router/           # 路由配置
├── services/         # 业务逻辑服务
├── stores/           # Pinia 状态管理
├── styles/           # 全局与正文样式
├── utils/            # 工具函数
└── views/            # 页面级组件
```

## 特性

- 🤖 Markdown 导入自动分类：根据完整笔记内容推荐知识库，显示匹配概率，确认前可逐篇修改
- 📝 Vditor 即时渲染编辑、自动保存、IndexedDB 本地草稿与冲突检测
- 🌳 层级文档树、新建与重命名、键盘导航
- 🧮 公式、Mermaid 图表与代码语法高亮
- 🔍 全文搜索与高亮
- 📎 附件上传与管理
- 📚 历史版本与恢复
- 🗑️ 回收站
- 💾 完整备份与恢复
- 📥 多篇 Markdown / ZIP 导入、Markdown 与离线 HTML 资源包导出
- 🌓 浅色/深色主题
- 📱 响应式设计

导入自动分类默认关闭，需要后端配置分类服务并在“设置 → 导入”开启、保存。开启后，笔记标题、完整 Markdown 正文及知识库名称、描述会发送至分类服务；自动选择概率最高的知识库，支持停止、重试和手动调整。ZIP 保留父子结构，不参与自动分类。配置步骤见[部署说明](../deploy/README.md#导入自动分类)。

## 浏览器支持

现代浏览器（Chrome、Firefox、Safari、Edge 最新版本）。需要支持：

- ES2020+
- IndexedDB（本地草稿）

当前界面提供简体中文，主题支持浅色、深色与跟随系统。
