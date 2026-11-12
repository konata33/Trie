# Trie for macOS

Trie 的桌面应用：Go 写成，界面用 [MyGo](https://github.com/egoist/mygo) 原生绘制。产品范围见 [docs/PRODUCT.md](../../docs/PRODUCT.md)，开发流程见 [CONTRIBUTING.md](../../CONTRIBUTING.md)。

## 运行与检查

```sh
TRIE_DATA_DIR=/tmp/trie-dev go tool mygo dev      # 开发版「Trie Dev」，只打开这个目录，改了源码自动重启
CGO_ENABLED=0 go test -timeout 30m ./...          # 测试
go vet ./... && go tool mygo vet . && gofmt -l .
go tool mygo build -skip-dmg                      # 打包到 build/darwin-arm64/Trie.app
```

## 结构

```text
main.go             入口：选资料库目录、加锁、组装各模块、开窗口
lock_*.go           资料库锁：同一个资料库一次只能被一个窗口打开
internal/
  app/              窗口与各页：外壳、首页、探索、收藏、摘录、作者页、阅读器、作品页
  reader/           把清理后的正文画成原生元素：段落、代码、表格、图片、摘录
  reading/          读一篇文章：取回网页、提取正文、排版、下载图片，存进资料库
  extract/          正文提取与清理（Readability）、设计画廊作品页、SVG 与图片类型
  feed/             解析 RSS、RDF、Atom，从主页发现订阅源和图标
  fetch/            联网：只访问公网地址，限制重定向、大小和时间
  subscription/     预览、添加订阅源，按批刷新已订阅的源
  explore/          探索目录、上游推荐、设计网站首屏截图
  library/          资料库（SQLite）的读写与校验
  theme/            颜色、字体、字号
  motion/           动效曲线和时长
  video/            作品页录屏解码（macOS AVFoundation）
  web/              URL 解析、HTML 实体等网页文本工具
resources/          应用图标和探索目录（catalog.json、seed.json、站点图标）
```

## 数据

真实资料库在 `~/Library/Application Support/Trie`：

| 文件 | 内容 |
| --- | --- |
| `library.sqlite` | 订阅、文章、正文、收藏、摘录、阅读进度 |
| `explore-cache.json`、`explore-icons.json` | 探索目录的最新文章和图标缓存 |
| `explore-shots/` | 设计网站首屏截图，保留 30 天 |
| `explore-works/` | 设计画廊作品的配图 |
| `recordings/` | 作品页录屏的播放文件 |
| `thumbs/` | 文章配图的缩略图 |
| `trie.lock` | 资料库互斥锁 |

打包后的 `Trie.app` 默认打开真实资料库，`TRIE_DATA_DIR` 可以改成别的目录。从源码运行时必须设置 `TRIE_DATA_DIR`，指向真实资料库会拒绝启动。同一个资料库同一时间只能被一个应用打开，第二个会弹提示并退出。

## 安全边界

- **联网**：所有请求经过 `internal/fetch`。域名解析出的每个地址都必须是公网地址，连接直接连到检查过的地址；重定向不出范围，也不会从 HTTPS 降级；响应大小和耗时有上限。
- **正文**：取回的网页先提取、清理成受限的文档结构，再由阅读器画成原生元素。来源网页的脚本、样式和组件都不会运行。
- **图片**：正文里的图片取回后存进资料库，阅读时只读本机副本。
- **外部链接**：只打开不带用户名和密码的 `http`、`https` 地址，交给系统浏览器。
- **首屏截图**：探索页的设计网站首屏在隐藏的 WebKit 窗口里加载后截取，这是应用里唯一会运行来源网页脚本的地方，窗口不显示、不获得焦点。

## 尚未支持

签名、公证和分发包；Intel Mac 和最低系统版本的兼容性验证；作品页录屏在 macOS 以外的解码；全量历史补齐、定时后台刷新、付费登录和云同步。
