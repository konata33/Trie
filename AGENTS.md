# Trie 开发指南

给在这个仓库里工作的 coding agent（Claude Code、Codex）。项目概览见 [README.md](README.md)，开发流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 项目

Trie 是 macOS 上的博客阅读应用，代码在 `apps/mygo`：Go + MyGo 原生界面。

## 文档

- 改界面：先读 `docs/DESIGN.md`（全应用规则）和 `docs/PAGES.md` 里对应的页，交付前逐条对照。
- 加功能或删功能：先读 `docs/PRODUCT.md`。规范里没有的功能，先和维护者确认该不该存在。
- 正文提取、清理、显示：`docs/READER_ENGINE.md`。
- 规范写现在的做法：陈述句、具体数值；不写日期、来历、被否的方案、代码名和实现细节。新决定直接改进规范。
- 以前的取舍和被否掉的做法在 git 历史里：`git log -- docs/decisions` 找到删除它的提交，用 `git show <提交>^:docs/decisions/<文件>` 查看。被否掉的做法不要再做回来。
- 新需求和规范冲突时，指出是哪一条，由维护者决定。

## 常用命令

都在 `apps/mygo` 下运行：

- 运行：`TRIE_DATA_DIR=<资料库副本的绝对路径> go run .`
- 测试：`CGO_ENABLED=0 go test -timeout 30m ./...`
- 检查：`go vet ./...`、`go tool mygo vet .`、`gofmt -l .`
- 打包：`go tool mygo build -skip-dmg`，产物在 `build/darwin-arm64/Trie.app`。装进 `/Applications` 会替换正在使用的版本，先征得维护者同意。

## 写代码

- 先读 `apps/mygo/.agents/skills/mygo-maintenance/SKILL.md`。
- 颜色和字号只从 `internal/theme` 取，动效的曲线和时长只从 `internal/motion` 取。
- 联网一律经过 `internal/fetch`，它负责只访问公网地址的检查。

## 资料库

- 已安装的 `/Applications/Trie.app` 打开真实资料库：`~/Library/Application Support/Trie`。
- 开发和测试只用资料库的副本，`TRIE_DATA_DIR` 指向副本目录。从源码运行时指向真实资料库会拒绝启动。
- 一个资料库同一时间只能由一个应用打开，第二个会弹提示并退出。不要关掉别人开着的实例。
- 动真实资料库之前，先完整备份到 `~/Library/Application Support/Trie Backups/`。
- 真实资料库很安静：几位作者，约一周一篇。界面要拿它的副本验收。

## 交付前

- 上面的测试和检查都通过。
- 改界面要在真实应用里看一遍，键盘操作和减弱动态效果各试一次。改完重启应用。
- 没被要求提交就不提交。

## 协作

同一份工作副本上可能同时有几个 agent 会话。

- 开工先 `git status`，查清没提交的改动属于谁，只改自己那一块。
- 共用文件（`docs/DESIGN.md`、`docs/PAGES.md`、`apps/mygo/go.mod`、`apps/mygo/main.go`）改之前重读一遍，只改需要的几行。
- `.context/` 是本机的临时协作目录，不进版本库；部分测试会读其中的样本，没有时自动跳过。
- 沟通、界面文案、项目文档用中文；代码注释用英文。
