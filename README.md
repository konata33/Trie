<div align="center">
  <img src="apps/mygo/resources/icon.png" width="96" height="96" alt="Trie 图标">
  <h1>Trie</h1>
  <p>为细读而做的 macOS 阅读器。<br>跟住一小组挑过的独立作者和设计网站，把读到的东西整理成可以交给 coding agent 借鉴的材料。</p>
</div>

## 简介

Trie 不追求「把所有新内容都推给你」，那是通用 RSS 阅读器的事。它只管一小组挑过的来源，把每一篇读好：结构完整的正文，安静的版面，读到一半可以接着读。读到值得借鉴的东西，一篇文章的想法，或者一个网站的版式、配色、字体和动效，可以连同出处一起复制成 Markdown，交给 Claude Code、Codex 或笔记。

## 功能

- **首页**：一屏往下滚的总览：画廊里今天的设计例子、聚合站排得最高的几篇、每位关注作者的最新一篇，以及几位还没关注的作者。
- **订阅**：每位作者一个文章列表。没有已读、未读和计数，只记阅读进度。
- **探索**：手挑的中英文独立博客、设计写作、通讯和设计画廊目录，加上上游聚合站推荐的作者。订阅前就能在应用里读。
- **阅读器**：统一排版的结构化正文，保留标题目录、代码、表格、图片和脚注；设计画廊的条目按作品页显示（录屏、截图、配色与署名）。
- **收藏**：按作者收，一键收藏，不用归类，可以撤销。
- **摘录**：带出处的段落摘录，可以连同链接和想法一起复制成 Markdown。

计划中：翻译和 AI 精读，范围刻意做小。

## 系统要求

- macOS，Apple Silicon
- 从源码构建需要 Go 1.27.3 或更高

## 快速开始

```sh
git clone <仓库地址> trie
cd trie/apps/mygo
TRIE_DATA_DIR=/tmp/trie-dev go run .
```

从源码运行时，应用只打开 `TRIE_DATA_DIR` 指向的资料库（必须是绝对路径），不会碰已安装版本的资料库。打包和安装见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [docs/PRODUCT.md](docs/PRODUCT.md) | 产品定位、目标用户、功能范围和约束 |
| [docs/DESIGN.md](docs/DESIGN.md) | 设计系统：颜色、字体、布局、层次、形状、动效、图标和组件 |
| [docs/PAGES.md](docs/PAGES.md) | 各页的版面和交互规格 |
| [docs/READER_ENGINE.md](docs/READER_ENGINE.md) | 正文从取回到显示的处理规则 |
| [docs/decisions/](docs/decisions/README.md) | 决策记录：为什么这样定，考虑过哪些做法 |
| [apps/mygo/README.md](apps/mygo/README.md) | 应用架构、数据位置和安全边界 |
| [assets/pictograms/README.md](assets/pictograms/README.md) | 场景小图形的原图和画法基准 |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 开发环境、检查、打包和提交约定 |
| [AGENTS.md](AGENTS.md) | 给 coding agent 的工作约定 |

## 项目结构

```text
apps/mygo/          macOS 应用（Go + MyGo 原生界面）
assets/pictograms/  场景小图形的生成原图、分层矢量稿和描线工具
docs/               产品、设计、页面规格、正文引擎和决策记录
.tools/             开发辅助工具（Ryugi 画板，用于界面方案对比）
```

## 参与开发

欢迎提交问题和改动。开始之前请先读 [CONTRIBUTING.md](CONTRIBUTING.md)；改界面前先读 [docs/DESIGN.md](docs/DESIGN.md) 和 [docs/PAGES.md](docs/PAGES.md) 中对应的页面。
