# OpenLJMDB 后端

本地 Markdown 知识库的 Go 后端，使用 SQLite WAL、FTS5 和 Lute。基础功能运行时无需外部数据库或互联网，可选的导入自动分类需要连接分类服务。完整项目和中英文使用说明见[根 README](../README.md)，前端位于 `web/`。

## 启动

需要 Go 1.27.1 或更新版本构建。开发机首次下载依赖需要网络；构建后的程序离线运行。

```bash
cd backend
go mod download
make build
./dist/ljmdb --data-dir ./data --no-open
```

默认地址为 `http://127.0.0.1:8080`，接口前缀 `/api/v1`。启动输出显示实际数据目录。省略 `--data-dir` 时使用系统用户数据目录；`--portable` 使用可执行文件同级的 `data` 目录。参数优先于 `LJMDB_HOST`、`LJMDB_PORT`、`LJMDB_DATA_DIR` 环境变量。`--portable` 与数据目录参数或环境变量冲突时拒绝启动。非回环地址拒绝启动。

```bash
./dist/ljmdb --port 8081 --no-open
./dist/ljmdb --version
```

前端完成构建后，在项目根目录可用 `./backend/dist/ljmdb --web-dir ./web/dist --no-open` 从同一个回环服务提供页面、SPA 路由和 API。后端还提供 `App.FrontendFS`，可用 `embed.FS` 接入单二进制；当前构建脚本不嵌入前端资源。

程序以 OS 文件锁独占数据目录，端口占用、目录不可写、数据库损坏及不支持的较新 schema 均会停止启动，不会改用另一套空数据库。使用 Ctrl+C / SIGTERM 正常退出。不要在程序运行时直接复制活跃数据库作为备份。

## 接口约定

先请求 `GET /api/v1/health` 取得 `data.instance_id` 与 `data.data_epoch`。**所有写请求必须携带 `X-Data-Epoch`**；保存带 `expected_revision`，文档树变更带 `expected_tree_revision`。恢复备份后世代改变，旧请求收到 `409 DATA_EPOCH_CHANGED`。已受理的恢复请求以同幂等键、同正文重试时，即使头为旧世代也会返回原任务，不启动新恢复。保留本地草稿并重新加载，不能自动更新请求头重发旧正文。

```bash
curl http://127.0.0.1:8080/api/v1/health
curl -X POST http://127.0.0.1:8080/api/v1/knowledge-bases \
  -H 'Content-Type: application/json' \
  -H 'X-Data-Epoch: 替换为健康接口返回的data_epoch' \
  -H 'Idempotency-Key: create-library-001' \
  -d '{"name":"学习笔记","description":"个人资料"}'
```

成功响应为 `{"data": ..., "meta": ...}`；列表 `meta` 包含分页与总数。错误为 `{"error":{"code":"...","message":"中文说明","details":{}},"request_id":"..."}`。业务时间为 UTC Unix 毫秒，导入导出及备份清单中的 `created_at` 使用 UTC RFC3339 字符串。标题同名允许；ID 均为 UUID。异步任务的 `202` 只表示受理，必须通过任务接口确认最终状态。

浏览器必须同源访问。开发前端请通过开发服务器代理 `/api`，并让请求 Host 与 Origin 都匹配后端地址；后端不开放通配 CORS。部署时应由同一个回环服务提供页面及 API。前端自动保存、IndexedDB 草稿、冲突比较与搜索导航由前端模型实现，后端提供相应协议与错误信息。

详见 [接口交接](../docs/06-后端接口交接.md) 和 [验收记录](../docs/07-后端验收记录.md)。原始需求文档保留作为设计依据。

## 数据与维护

```text
<data-dir>/knowledge.db                 正文、结构、历史、回收站、设置、索引与任务
<data-dir>/uploads/<prefix>/<uuid>/blob 不可变附件
<data-dir>/backups/                     完整备份及恢复前副本
<data-dir>/tmp/                         隔离导入预览、任务暂存
<data-dir>/runtime/restore-state.json    跨数据库替换的恢复协调记录
<data-dir>/runtime.lock                 OS 独占锁
<data-dir>/logs/server.log              无正文的容量受限日志
```

迁移 SQL 嵌入二进制，位于 `internal/backend/migrations`。HTML、检索文本与 FTS 均为可重建派生数据；Markdown 是正文来源。附件清理必须先预览、确认，再由可重试任务执行；当前正文、历史和回收站引用均阻止清理。内容 ZIP 用于交换，完整备份保留原 ID、历史、回收站和持久设置。浏览器草稿不包含在后端备份内。

HTML 资源 ZIP 包含本地 KaTeX、Mermaid、字体及样式，用于导出阅读；它们不是前端应用代码。授权和锁定版本见 `internal/backend/exportassets/README.txt` 和 `THIRD_PARTY_NOTICES.md`。

## 验证与构建

```bash
cd backend
go test ./...
go vet ./...
go test -race ./...
python3 scripts/smoke.py dist/ljmdb
bash scripts/build.sh
```

构建脚本产生 Linux、macOS、Windows 的 amd64 / arm64 产物与 SHA256SUMS。跨平台编译不代表目标平台运行验收；当前执行环境和验证范围在验收记录中列出。此后端交付也不代表完整前端应用已通过浏览器及正式发布验收。
