# 第三方依赖

Go 依赖的准确版本、直接及间接依赖见 `go.mod` 和 `go.sum`。发行时须保留各依赖的许可证；以下列出直接运行依赖及导出资源。

| 依赖 | 版本 | 授权 | 用途 |
|---|---|---|---|
| modernc.org/sqlite | v1.59.0 | BSD-3-Clause | 纯 Go SQLite 驱动；SQLite 引擎为 public domain |
| github.com/gofrs/flock | v0.13.0 | BSD-3-Clause | 数据目录 OS 文件锁 |
| github.com/88250/lute | v1.7.6 | Mulan PSL v2 | Markdown 解析与渲染 |
| github.com/microcosm-cc/bluemonday | v1.0.27 | BSD-3-Clause | HTML 安全过滤 |
| golang.org/x/net | 见 go.mod | BSD-3-Clause | HTML 解析 |
| KaTeX | 0.16.22 | MIT | HTML 离线导出公式及字体 |
| Mermaid | 10.9.3 | MIT | HTML 离线导出图表 |

导出资源的完整授权附在资源目录，并随 HTML 导出包携带。其余依赖的完整授权副本收集在 `licenses/`。
