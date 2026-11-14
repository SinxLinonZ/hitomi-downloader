# Hitomi Local

Go CLI 启动本地 HTTP GUI，前端使用 React、TypeScript 和 Mantine。所有源代码与生成产物位于本目录。

## 构建与运行

需要 Go 1.23+、Node.js 20.19+ 或 22.12+。

```sh
cd src/web
npm ci
npm run build
cd ..
go build -o hitomi-downloader ./cmd/hitomi-downloader
./hitomi-downloader
```

启动后自动打开默认浏览器。也可以打开终端打印的链接。前端产物已嵌入 binary，发布时只需分发该 binary，无需安装 Node.js。

```sh
./hitomi-downloader --port 8080 --download-dir /absolute/path/to/downloads
./hitomi-downloader --proxy http://127.0.0.1:7890
./hitomi-downloader --proxy direct --no-open --data-dir /absolute/path/to/state
```

默认监听 `127.0.0.1` 的随机端口。默认下载目录为工作目录下的 `downloads`，配置与任务记录在 `data`；GUI 保存的下载路径会在下一次启动时恢复，CLI 的 `--download-dir` 可覆盖它。Proxy 默认遵循 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` 环境变量。CLI session 链接用于建立本机 HTTP-only cookie，重启后请使用新链接。

GUI 统一使用 dark theme，保留固定顶栏与侧栏的 Mantine AppShell，移动端通过左上角按钮展开导航。内容区按高密度设计：页面标题与说明合并为一行；各页的搜索与筛选工具栏固定在顶部；搜索结果为封面网格，下载队列为紧凑表格（带“全部 / 进行中 / 待恢复 / 已完成”筛选，每页 100 项），已下载为每页 50 项的双列列表。按 `/` 可聚焦当前页面的搜索框。顶栏“停止服务”和终端 Ctrl+C / SIGTERM 使用相同的退出流程：通知所有已连接页面停止 SSE 重连、取消正在处理的 HTTP 请求、暂停下载并保存进度，再排空连接并退出。停止后页面会禁用操作；重新使用时运行 CLI 并打开新的启动链接。

任务状态通过 SSE 推送：仅在任务列表变化时发送，空闲时每 15 秒发送一次 keep-alive。

## 搜索与下载

- 普通关键词之间取交集；`artist:name`、`group:name`、`series:name`、`character:name`、`language:japanese`、`type:manga`、`tag:name`、`female:name`、`male:name` 支持标签搜索。
- 标签值中的空格使用下划线：`artist:some_artist`。以 `-` 排除词，例如 `language:japanese -tag:anthology`。
- 支持 gallery ID 和作品链接打开详情。分页失败时保留成功卡片，可重试当前页。
- 默认同时处理 2 个作品，每个作品最多 4 页并发；HTTP 请求全局按至少 200ms 间隔发出，避免瞬间冲击上游。
- 网络错误、408、429、5xx 最多尝试 4 次，使用 exponential backoff 和 jitter，并尊重 `Retry-After`（超过 60 秒的要求会返回可重试错误）。
- 优先尝试 metadata 中源文件的原始格式。原始格式返回 403/404 时强制刷新 `gg.js` 重试，仍不可用才尝试可用的 JXL、AVIF、WebP。衍生格式顺序是固定回退顺序，并不保证其编码画质高低；不在客户端重新编码。
- 网络超时或 5xx 不触发格式降级。页面格式、回退原因和源 hash 都保存在 metadata 中。
- 暂停或取消会停止请求，保留完整页面。恢复时核对页面 SHA-256，仅重新下载未完成或损坏的页面；不进行单张图片的字节级 Range 续传。

## 文件布局与恢复

```text
downloads/
  12345 - Title/
    0001.png
    0002.jpg
    metadata.json
  .downloading-67890/
    metadata.json
    0001.png
data/
  config.json
  tasks/12345.json
  covers/12345.webp
