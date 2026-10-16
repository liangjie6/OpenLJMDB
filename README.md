<div align="center">

<img src="web/public/logo.png" alt="OpenLJMDB 项目图标" width="160" height="160" />

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# OpenLJMDB —— 本地 Markdown 个人知识库

> 根据笔记完整内容自动推荐知识库，显示匹配概率并支持逐篇确认，让 Markdown 笔记的整理、编辑、检索和备份更方便。

**简体中文** | [English](README.en.md)

[简介](#简介) • [自动分类](#导入自动分类) • [功能特性](#功能亮点) • [项目截图](#项目截图) • [快速开始](#快速开始) • [运行配置](#运行配置) • [分类服务配置](#配置自动分类) • [开发与验证](#开发与验证) • [数据备份](#数据与备份) • [常见问题](#常见问题) • [参与共建](#更多文档与贡献) • [开源协议](#开源协议)

</div>

## 简介

OpenLJMDB 将零散笔记整理为知识库和树形文档目录，提供 Markdown 编辑与阅读、全文搜索、附件、历史版本、回收站、导入导出和备份恢复。基于 Vue 3 + TypeScript + Go + SQLite，数据保存在本机，无需外部数据库；日常编辑、阅读和检索可离线使用，导入自动分类按需连接外部分类服务。

## 导入自动分类

批量导入 Markdown 时，OpenLJMDB 根据每篇笔记的**完整正文、标题以及现有知识库的名称和描述**，为文档推荐最适合的知识库，减少逐篇整理的操作。

```text
选择 Markdown 文件 → 导入预检 → 自动推荐知识库与匹配概率
                                      ↓
                           逐篇确认或调整位置 → 确认导入
```

- **按内容分类**：使用已有知识库作为候选分类，默认选择模型返回概率最高的知识库。建议先创建知识库，并填写清晰的主题描述。
- **批量处理**：支持同时选择多篇 `.md` / `.markdown` 文件，在确认页查看分类进度和每篇的匹配概率。
- **位置可调整**：推荐位置为知识库根目录；确认前可以逐篇修改知识库和父文档，也可以将一个位置应用到全部文档。
- **过程可控制**：支持停止和重新分类；服务未配置、请求失败或超时时，仍可手动选择位置并导入。
- **可选开启**：默认关闭，在“设置 → 导入 → 导入自动分类”中开启并保存。配置方法见[下文](#配置自动分类)。

ZIP 资料包保留父子结构，统一选择整包导入位置，不参与自动分类。分类建议没有最低概率门槛，导入前请检查推荐位置；它不会自动整理或移动已有笔记。

> 开启分类后，待导入文档的标题、完整 Markdown 正文，以及现有知识库的 ID、名称和描述会发送到配置的分类服务。API Key 仅由后端环境变量读取，不进入前端、数据库或应用备份。

## 功能亮点

| 功能 | 说明 |
| --- | --- |
| 知识库与文档树 | 多知识库、知识库描述、层级文档、新建子文档、重命名和删除 |
| Markdown 编辑与阅读 | Vditor 即时渲染、Markdown 源码编辑、独立阅读视图、大纲导航、代码高亮、KaTeX 公式和 Mermaid 图表 |
| 源码查找与替换 | 按原文查找、区分大小写、循环定位、单次或全部替换，以及撤销和重做 |
| 自动保存与草稿 | 保存状态提示、IndexedDB 本地草稿、版本冲突检测与人工合并 |
| 全文搜索 | 按知识库检索标题和正文，支持中文子串回退、命中高亮与导航 |
| 图片与附件 | 上传、粘贴或拖入图片和附件，管理引用及未引用附件清理 |
| 历史与回收站 | 查看与恢复历史版本，删除内容进入回收站并支持恢复 |
| 导入与自动分类 | Markdown 批量预检、逐篇分类与位置确认，ZIP 结构导入 |
| 内容导出 | Markdown、HTML 和包含资源的 ZIP；图片导出支持多种背景、单张 PNG 长图和多页 PNG 图片 ZIP |
| 完整备份与恢复 | 保留文档、附件、历史、回收站和设置，恢复前校验并保留副本 |
| 外观与布局 | 浅色／深色／跟随系统主题，可折叠侧边栏和响应式布局 |

文档右上角“文档操作 → 导出为图片…”可选择背景，并预览标题和正文。选择单张长图可下载 PNG；选择多页图片可按页面比例分页，下载包含顺序编号 PNG 的 ZIP。编辑时使用打开导出窗口时的当前内容。超长文档请使用多页图片；无法载入的图片会以提示框标注。

### 编辑与查找替换

- 文档顶部提供“阅读”“编辑”“源编辑”。“编辑”使用 Vditor 即时渲染；“源编辑”直接操作 Markdown 原文，适合调整代码块、链接和 Markdown 标记。
- 在编辑状态点击“查找与替换”，或在编辑区域按 `Ctrl/Cmd+F` / `Ctrl/Cmd+H`，会进入源码模式并打开查找面板。
- 查找按字面内容匹配，支持区分大小写；`Enter` 跳到下一处，`Shift+Enter` 跳到上一处，`Esc` 关闭面板。替换内容为空时删除匹配文本。
- 源码模式提供撤销和重做；一次“全部替换”可整体撤销。两种编辑模式共用自动保存、草稿和冲突保护。

## 项目截图

以下截图于 2026 年 10 月 10 日从实际运行的应用捕获，使用独立的演示知识库；后续增加的源码编辑和图片导出未在这些截图中展示。自动分类截图中的推荐结果由本地模拟分类服务生成，用于展示操作流程，不代表真实模型评测结果。截图说明见 [docs/images/README.md](docs/images/README.md)。

### 知识库工作区

文档树、Markdown 阅读视图和大纲在同一工作区中呈现。

![OpenLJMDB 知识库工作区：文档树、Markdown 阅读与大纲](docs/images/workspace.png)

### Markdown 编辑

Vditor 即时渲染编辑器提供格式工具栏、代码高亮和保存状态提示。

![OpenLJMDB Markdown 编辑：Vditor 即时渲染、格式工具栏与保存状态](docs/images/workspace-edit.png)

### Markdown 导入自动分类

批量导入时查看推荐知识库与匹配概率，并在提交前调整每篇笔记的位置。

![OpenLJMDB Markdown 导入自动分类：推荐知识库、匹配概率与逐篇位置确认](docs/images/auto-classification.png)

## 快速开始

### 环境要求

- **Go 1.27.1 或更新版本**，与 [backend/go.mod](backend/go.mod) 一致。
- **Node.js 24 LTS（推荐）或 22.x（至少 22.12）**，以及 npm。
- **Git**；下方快捷命令需要 `make`。当前浏览器建议使用新版 Chrome、Edge、Firefox 或 Safari。

首次安装前后端依赖需要网络；构建后基础知识库功能可离线运行。自动分类需要单独配置可用的分类服务。

### 从源码构建并运行

在终端执行：

```bash
git clone https://github.com/liangjie6/OpenLJMDB.git
cd OpenLJMDB

npm --prefix web ci
make build
make web-build

./backend/dist/ljmdb --host 127.0.0.1 --port 8080 --data-dir ./data --web-dir ./web/dist --no-open
```

打开 **[http://127.0.0.1:8080](http://127.0.0.1:8080)**。后端同时提供前端页面和 `/api/v1` 接口；数据保存在项目根目录的 `data/`，与构建产物分离。使用 Ctrl+C 正常退出。

如果 8080 已被其他程序占用，将 `--port 8080` 改为可用端口（例如 `--port 8081`），并访问对应地址。无需配置分类服务即可开始使用。

没有 `make` 时，可将两个构建命令替换为：

```bash
go -C backend build -trimpath -o dist/ljmdb ./cmd/ljmdb
npm --prefix web run build
```

Windows 可将输出文件改为 `dist/ljmdb.exe`，然后运行 `./backend/dist/ljmdb.exe` 并传入相同参数。

### 首次使用

1. 新建知识库，例如“编程开发”“研究资料”“生活记录”，并填写各自的主题描述。
2. 新建文档开始编写，或进入“导入”选择 Markdown 文件或 ZIP 资料包。
3. 如需自动分类，配置后端分类服务，再在设置中开启“导入自动分类”。
4. 在导入确认页检查文档位置，确认后导入；定期通过“备份与恢复”创建完整备份。

## 运行配置

### 环境变量与 .env

可以通过进程环境变量或 `.env` 配置后端。从项目根目录运行时：

```bash
cp .env.example .env
```

编辑 `.env` 填写所需配置；无需自动分类时，可以删除或留空两个 `LJMDB_DECISION_*` 配置项。`.env` 已被 Git 忽略，请将密钥保留在本机。

| 环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `LJMDB_HOST` | `127.0.0.1` | 监听 IP，只允许回环地址，如 `127.0.0.1` 或 `::1` |
| `LJMDB_PORT` | `8080` | 后端 HTTP 端口 |
| `LJMDB_DATA_DIR` | 系统用户数据目录 | 数据存储路径 |
| `LJMDB_DECISION_BASE_URL` | 未配置 | 分类服务站点根地址，不带 `/v1` |
| `LJMDB_DECISION_API_KEY` | 未配置 | 分类服务密钥 |

程序依次查找当前工作目录、可执行文件所在目录、可执行文件上一级和上两级目录中的 `.env`。已有非空环境变量优先，较早读到的非空值不会被后续文件覆盖；命令行参数优先于对应环境变量。支持 `KEY=value`、引号包裹的值、`export KEY=value` 和整行 `#` 注释；不执行 shell 脚本或变量展开。

修改配置后需要重启后端。使用相对数据路径时，路径相对于进程工作目录解析；长期运行建议指定绝对路径。

### 命令行参数

| 参数 | 说明 |
| --- | --- |
| `--host <IP>` | 回环监听 IP，默认 `127.0.0.1` |
| `--port <端口>` | HTTP 端口，默认 `8080` |
| `--data-dir <路径>` | 指定数据目录 |
| `--web-dir <路径>` | 已构建的前端目录，需包含 `index.html`；省略时仅提供 API |
| `--portable` | 使用可执行文件同级的 `data/`，与 `--data-dir` / `LJMDB_DATA_DIR` 互斥 |
| `--no-open` | 不自动打开浏览器 |
| `--version` | 输出版本并退出 |

后端直接监听仅支持回环 IP。需要通过 Nginx 提供访问入口时，参照 [部署说明](deploy/README.md) 配置；当前应用没有登录鉴权，访问入口应限于可信环境。

## 配置自动分类

当前分类实现固定使用 **`decision-model-preview`**，请求专用的 **`POST /v1/systemone`** 接口。服务必须支持该接口的分类请求和响应格式；普通 Chat Completions 接口不能直接替代。

| 环境变量 | 含义 |
| --- | --- |
| `LJMDB_DECISION_BASE_URL` | 分类服务站点根地址，例如 `https://your-classifier.example.com`；不带 `/v1`，没有默认服务地址 |
| `LJMDB_DECISION_API_KEY` | 分类服务 API Key，仅提供给后端进程 |

### 直接运行后端

先停止已运行的后端，在同一 Bash 终端配置环境变量后重新启动：

```bash
export LJMDB_DECISION_BASE_URL='https://your-classifier.example.com'
read -r -s -p '分类服务 API Key: ' LJMDB_DECISION_API_KEY
printf '\n'
export LJMDB_DECISION_API_KEY

./backend/dist/ljmdb --data-dir ./data --web-dir ./web/dist --no-open
```

将示例域名替换为自己的服务地址。隐藏输入的密钥不会直接写入命令历史。启动后，进入“设置 → 导入”，开启“导入自动分类”并保存，再导入 Markdown 文件。

### 已有 systemd 部署

```bash
sudo bash deploy/configure-classifier.sh https://your-classifier.example.com
```

脚本隐藏读取密钥，将环境配置写入仅 root 可读的 `/etc/openljmdb/classifier.env`，通过 systemd 注入后端并重启服务。部署前提和更新步骤见 [deploy/README.md](deploy/README.md)。

## 开发与验证

前后端开发时分别启动两个终端，命令均在项目根目录执行。

终端一，启动后端：

```bash
make build
./backend/dist/ljmdb --host 127.0.0.1 --port 8080 --data-dir ./data --no-open
```

终端二，启动前端：

```bash
npm --prefix web ci
npm --prefix web run prebuild
npm --prefix web run dev
```

`prebuild` 将 Vditor 依赖复制到前端本地资源目录，首次开发启动前需要执行；`npm run build` 会自动执行此步骤。

访问 **[http://localhost:5173](http://localhost:5173)**；Vite 将 `/api` 代理到 `http://localhost:8080`。生产运行使用上面的 `--web-dir` 方式保持页面与 API 同源。

若开发后端使用其他端口，请同步修改 [web/vite.config.ts](web/vite.config.ts) 的 `server.proxy['/api'].target`。

| 命令 | 用途 |
| --- | --- |
| `make build` | 构建 Go 后端到 `backend/dist/ljmdb` |
| `make web-build` | 类型检查并构建前端到 `web/dist` |
| `make test` | 运行后端测试 |
| `make check` | 运行 `go vet` 和后端 race 测试 |
| `npm --prefix web run type-check` | 前端 TypeScript 检查 |
| `npm --prefix web test` | 运行前端 Vitest 测试 |
| `make release` | 构建 Linux、macOS、Windows 的 amd64 / arm64 后端二进制、第三方声明和 SHA256 校验和 |

`make release` 将产物写入 `backend/dist/`，不包含前端；前端需要单独构建并通过 `--web-dir` 提供。跨平台编译不代表各目标平台已完成运行验收。

### 更新已有部署

更新前先通过应用创建完整备份，在源码目录拉取更新，再构建和验证：

```bash
git pull --ff-only
npm --prefix web ci
make check
npm --prefix web test
make build
make web-build
```

直接运行的实例应先正常停止旧进程，再使用原有数据目录和启动参数运行新版本。已有 Nginx / systemd 部署可用 `sudo bash deploy/update-web.sh` 更新前端；同时更新后端时先构建 `deploy/bin/ljmdb.next`，再运行 `sudo bash deploy/update-app.sh`。完整前提、构建命令和回退行为见 [deploy/README.md](deploy/README.md)。

更新脚本面向仓库默认 HTTP 部署模板。自定义 HTTPS、域名、端口或静态目录时，应先将脚本的校验入口和发布路径调整为实际配置。

## 项目结构

```text
OpenLJMDB/
├── backend/               # Go API、SQLite、迁移、分类服务调用和后端测试
│   ├── cmd/ljmdb/         # 程序入口
│   └── internal/          # 配置与业务实现
├── web/                   # Vue 3 + TypeScript + Vite + Vditor
│   ├── src/               # 页面、组件、状态管理与 API 客户端
│   └── public/            # 图标、字体和本地资源
├── docs/                  # 产品、架构、接口、验收和截图
├── deploy/                # Nginx / systemd 模板、更新与分类配置脚本
├── .env.example           # 后端环境变量示例，不包含真实密钥
├── README.md              # 中文说明
├── README.en.md           # English documentation
└── Makefile               # 根目录构建与检查入口
```

后端使用 SQLite WAL + FTS5 保存与检索内容，以 Markdown 为正文来源，通过 Lute 渲染；前端使用 Pinia、Vue Router 和 Vditor，编辑器及阅读所需资源随前端构建提供。

## 数据与备份

- `--data-dir <路径>` 指定数据目录；省略时使用系统用户数据目录，实际路径会在启动输出中显示。
- `--portable` 使用可执行文件同级的 `data/`，不能与 `--data-dir` 或 `LJMDB_DATA_DIR` 同时使用。
- 默认监听 `127.0.0.1:8080`；可通过 `--port` 调整端口。更多参数见 [backend/README.md](backend/README.md)。

```text
<data-dir>/
├── knowledge.db           # 文档、结构、历史、回收站、设置、索引与任务
├── uploads/               # 附件文件
├── backups/               # 完整备份和恢复前副本
├── tmp/                   # 导入预览与任务暂存
├── runtime/               # 恢复协调信息
└── logs/                  # 应用日志
```

内容导出用于交换和离线阅读；完整备份保留原始 ID、历史、回收站和持久设置。浏览器中的 IndexedDB 草稿不包含在后端备份内。程序运行时请使用应用内备份，避免直接复制活跃的 SQLite 数据库。

未指定数据目录时，Linux 使用 `$XDG_DATA_HOME/LJMDB`（未设置时为 `~/.local/share/LJMDB`），macOS 使用 `~/Library/Application Support/LJMDB`，Windows 优先使用 `%LOCALAPPDATA%/LJMDB`。启动输出会给出最终绝对路径。

## 常见问题

| 问题 | 处理方式 |
| --- | --- |
| 启动提示端口不可用 | 使用 `--port` 选择空闲端口；开发模式同时调整 Vite API 代理 |
| 后端启动了，但没有应用页面 | 先构建前端，再传入 `--web-dir ./web/dist`；仅启动 API 时可访问 `/api/v1/health` |
| 开发时编辑器或公式资源加载失败 | 在 `web/` 执行 `npm ci` 和 `npm run prebuild`，然后重启 Vite |
| `.env` 修改后没有生效 | 重启后端，核对工作目录，以及命令行参数和已有环境变量是否覆盖了配置 |
| 自动分类不可用 | 核对站点根地址、密钥和 `/v1/systemone` 支持，重启后端，并确认设置中的开关已开启保存；ZIP 不参与分类 |
| 出现版本冲突或本地草稿提示 | 保留草稿，通过界面对比与人工合并，确认内容后再保存 |
| 恢复备份后旧页面无法写入 | 重新加载页面，并按提示确认本地草稿；恢复会更新数据世代以阻止旧页面覆盖内容 |
| 导出图片过长或失败 | 切换为多页图片，等待文内图片加载，并检查浏览器内存及外部图片是否允许跨域读取 |
| 无法直接用 `0.0.0.0` 监听 | 当前后端仅支持回环地址；使用部署模板配置 Nginx 入口 |

排查本机部署可查看 `journalctl -u ljmdb-backend -n 50`，直接运行可查看终端及数据目录中的 `logs/server.log`。提交日志前请移除密钥和个人内容。

## 更多文档与贡献

- [后端运行、参数与 API 约定](backend/README.md)
- [前端开发说明](web/README.md)
- [本机部署、更新与分类服务配置](deploy/README.md)
- [产品、架构、数据模型和验收文档](docs/README.md)
- [后端实际验收记录](docs/07-后端验收记录.md)与[问题修复验收](docs/08-问题修复验收.md)
- [后端第三方依赖声明](backend/THIRD_PARTY_NOTICES.md)

项目定位为本机、单用户知识库，当前未提供登录鉴权，文档树暂不支持拖拽移动或重排。设计文档中的规划项与已交付功能可能存在差异，请以当前实现和验收记录为准。

欢迎通过 [Issues](https://github.com/liangjie6/OpenLJMDB/issues) 反馈问题，或提交 Pull Request。报告问题时请附上系统、Go / Node.js 版本、复现步骤及经过脱敏的日志；修改代码后运行对应的前后端检查。

## 开源协议

本项目采用 [MIT License](LICENSE)。第三方依赖和资源遵循各自的许可证，详见[后端第三方依赖声明](backend/THIRD_PARTY_NOTICES.md)、[依赖许可证](backend/licenses/README.md)及[字体授权说明](web/public/fonts/jetbrains-maple-mono/README.md)。
