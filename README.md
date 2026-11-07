# BlogShelf

Mac 博客订阅与阅读工具，名称暂定。已经建立 Electron + React + TypeScript 桌面工程，同时保留 Ryugi 原型与目录核验工具。

## 桌面工程

工程位于 [apps/desktop](apps/desktop/README.md)，按第三轮实现三栏界面，并将收藏、已读、订阅名称和退订状态保存到本地 SQLite。已实际验证退出进程后重新启动仍保留记录。

```sh
cd apps/desktop
pnpm install
pnpm dev
```

已经接通“输入 RSS/Atom 或博客首页 → 收录真实文章列表 → 点击阅读正文”，支持真实刷新、去重、本机保存与正文缓存，不限制预置博主。首次收录以源当前返回的条目为准，全历史补齐尚待接入。运行见 [工程说明](apps/desktop/README.md)，真实订阅验收见 [SUBSCRIPTION_VERIFICATION.md](SUBSCRIPTION_VERIFICATION.md)，正文规则见 [READER_VERIFICATION.md](READER_VERIFICATION.md)。

文章详情已接入自动目录、提示块、真实代码高亮与复制、图片预览、阅读设置和专注阅读，旧缓存可离线升级排版。模块边界与开源参考见 [阅读引擎说明](READER_ENGINE_SPEC.md)，实际验收见 [正文与阅读验收](READER_VERIFICATION.md)。

正式应用界面与受控正文组件已采用 StyleX，保留现有外观。文章先统一为内容树和阅读单元，旧缓存可按需离线转换，失败继续使用隔离 HTML 阅读。边界见 [样式说明](apps/desktop/STYLING.md) 与 [阅读引擎说明](READER_ENGINE_SPEC.md)。

导航与阅读状态已分开管理：博主入口显示已保存的全部文章，仅打开不自动已读，中途切换后可续读。交互规则、检查发现和剩余边界见 [阅读流程审视](PRODUCT_REVIEW.md)。

## 已确认的范围

- 平台：Mac。
- 订阅博主、查看文章列表、阅读文章、收藏与取消收藏。
- 原型使用 Ryugi，Figma 不参与本项目流程。
- 第一批实际验证来源：Innei、Anthony Fu（antfu.me）、Velas 电波站（velasx.com）。
- 核心目标是聚合所支持博主的完整公开历史文章，支持查阅和搜索。首次订阅补历史目录，RSS/Atom 负责后续更新；不将 RSS 当前条数作为产品的历史数量上限。目录覆盖和正文获取分开验证。

## 当前原型

统一画布页面为 `foundation`（原型迭代）。第一轮至第四轮从上往下排列，同一主题继续向下追加，保留原型历史。

第四轮位于 `.ryugi/artifacts/artboard/mac-reader/pages/reading/`，当前阅读基线改为石墨色工作台：48px 活动栏、200px 订阅浏览器，主区以标签切换文章索引和阅读器，阅读器配有停靠目录与底部进度；右上角提供纸白/石墨切换，并支持 `⌘K`。保留真实博客图标、目录、提示块、代码复制、图片、表格、阅读设置和异常状态。内容为明确标注的节选与排版样例，不连接实时订阅或正式资料库，范围见 [ROUND4_SCOPE.md](ROUND4_SCOPE.md)。当前布局已通过 21 项浏览器交互检查，产品设计说明见 [PRODUCT_STYLE.md](PRODUCT_STYLE.md)。

该画板是当前阅读设计基线。后续同一方向的细节直接更新当前方案，仅在比较不同方向或重要阶段评审时保留新快照；第一至第三轮保留为历史记录。

当前阅读基线为石墨色工作台，并可通过右上角切换纸白主题；`⌘K` 可快速打开文章与操作。此次仅调整同一原型画板，正式 Electron 应用样式保持不变。参考依据和当前验证状态见 [PRODUCT_STYLE.md](PRODUCT_STYLE.md)。

第三轮位于 `.ryugi/artifacts/artboard/mac-reader/pages/verified/main.html`，已经接入六个博客的真实核验快照：5 篇去重文章、10 条待核验、1 条已排除。提供来源筛选、标题与作者搜索、收藏、来源管理及重新载入记录；正文继续通过原文入口查看。范围见 [ROUND3_SCOPE.md](ROUND3_SCOPE.md)，验证见 [ROUND3_VERIFICATION.md](ROUND3_VERIFICATION.md)。

第二轮文件：`.ryugi/artifacts/artboard/mac-reader/pages/foundation/main.html`，聚焦订阅基础和产品细节。

已加入三个博客的真实网站图标、添加前预览、重复识别、来源菜单、重命名、退订与撤销、保留收藏、已读操作及恢复、空状态、刷新反馈和可切换的网络演示状态。正文阅读本轮只保留信息预览与原文入口。

第一轮文件保留在 `.ryugi/artifacts/artboard/mac-reader/pages/reader/main.html`，同样显示在统一画布上；两个 HTML 文件均未因合并画布而改动。

阅读方案画板尺寸为 1280 × 860，默认石墨色工作台布局，正文与元信息使用系统字体。活动栏、订阅浏览器、标签栏、阅读器和停靠目录构成当前结构。

示例标题、链接和日期来自已验证订阅源，导读是为原型撰写的示例文字，不冒充作者原文。订阅和刷新使用内存中的固定示例数据，不执行真实 RSS 请求；刷新页面会重置。

运行本地预览：

```sh
node .tools/ryugi-skills/artifacts/app/ryugi-artifacts.cjs start --json --project-dir . --artifact artboard/mac-reader
```

项目关闭 Ryugi 自动发布，当前未发布至 Ryugi Cloud。技能安装在项目的 `.tools/ryugi-skills/` 内，没有修改全局插件配置。

前两轮为 Ryugi 原型阶段，第二轮范围见 [ROUND2_SCOPE.md](ROUND2_SCOPE.md)。用户随后确认采用 Electron 技术栈，正式工程以上面的桌面工程说明为准。

## 桌面版本的基础验收目标

订阅一个提供 RSS 或 Atom 的博客，刷新并获得文章列表，查看正文或清楚标注的摘要，收藏文章，重启应用后仍能找到收藏。

提议优先支持 RSS/Atom，尚未确认支持所有博客。正文获取失败、只有摘要、更新失败需要明确展示，不能把摘要当作全文。收藏正文和元数据的保存策略在实现前确定。

后续再讨论 AI 分类、语义搜索、付费文章登录、多设备同步与收费。

三个来源的订阅源、正文情况和兼容性发现见 [SOURCE_VALIDATION.md](SOURCE_VALIDATION.md)。

历史目录的独立最小技术验证见 [CATALOG_SPIKE.md](CATALOG_SPIKE.md)。它不修改原型，尚未接入真实订阅流程，也不宣称已取得任意博客的全部文章。

通用目录、RSS 入口及第三方订阅源的改进和 12 站同预算回归见 [最新实测记录](reports/blog-batch-2/improved/RESULTS.md)。三份公开参考目录候选集合已逐项匹配，正文与文章身份仍需分别核验。

目录之后的文章身份、元数据和地址去重已建立独立核验程序，规则与运行方式见 [ARTICLE_VERIFICATION.md](ARTICLE_VERIFICATION.md)，六站抽样见 [文章核验记录](reports/article-verification/RESULTS.md)。保存的核验结果已接入第三轮原型；不确定和失败的候选会保留，不当作已验证文章删除。
