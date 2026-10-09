# OpenLJMDB Web 前端

基于 Vue 3 + TypeScript + Vite 的 Markdown 知识库前端应用。

## 技术栈

- **框架**: Vue 3 (Composition API)
- **状态管理**: Pinia
- **路由**: Vue Router 4
- **编辑器**: CodeMirror 6
- **Markdown 渲染**: markdown-it
- **构建工具**: Vite
- **类型检查**: TypeScript

## 开发

```bash
# 安装依赖
npm install

# 启动开发服务器（需要后端在 localhost:8080 运行）
npm run dev

# 类型检查
npm run type-check

# 构建生产版本
npm run build

# 预览生产构建
npm run preview
```

## 项目结构

```
src/
├── api/              # API 客户端与类型定义
├── assets/           # 静态资源与全局样式
├── components/       # Vue 组件
│   ├── base/        # 基础 UI 组件
│   └── workspace/   # 工作区组件（编辑器、文档树等）
├── composables/      # 组合式函数
├── editor/           # CodeMirror 编辑器配置与扩展
├── router/           # 路由配置
├── services/         # 业务逻辑服务
├── stores/           # Pinia 状态管理
├── utils/            # 工具函数
└── views/            # 页面级组件
```

## 特性

- 📝 实时保存与冲突检测
- 🌳 文档树管理（拖拽排序）
- 🔍 全文搜索与高亮
- 📎 附件上传与管理
- 📚 历史版本与恢复
- 🗑️ 回收站
- 💾 完整备份与恢复
- 📥 Markdown 导入导出
- 🌓 浅色/深色主题
- 📱 响应式设计

## 浏览器支持

现代浏览器（Chrome、Firefox、Safari、Edge 最新版本）。需要支持：
- ES2020+
- IndexedDB（本地草稿）
- Web Workers（Markdown 渲染）
