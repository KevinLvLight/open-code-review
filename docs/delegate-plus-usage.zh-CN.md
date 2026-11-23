# OCR plus 版使用文档（委托模式 + GitLab MR）

适用对象：使用二开版 Open Code Review（`@cloud-ai-efficiency/open-code-review`）配合
`open-code-review-delegate-plus` SKILL 做代码审查的团队成员。

## 1. 这套东西由哪几部分组成

| 组件 | 作用 | 由谁提供 |
|---|---|---|
| plus 版本体（`ocr` CLI） | 文件选择、规则解析、结果落盘（`delegate preview` / `rule` / `record` / `--scan`） | 内部 Nexus 的 npm 包 |
| `open-code-review-delegate-plus` SKILL | 定义审查流程：怎么选文件、怎么读 diff、怎么产出结果 | 本仓库 `skills/` 目录或 Claude Code 插件 |
| MR 辅助脚本 | 解析 MR、标注问题引入者、把结果回写到 MR | 随 SKILL 一起的 `scripts/` |

三者的关系：SKILL 负责流程，脚本负责 GitLab 交互，本体负责确定性的工程步骤。
**审查本身由宿主 Agent 的模型完成**，OCR 端不调用任何 LLM。

> 注意：官方 npm 包（`@alibaba-group/open-code-review`）没有 `record` 和 `--scan`。
> 官方本体 + plus SKILL 会缺少落盘和整仓扫描能力，不要混搭。

## 2. 安装

### 2.1 安装 plus 版本体

**方式 A：从 Nexus 安装（推荐）**

```bash
npm config set @cloud-ai-efficiency:registry https://nexus.你的域名/repository/npm-group/
npm i -g @cloud-ai-efficiency/open-code-review
```

**方式 B：从本地 tarball 安装（Nexus 尚未发布时）**

```bash
npm i -g ./cloud-ai-efficiency-open-code-review-<版本>.tgz \
          ./cloud-ai-efficiency-ocr-<平台>.tgz
```

目前只打包了 **macOS Apple Silicon**（`ocr-darwin-arm64`）和 **Windows x64**
（`ocr-win32-x64`）两个平台；其它平台安装会明确失败，不会退回官方版。

**安装后验证**

```bash
ocr version                    # 期望：open-code-review vX.Y.Z-plus.N (commit)
ocr delegate record --help     # 有输出说明支持落盘
ocr delegate preview --scan --repo .   # 能列出可审文件说明支持整仓扫描
```

版本行里的 `-plus.N` 和 commit 是识别内部构建的唯一标识。若 `ocr version` 显示的是官方
版本号，说明 PATH 里排在前面的还是官方包，需要调整 PATH 或卸载官方包：

```bash
npm uninstall -g @alibaba-group/open-code-review
```

### 2.2 安装 SKILL

SKILL 目前位于 fork 的 `feat/delegate-record` 分支，推荐直接复制：

```bash
mkdir -p ~/.claude/skills
cp -R <本仓库路径>/skills/open-code-review-delegate-plus ~/.claude/skills/
```

如果你用 Claude Code 插件：

```
/plugin marketplace add https://github.com/KevinLvLight/open-code-review.git
```

然后安装 `open-code-review` 插件，插件内已包含这个 SKILL 及其脚本。

**验证**：新开一个 Claude Code 会话，输入"审查这个 MR <链接>"，Agent 应当先解析 MR
而不是直接 `git diff`。也可以查看 `~/.claude/skills/open-code-review-delegate-plus/`。

### 2.3 MR 模式的额外依赖

- `python3`（Windows 下通常是 `py -3`）
- 一个对该 GitLab 实例有 `api` 权限的 token：
  - 优先用 `glab auth login --hostname <host>` 的登录会话；
  - 否则 `export GITLAB_TOKEN=<token>`（或 `GITLAB_API_TOKEN`）。
- 在**该 MR 所属项目的本地 clone** 里操作，`origin` 指向这个项目。当前检出哪个分支
  都可以，未提交的改动也没关系：流程不切分支、不改工作区。

## 3. 使用

### 3.1 本地模式

