# JetBrains Maple Mono

网站界面、Markdown 正文、代码与 Mermaid 图表使用的本地字体。

- 项目：https://github.com/SpaceTimee/Fusion-JetBrainsMapleMono
- 固定版本：`1.2304.79`
- 上游文件：`JetBrainsMapleMono-XX-XX-XX-XX.zip`（标准版，保留连字与中英文 2:1 等宽比例）
- 下载：https://github.com/SpaceTimee/Fusion-JetBrainsMapleMono/releases/download/1.2304.79/JetBrainsMapleMono-XX-XX-XX-XX.zip
- 上游 ZIP SHA-256：`1998cf7047be954c781510d5a651e9d57fadb2ee00bebd80a0719d49eaa50513`
- 许可：SIL Open Font License 1.1，原文见同目录 `LICENSE.txt`。

使用 FontTools 的 WOFF2 编码（Brotli quality 9）将上游 TTF 转为浏览器字体，保留完整字符集与 OpenType 连字表，不裁剪中文字符。包含 Regular（400）、Medium（500）、SemiBold（600）、Bold（700）、Italic（400）及 BoldItalic（700）。

字体通过 `src/styles/base.css` 中的 `@font-face` 加载，由 Vite 随网站构建复制到 `dist/fonts/jetbrains-maple-mono/`。运行时不请求 GitHub 或字体 CDN，也不需要用户安装字体。

重新转换时，在独立 Python 环境安装 `fonttools[woff]`，解压指定版本后对以上六个 TTF 执行：

```python
from fontTools.ttLib import TTFont, woff2

compress = woff2.brotli.compress
woff2.brotli.compress = lambda data, mode: compress(data, mode=mode, quality=9)

font = TTFont('JetBrainsMapleMono-Regular.ttf', recalcBBoxes=False, recalcTimestamp=False)
font.flavor = 'woff2'
font.flavorData = woff2.WOFF2FlavorData(transformedTables=set())
font.save('JetBrainsMapleMono-Regular.woff2')
```

对其他字重与斜体使用相同方式，并保留原始 `LICENSE.txt`。
