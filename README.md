# OpenLJMDB

OpenLJMDB 是一个运行在本机的 Markdown 个人知识库，提供知识库管理、文档树、编辑与阅读、全文搜索、附件、历史版本、回收站、导入导出以及备份恢复。项目由 Vue 3 + TypeScript 前端和 Go + SQLite 后端组成，默认只监听本机地址，不依赖外部数据库或在线服务。

## 项目结构

```text
OpenLJMDB/
├── backend/       # Go 后端、数据库迁移、构建与后端依赖
├── web/            # Vue 3 + TypeScript 前端
├── docs/           # 产品、架构、接口和验收文档
├── deploy/         # 前后端部署模板与发布脚本
└── Makefile        # 根目录快捷构建入口
```

后端详细说明、接口约定和本地运行参数见 [backend/README.md](backend/README.md)。前端开发方式见 [web/README.md](web/README.md)，完整设计和验收资料见 [docs/README.md](docs/README.md)。

## 快速开始

先启动后端：

```bash
make build
./backend/dist/ljmdb --data-dir ./data --no-open
```

开发前端需要另开终端：

```bash
cd web
npm install
npm run dev
```

前端开发服务器通过 `/api` 访问本机后端。构建前端后，也可以让后端直接提供 `web/dist`：

```bash
cd web && npm run build
cd ..
./backend/dist/ljmdb --web-dir ./web/dist --no-open
```

默认后端地址为 `http://127.0.0.1:8080`，API 前缀为 `/api/v1`。数据目录与程序分离，升级程序不会覆盖用户数据。

## 常用命令

```bash
make build       # 构建后端
make test        # 运行后端测试
make check       # go vet 和 race 测试
make web-build   # 构建前端
make release     # 构建后端跨平台发行包
```

项目仍在持续完善中，发布边界和验证结果以 `docs/` 中的最新文档为准。