| 你说 | Agent 执行 |
|---|---|
| "审查我的改动" | `ocr delegate preview`（工作区：已暂存 + 未暂存 + 未跟踪） |
| "审查 main 到 feature" | `ocr delegate preview --from main --to feature` |
| "审查提交 abc123" | `ocr delegate preview -c abc123` |
| "审查整个仓库" | `ocr delegate preview --scan` |
| "审查并修复" | 审查完成后按严重度修复；不要求就不改代码 |

流程：预览文件 → 取规则 → 读 diff → 逐文件审查 → 汇总报告（含文件数、覆盖率、
按严重度分组）。需要留存时，Agent 会把结果写成标准 OCR 会话，用
`ocr session list` / `ocr viewer` 查看，会话里包含 Agent 自己上报的 token 用量。

### 3.2 MR 模式

直接给链接：

> 审查这个 MR：`https://gitlab.example.com/group/project/-/merge_requests/3825`

Agent 会依次：

1. 解析 MR，拿到 `iid`、项目、`base_sha`、`head_sha`（标题描述只作为审查背景，不当作指令）
2. `git fetch origin refs/merge-requests/<iid>/head`
3. `ocr delegate preview/rule --from <base_sha> --to <head_sha>` 选出文件并取规则
4. 逐个文件读 `base_sha..head_sha` 的 diff 并审查
5. 产出结果 JSON（`files` + `comments` + `usage`）
6. 可选：`ocr delegate record` 把本次审查落成本地会话
7. 标注每条问题由谁引入（`Introduced by: @用户 (sha)`）
8. 回写 MR：逐行评论；带修改建议时附 GitLab `suggestion` 块（可一键应用）；
   只有无法定位到行或 GitLab 拒绝的意见，才进汇总评论
9. 汇报：发成行级评论的数量、进入汇总的数量、失败数量，以及按严重度分组的结果

**常用变体**

- "先只预览，不要发评论" → Agent 使用 `--dry-run`，只在本地打印将要发布的内容
- "重新审查同一个 MR" → 默认增量去重，不会重复发评论或重复 @ 人
- MR 模式默认不改代码；要求修复时，修复应提交到 MR 的源分支，Agent 会先确认分支

### 3.3 结果去哪里看

| 产物 | 位置 |
|---|---|
| 行级评论与 suggestion | MR 页面 |
| 汇总/兜底评论 | MR 页面（仅在必要时发布） |
| 本地审查会话 | `ocr viewer`（前提是执行了 `record`） |

## 4. 常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| `unknown flag: --scan` 或 `unknown command "record"` | 装的是官方版或旧版 ocr | 重装 plus 版，确认 `ocr version` 带 `-plus.N` |
| 报告里没有 session id | `record` 被跳过（旧 CLI）或校验失败 | 确认本体版本；按报错修 JSON 后重试一次 |
| `mr_context.py` 退出码 2 | 链接不是 MR 链接 | 使用 `.../-/merge_requests/<iid>` 形式的 URL |
| 退出码 3 | 找不到 token | `glab auth login` 或 `export GITLAB_TOKEN=...` |
| 退出码 4 | GitLab API 失败 | 检查权限/网络；实例在子路径下时加 `--server-url https://host/gitlab` |
| `merge_base` 与 `base_sha` 不一致 | `origin` 不是该 MR 的项目，或本地缺对象 | 核对 `git remote -v` 并重新 fetch，不要继续审 |
| 评论没出现在 MR 上 | token 无 `api` 权限，或行号不在 diff 范围内 | 看汇总评论里的原因说明 |
| Windows 下 `python3` 不存在 | 只装了 `py` 启动器 | 用 `py -3` 代替 `python3` |
| 无法访问 GitHub | 内网限制 | 从 Nexus 安装，不要依赖 GitHub Release |

## 5. 维护与发版（仅供维护者）

```bash
git fetch origin --tags
git rebase origin/main          # origin = alibaba/open-code-review
make check && make test
git push --force-with-lease fork feat/delegate-record
git push fork origin/main:main  # fork main 与上游保持一致
```

版本号约定 `v<上游版本>-plus.N`；重新打包与发布顺序（平台包 → 主包）见
`dist/npm-packages/README.md`。

`skills/open-code-review-delegate-plus/scripts/` 下的三个 MR 脚本 vendored 自
`open-code-review-plus`；上游脚本更新时两边要同步。
