# 本机部署

本目录提供部署模板。执行前请将 `deploy/ljmdb-backend.service` 中的用户、项目目录和数据目录占位符替换为本机值，并按需调整 Nginx 域名。

Nginx 在 IPv4 / IPv6 的 9955 端口提供前端，并代理 `/api/` 到仅监听 `127.0.0.1:19955` 的 Go 后端。后端数据目录为 `/path/to/OpenLJMDB-data`。访问入口为 `http://<PUBLIC_HOST>:9955/`，导航为 `http://<PUBLIC_HOST>:10000/`。

当前入口使用 HTTP，应用尚无登录鉴权，能够访问该端口的用户可以读取及修改知识库。Nginx 在转写回环 Host / Origin 前校验浏览器的原始同源信息，并保留后端的跨站及数据世代校验。

更新部署：

```bash
cd /path/to/OpenLJMDB/web
npm ci
npm run build
cd ..
mkdir -p deploy/bin
go -C backend build -o ../deploy/bin/ljmdb ./cmd/ljmdb
sudo bash deploy/install.sh
```

`install.sh` 会创建 `/var/www/openljmdb/releases/` 下的新构建副本，切换 `current` 链接，验证并重载 Nginx，启用后端开机自启，并放行 UFW 的 IPv4 / IPv6 TCP 9955。已有配置备份保存在 `/var/backups/openljmdb-deploy/`。部署不会覆盖后端数据。

已部署环境同时更新前后端时，可先将验证过的后端构建到 `deploy/bin/ljmdb.next` 并构建前端，再运行 `sudo bash deploy/update-app.sh`。脚本备份正在运行的后端和前端发布目录，重启后端并发布前端，校验服务和数据实例/世代；失败时恢复前后端，不更改 Nginx 配置或后端数据。

仅更新前端（例如侧边栏折叠）时：

```bash
cd /path/to/OpenLJMDB/web
npm run build
cd ..
sudo bash deploy/update-web.sh
```

`update-web.sh` 原子切换前端发布目录，保留上一版本，并校验首页、全部 JS/CSS 文件、IPv4 / IPv6 API 健康及数据实例/世代；校验失败自动恢复上一版本。它不重启后端，也不改动 Nginx 配置。该脚本要求已有本机部署，发布仍需 sudo 权限。

运维检查：

```bash
systemctl status ljmdb-backend nginx
journalctl -u ljmdb-backend -n 50
curl --noproxy '*' http://127.0.0.1:9955/api/v1/health
curl --noproxy '*' 'http://[::1]:9955/api/v1/health'
sudo ufw status
```

## 导入自动分类

分类模型固定为 `decision-model-preview`，通过专用 `POST /v1/systemone` 接口调用。
分类服务地址需要由部署者显式配置为 HTTPS 站点根地址，不带 `/v1`。
后端从 `LJMDB_DECISION_API_KEY` 读取密钥，`LJMDB_DECISION_BASE_URL` 可覆盖服务地址；
密钥不进入前端、数据库或应用备份。

现有 systemd 部署在终端运行：

```bash
cd /path/to/OpenLJMDB
sudo bash deploy/configure-classifier.sh https://your-classifier.example.com
```

脚本隐藏输入 API Key，将配置写入仅 root 可读的 `/etc/openljmdb/classifier.env`，
通过 systemd 的 `EnvironmentFile` 注入后端进程，并重启后端。
脚本要求将 HTTPS 根地址作为第一个参数。
无需在终端命令、shell 历史或聊天中粘贴密钥。

配置后，在“设置 → 导入”开启“导入自动分类”并保存。
开关默认关闭，持久保存在知识库设置中。开启后，Markdown 完整正文、标题和现有
知识库名称、描述会发送至分类服务，每篇默认采用概率最高的知识库。
导入确认页可以逐篇修改知识库和父文档，也可以统一应用一个位置。
ZIP 不调用分类接口，保留父子结构和整包导入位置。
模型请求失败或未配置密钥时，仍可手动导入。

如果直接从 shell 启动后端，环境变量应在同一个终端设置后启动进程：

```bash
export LJMDB_DECISION_BASE_URL='https://your-classifier.example.com'
read -r -s -p '分类服务 API Key: ' LJMDB_DECISION_API_KEY
printf '\n'
export LJMDB_DECISION_API_KEY
# 使用原有启动参数运行后端
```

公网域名的 AAAA 记录需跟随本机动态 IPv6 更新。绑定 `[::]:9955` 后，本机 IPv6 地址续租无需修改 Nginx。
