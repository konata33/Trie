# 参与开发

感谢参与 Trie。这份文档说明怎么搭环境、跑检查、打包，以及改动怎样才算可以合并。

## 环境

- macOS，Apple Silicon
- Go 1.27.3 或更高（以 `apps/mygo/go.mod` 为准）
- MyGo 命令行通过 `go.mod` 的 `tool` 指令提供，用 `go tool mygo` 调用，不需要单独安装

## 运行

```sh
cd apps/mygo
TRIE_DATA_DIR=/tmp/trie-dev go tool mygo dev
```

开发模式把应用打包成「Trie Dev」（标识 `com.example.trie.dev`，放在 `apps/mygo/.mygo/dev/`）再启动，程序坞上和正式版分得开。改了源码会自动重新构建并重启，Ctrl+C 结束。

从源码运行时，应用只打开 `TRIE_DATA_DIR` 指向的目录：必须是绝对路径，不能是真实资料库，目录不存在时自动创建一个空资料库。

要用真实数据验收界面，先退出 Trie，再复制一份资料库：

```sh
cp -R ~/Library/Application\ Support/Trie /tmp/trie-copy
TRIE_DATA_DIR=/tmp/trie-copy go tool mygo dev
```

| 环境变量 | 作用 |
| --- | --- |
| `TRIE_DATA_DIR` | 资料库目录，绝对路径；从源码运行时必填 |
| `TRIE_NO_REFRESH=1` | 启动时不刷新订阅 |
| `TRIE_FONT_SMOOTHING=1` | 保留 MyGo 默认的字体平滑，用于对比渲染效果 |
| `TRIE_PLAY` | 按逗号分隔的步骤自动操作窗口，配合 `MYGO_FRAME_STATS=all` 记录帧耗时，结束后退出 |

## 检查

提交前在 `apps/mygo` 下全部通过：

```sh
gofmt -l .            # 没有输出才算通过
go vet ./...
go tool mygo vet .
CGO_ENABLED=0 go test -timeout 30m ./...
```

部分测试读取本机的样本（资料库副本、真实文章），样本不在时自动跳过；对拍和真实数据测试由环境变量开启，用法写在各测试文件开头的注释里。

## 打包

```sh
cd apps/mygo
go tool mygo build -skip-dmg
```

产物在 `apps/mygo/build/darwin-arm64/Trie.app`。安装时先退出正在运行的 Trie，再替换 `/Applications/Trie.app`。

## 资料库

- 位置：`~/Library/Application Support/Trie/library.sqlite`。
- 同一个资料库同一时间只能被一个应用打开。
- 动真实资料库之前，先完整备份到 `~/Library/Application Support/Trie Backups/`。

## 代码约定

- 颜色和字号只从 `internal/theme` 取，动效的曲线和时长只从 `internal/motion` 取。
- 联网一律经过 `internal/fetch`。
- 界面文字中英两份，写在 `internal/locales` 的 `en.json` 和 `zh-Hans.json`，改完运行 `go tool mygo generate`；口吻和用词见 `docs/DESIGN.md` 的 Copy 一节。代码注释用英文。
- MyGo 的界面生命周期、线程和测试写法见 [apps/mygo/.agents/skills/mygo-maintenance/SKILL.md](apps/mygo/.agents/skills/mygo-maintenance/SKILL.md)。
- 应用的模块划分见 [apps/mygo/README.md](apps/mygo/README.md)。
- `.tools/` 是本机的开发辅助工具（Ryugi 画板，用于界面方案对比），不进版本库。

## 文档约定

- 规范（[docs/DESIGN.md](docs/DESIGN.md)、[docs/PAGES.md](docs/PAGES.md)、[docs/READER_ENGINE.md](docs/READER_ENGINE.md)）只写现在的做法：陈述句、具体数值，不写日期、来历和被否的方案。
- 项目文档用中文。README.md 用英文，中文版是 README.zh-CN.md，两份一起改。

## 界面改动

- 动手前先读 [docs/DESIGN.md](docs/DESIGN.md) 和 [docs/PAGES.md](docs/PAGES.md) 里对应的页，交付前逐条对照。
- 在真实应用里看一遍，用资料库副本验收；键盘操作和「减弱动态效果」各试一次。
- 改动说明里附改动前后的截图。

## 提交

- 一次提交只做一件事；提交信息用英文，首行概括改了什么，例如 `Motion polish: continuous selection, page fades and rolling counts`。
- 合并请求写清改了什么、为什么改、怎么验证过；界面改动附截图。
- 改了产品或界面规则，同一个合并请求里更新 `docs/` 里对应的规范。