```

`metadata.json` 包含原始 gallery 信息（保留所有上游字段）、每页文件名、源 hash、实际格式、字节数、本地 SHA-256 和更新时间。任务进度保存在独立 JSON 中，已下载列表通过扫描当前下载目录的 metadata 获取，不依赖数据库。JSON 和图片均先写临时文件后 rename，不删除已有完成目录。

在 GUI 设置中修改下载目录，然后打开“已下载”。后台扫描会逐步展示发现的作品，显示当前目录、已检查目录数和读取警告，可取消；找到作品 metadata 后不再枚举该目录中的所有图片或子目录。应用运行期间复用内存索引，切换页面不会重新扫描，手动“重新扫描”刷新索引；重启程序后重新发现文件。网络挂载的操作系统 I/O 不能被 Go 强制中断，取消请求会在当前 I/O 返回后生效。

已下载页面采用每页 50 项的列表（宽屏双列），包含本地首张图片的懒加载封面、标题、作者、语言、页数和格式。支持按标题、作者或 ID 筛选、按 metadata 格式筛选和跨页勾选。封面不依赖 Hitomi 在线；JXL 无浏览器支持时显示占位图。 列表显示完整 namespace 的 tags（`female`、`male`、普通 `tag`），点击标签直接加入筛选；标签较多时可展开。搜索框明确标为“搜索已下载”，输入后自动在本地索引中搜索标题、作者、ID 和标签，多个词取交集，支持 `female:glasses`、`female:long_hair`、`-tag:digital` 等条件。独立的可搜索标签多选框显示作品计数，多标签按 AND 组合，可与文字和 metadata 格式同时筛选。新下载与旧数据迁移都完整保留 `metadata.json` 中的 `gallery.tags` 原始字段；搜索使用规范化索引，不会改写或删减原始 tags。

新格式支持逐作品“验证并补全”、勾选批量验证和“验证全部”：核对 metadata 中预期页数、文件大小和 SHA-256，完整页面不访问网络，缺失或损坏页面自动补下。任务详情显示具体失败页，网络恢复后点击“继续 / 重试”。

程序退出或崩溃后，未完成任务恢复为暂停状态。更换下载目录后，旧目录的未完成任务需切回原目录才能继续；移动后的完整作品可直接在新目录扫描和验证。同一 gallery ID 同时只执行一个任务。

下载队列支持“全部暂停”“全部开始”“隐藏已完成”和“移除已完成任务”。全部暂停作用于排队及运行中的下载、迁移和校验任务；全部开始恢复暂停、失败和已取消的未完成任务，跳过已完成或已在运行的任务。暂停后立即全部开始会先等待旧 worker 结束，避免同一任务重复处理；下载目录不匹配等失败会逐项报告。

“隐藏已完成”只影响队列显示。“移除已完成任务”仅删除 `data/tasks/{id}.json` 任务记录和内存中的对应任务，不访问下载目录，不删除图片或作品 metadata，也不会从“已下载”列表移除作品。未完成任务不会被清理；已完成记录在 worker 最后一次保存结束后才可移除，重启后不会恢复已移除的记录。

参考 Tauri 下载器的平铺 `metadata.json` 只识别为“待迁移”，扫描和验证入口不会自动改写。点击单项“迁移”、勾选后“迁移所选”、“迁移本页旧格式”或顶部“全部迁移”后，任务读取现有图片并建立本地 SHA-256 基线，不重新下载、不修改图片。完成前检查 metadata 未被其他程序更改，原文件保存在 `metadata.legacy.json`，新 metadata 原子写入；已有不同备份时停止迁移并报错。旧字段（例如 camelCase 标题和字符串作者列表）转换为新结构，并保留原字段信息。 “全部迁移”需等待扫描完成，覆盖当前下载目录的全部旧 metadata，不受筛选与分页限制；先固定目标快照、跳过已有活动任务，再每批 100 项加入队列，迁移进度仍在下载队列中查看。

旧图片缺失、损坏或同一页存在多个格式时，迁移会记录具体页和警告，保留原图片；后续从“已下载”验证并补全。暂停或取消迁移会保留旧 metadata，恢复时重新读取本地图片；提交 metadata 后发生退出也可恢复任务状态。迁移不会提高旧图片的编码质量，也不再保证旧下载器可读取新 metadata。Node 脚本导出的 gallery JSON 不是完整下载目录，不直接导入。

本地 SHA-256 用于发现下载完成后的文件变化，不代表上游图片的远程真实性证明。源 hash 与本地文件 SHA-256 分开存储。JPEG、PNG、GIF 下载时进行完整解码检查；WebP、AVIF、JXL 检查文件签名和响应长度，后续均使用 SHA-256 检查文件变化。metadata 本身损坏时无法可靠推断页清单，需先恢复 metadata；不会擅自覆盖无法识别的目录。

## 验证

```sh
cd src
go test -race ./...
go vet ./...
cd web
npm run build
npm test
```

Playwright tests 启动真实 Go binary，覆盖目录扫描、离线校验、搜索局部失败和设置持久化，因此先执行上述构建。默认使用 macOS Chrome，可通过 `CHROME_PATH` 指定其他系统的 Chromium executable。测试使用临时 fixture 和 mock 搜索响应，不依赖 Hitomi 在线。受限环境如禁止 `/tmp` 默认路径，可设置 `TMPDIR=/private/tmp`；如果禁止绑定 localhost 端口，则浏览器测试无法启动，Go HTTP handlers 的测试仍可运行。

开发前端时，先通过 `--port 8080` 启动 Go 并在浏览器中打开 CLI 链接完成 session 初始化，再运行 `npm run dev` 并访问同一 hostname 的 Vite 页面。Vite 默认将 `/api` 转发至 `http://127.0.0.1:8080`，可通过 `HITOMI_BACKEND` 修改。浏览器中始终使用 `127.0.0.1`，保持 cookie 的 hostname 一致。

本轮真实上游联调受到执行环境 DNS 限制，动态域名和 `gg.js` 规则以 `ref` 中的协议实现为依据；协议发生变化时会给出明确解析错误，需更新 `internal/hitomi` 适配层。
