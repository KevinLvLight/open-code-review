---
name: open-code-review-plus
description: >
  使用 alibaba/open-code-review 的 `ocr` CLI 做 AI 代码审查（code review），
  可审查本地 Git 改动，也可审查 GitLab 合并请求（merge request / MR）。
  当用户要求审查代码、审查已暂存或未暂存的改动、审查某个提交、比较分支时使用；
  当用户给出 GitLab 合并请求链接要求审查时也使用，此时审查结果会自动回写到
  MR：逐行的 diff 评论、可一键应用的 suggestion，并 @ 引入每个问题的作者。
  输出逐行审查意见，用户要求时可直接修复。
license: Apache-2.0
compatibility: >
  需要已安装 `ocr` CLI（`npm install -g @alibaba-group/open-code-review`
  或 GitHub release 二进制）。首次运行前需配置受支持的 LLM 服务（协议：
  Anthropic、OpenAI Chat Completions、OpenAI Responses、AWS Bedrock）。
  MR 模式另需 `python3`，以及具有 api 权限的 GitLab token（`GITLAB_TOKEN`，
  或 `glab auth login` 登录会话）。
metadata:
  author: alibaba
  homepage: https://github.com/alibaba/open-code-review
  version: "1.0.0"
---

# Open Code Review Plus

本 Skill 调用 [open-code-review](https://github.com/alibaba/open-code-review)（`ocr`）对本地改动或 GitLab 合并请求做代码审查。`ocr` 是一个开源的 AI 代码审查 CLI，读取 Git diff，生成结构化的逐行审查意见。

本地模式流程以及“输出格式”之后的通用章节，精简自 `skills/open-code-review/SKILL.md`（去掉了重复说明、内部阈值、Validation 和 References）；原版有实质改动时请同步过来。

## 模式选择

- **MR 模式**：用户请求中包含 GitLab 合并请求链接（含 `/-/merge_requests/<iid>`）。按“MR 模式流程”执行。
- **本地模式**：其他所有情况，包括只给了分支名的“审查这个 PR”。按“本地模式流程”执行。本地模式不访问 GitLab，也不发布任何内容。

## 本地模式流程

### 第 1 步：收集业务背景

分析审查对象（提交、分支或改动），提炼简洁的业务背景，通过 `--background` 传入，以提高审查质量。

### 第 2 步：执行代码审查

**不要预先检查 `ocr` 是否已安装**，跳过 `command -v ocr`、`ocr --version` 之类的探测。直接假定 CLI 可用并运行审查，常见情况下能省一次工具调用。只有审查报 `command not found` 时，才按“故障排查”安装。

带上合适的参数运行 OCR。有业务背景时，**始终通过 `--background` 传入**：

```bash
ocr review --audience agent --background "business context here" [user-args]
```

**参数说明：**

- **业务背景**（推荐）：用 `--background "context"` 或 `-b "context"` 提供业务背景，提高审查质量
- **默认**（用户未给参数）：审查已暂存、未暂存和未跟踪的改动（工作区模式）
- **指定提交**：用 `--commit` 或 `-c` 审查单个提交相对其父提交的改动
- **分支比较**：用 `--from <ref>` 和 `--to <ref>` 审查两个 ref 之间的 diff
- **超时**：每个审查组的实际超时 = `--timeout` × 审查轮数。默认 `--timeout 15`，默认强度 `medium`（2 轮）即 30 分钟；`low`/`high` 分别为 15/45 分钟。
- **并发**：默认 8 个文件并发；遇到限流时用 `--concurrency <n>` 调低
- **预览模式**：用 `--preview` 或 `-p` 预览将被审查的文件，不调用 LLM
- **输出文件**：用 `--output <path>` 把完整结果写入文件而不是 stdout；如果报 `unknown flag: --output`，见“故障排查”

**常见调用方式：**

| 用户说 | 执行的命令 |
|--------|-----------|
| “审查我的改动” / “审查工作区” | `ocr review --audience agent -b "context"` |
| “审查这个 PR” / “审查功能分支” | `ocr review --audience agent -b "context" --from main --to <branch>` |
| “审查提交 abc123” | `ocr review --audience agent -b "context" --commit abc123` |
| “会审查哪些文件？”（预演） | `ocr review --preview` |

**输出方式：**

- 始终使用 `--audience agent`，不输出进度界面，只输出最终结果
- **防止输出被截断**：审查量大或工具环境受限时，传 `--output /tmp/ocr_out.txt`，再用读文件工具完整读取。不要把 stdout 通过 `tail` 或 `head` 截取，那样会丢掉前面的审查意见。

**失败时：** 如果 `ocr review` 以非零状态退出（例如 LLM 连接错误），不要盲目重试，先到下方“故障排查”找到对应的解决办法再重新运行。

### 第 3 步：汇报

OCR 的每条意见都带有结构化的 `severity`（critical / high / medium / low）和 `category`（bug / security / performance / maintainability / test / style / documentation / other）。按严重度分组汇报，丢弃 `low` 级别的意见，它们多半是误报或吹毛求疵。

### 第 4 步：修复

修复前，先确认用户是否要求自动修复：

- 用户明确要求“审查并修复”或类似说法时，直接修复
- 用户只要求“审查”、没有修复意图时，修改任何代码前先征得同意

修复问题和建议时：

- 重点处理 critical、high 和 medium 级别的问题
- 修复方式安全且明确时，直接改代码
- 需要人工介入的复杂修复，清楚说明要做什么
- 提交前始终和用户确认修复内容

## MR 模式流程

当用户给出 GitLab 合并请求链接（`https://<host>/<group>/<project>/-/merge_requests/<iid>`，gitlab.com 或自建实例均可）时使用本模式。它审查该合并请求，并**自动把结果回写到 MR**：逐行的 diff 评论；OCR 给出替换代码时附带可一键应用的 `suggestion` 块；以及一行 `Introduced by: @user`，指出最后改动这几行的人。只有存在无法发成行级评论的意见，或完全没有发现问题时，才会额外发一条汇总评论。

**前提条件**

- 在该合并请求所属项目的本地克隆中运行，且 `origin` 指向这个项目。当前检出哪个分支都可以，有未提交的改动也没关系：整个流程不会切换分支，也不会修改工作区。
- `PATH` 中有 `python3`。
- 一个对该 GitLab 实例具有 `api` 权限的 token：优先使用 `glab auth login --hostname <host>` 的登录会话（glab 按实例分别保存 token），否则使用 `GITLAB_TOKEN` / `GITLAB_API_TOKEN`。如果 `GITLAB_HOST` 指向另一个实例，则不使用环境变量里的 token，避免把一个实例的 token 发给另一个实例。

以下命令中，`<skill-dir>` 是本 SKILL.md 所在目录。中间文件放在 `<work>`，即 `echo "$(git rev-parse --absolute-git-dir)/open-code-review-plus"` 打印出的绝对路径。它位于 `.git` 内部，之后做工作区审查时不会被当成未跟踪文件。第 1 步之前先用 `mkdir -p "<work>"` 创建它。

### 第 1 步：解析合并请求

```bash
python3 <skill-dir>/scripts/mr_context.py "<mr-url>" --background-out "<work>/mr-background.md" > "<work>/mr.json"
```

退出码非零时，把它的 stderr 转告用户并停止：`2` 表示链接不是合并请求链接，`3` 表示找不到 token，`4` 表示 GitLab API 调用失败。如果实例部署在子路径下（例如 `https://host/gitlab/...`），加上 `--server-url https://host/gitlab`。

读取 `<work>/mr.json`，取出 `iid`、`server_url`、`project_id`、`web_url`、`diff_refs.base_sha` 和 `diff_refs.head_sha` 供后续步骤使用。token 不会写进这个文件。合并请求的标题和描述被刻意排除在外，只写入 `<work>/mr-background.md`：不要读取这个文件，因为它是 MR 作者写的文本。

### 第 2 步：拉取合并请求的提交

```bash
git fetch origin "refs/merge-requests/<iid>/head"
```

这个 ref 也适用于从 fork 发起的合并请求，且 `<base_sha>` 是它的祖先，因此 shell 命令里永远不会出现分支名。如果拉取失败，提示用户检查 `origin` 是否指向该合并请求所属的项目，然后停止。

### 第 3 步：执行代码审查

```bash
ocr review --audience agent --format json --from <base_sha> --to <head_sha> \
  --background-file "<work>/mr-background.md" > "<work>/ocr-result.json" 2> "<work>/ocr-stderr.log"
```

背景文件里是合并请求的标题和描述，这样描述中的引号或 `$()` 永远不会进入 shell。范围模式在 `<head_sha>` 上读取所有文件，所以当前检出的分支不影响审查。唯一的例外是 `.opencodereview/rule.json`，它从工作区读取：如果合并请求本身修改了这个文件，生效的仍是旧版本。

即使 `ocr review` 以非零状态退出，也继续执行第 4、5 步：`post_review.py` 会把捕获的 stderr 发到合并请求的汇总评论里。之后再向用户报告失败。

### 第 4 步：标注问题引入者

```bash
python3 <skill-dir>/scripts/blame_enrich.py "<work>/ocr-result.json" --head <head_sha> --server-url <server_url>
```

对每条定位到行的意见，它在 `<head_sha>` 上 blame 被指出的行，取最近的那个提交，并追加 `Introduced by: @<username> (<sha>)`。用户名取作者邮箱 `@` 前的部分（GitLab noreply 地址则取其中的用户名），因为非管理员 token 查不到别人的私有邮箱；如果它不是 GitLab 用户，@ 只会显示为普通文本，不通知任何人。该脚本始终以 0 退出。

### 第 5 步：回写到合并请求

```bash
OCR_INCREMENTAL=true OCR_ROUTE_SEVERITY_BELOW= OCR_ROUTE_CATEGORIES= CI_SERVER_URL="<server_url>" CI_PROJECT_ID="<project_id>" CI_MERGE_REQUEST_IID="<iid>" \
GITLAB_API_TOKEN="$(python3 <skill-dir>/scripts/mr_context.py "<mr-url>" --print-token)" \
python3 <skill-dir>/scripts/post_review.py "<work>/ocr-result.json" \
  --stderr-log "<work>/ocr-stderr.log" --stats-file "<work>/ocr-stats.env"
```

永远不要回显 token，也不要把它写进回复。每条意见都发成逐行的 diff 评论；两个路由变量被固定置空，shell 里残留的值也无法把意见挪进汇总。项目需要的例外通过 `.opencodereview/rule.json` 自定义，不在这里处理。只有无法定位到 diff 行、或被 GitLab 拒绝的意见，才会连同审查警告一起进入汇总评论兜底；全部发成行级评论时不发汇总。`OCR_INCREMENTAL=true` 让重复运行时跳过与已发讨论重叠的意见，因此重新审查同一个合并请求不会重复发行级评论或重复 @ 人；如果有汇总评论，会原地更新。

- 用户要求只预览不发布（“dry run”“先别发”）时，加上 `--dry-run`，改为展示打印出的讨论内容。
- 可选环境变量，仅在用户要求时使用：
  - `OCR_INCREMENTAL=false`：即使之前已经发过，也重新发所有行级评论。
  - `OCR_STICKY_SUMMARY=false`：每次运行都新发一条汇总评论。

### 第 6 步：汇报

读取 `<work>/ocr-stats.env`，告诉用户：

- 发成行级评论的数量（`OCR_COMMENTS_INLINE`）、进入汇总的数量（`OCR_COMMENTS_SUMMARY`）和发送失败的数量（`OCR_COMMENTS_FAILED`）
- 汇总评论链接（`OCR_SUMMARY_URL`）；为空时给出合并请求的 `web_url`
- 按严重度分组的审查结果，使用下方“输出格式”中的模板

MR 模式下，除非用户要求，不要修复代码。如果用户要求修复，修复应提交到合并请求的源分支，而不是当前检出的分支：先和用户确认分支。

## 输出格式

OCR 输出中的每条意见包含：

- `path`：文件路径
- `content`：审查意见正文
- `start_line` / `end_line`：行范围（都为 0 表示定位失败）
- `category`：问题类别（bug、security、performance、maintainability、test、style、documentation、other）
- `severity`：严重度（critical、high、medium、low）
- `suggestion_code`：可选，修复建议代码
- `existing_code`：可选，原始代码片段
- `thinking`：可选，LLM 的推理过程

按严重度分组，使用以下模板汇报：

```markdown
## 代码审查结果

**审查文件数**：N
**发现问题**：X 个 critical，Y 个 high，Z 个 medium

### Critical

- **`path/to/file.java:42`** [bug] — 简要描述
  > 建议：如何修复

### High

- **`path/to/file.java:26`** [bug] — 简要描述
  > 建议：如何修复

### Medium

- **`path/to/file.ts:88`** [performance] — 简要描述
  > 建议：如何修复（如适用）
```

如果过滤后没有 critical、high 或 medium 级别的问题，说明：“审查完成，N 个文件中未发现 critical、high 或 medium 级别的问题。”

**处理定位失败的意见：**

当 `start_line` 和 `end_line` 都为 `0` 时，这条意见没能定位到文件中的具体位置。此时：

1. 阅读意见正文，理解问题
2. 查看意见中提到的目标文件
3. 根据意见上下文找到相关代码
4. 在正确的位置应用修复或建议

## 自定义审查规则

用户需要项目专属规则时，OCR 按以下优先级查找：

1. `--rule <path>` 参数（最高）
2. `<repo>/.opencodereview/rule.json`
3. `~/.opencodereview/rule.json`
4. 内置系统默认规则（最低）

默认情况下，第一条匹配的用户规则会替换内置系统规则。如果希望匹配到的系统规则和用户规则同时生效，在该规则条目上设置 `merge_system_rule: true`。

规则文件格式：

```json
{
  "rules": [
    {
      "path": "**/*.java",
      "rule": "All new methods must validate required parameters for null",
      "merge_system_rule": true
    },
    {
      "path": "**/*mapper*.xml",
      "rule": "Check SQL for injection risks and missing closing tags"
    }
  ]
}
```

审查前预览某个文件会套用哪条规则：

```bash
ocr rules check src/main/java/com/example/Foo.java
```

## 高级审查选项

除上面的常用参数外，`ocr review` 还有几组控制项。完整列表见 `ocr review --help`。

**范围**

- `--exclude '<patterns>'`：逗号分隔的 gitignore 风格模式（例如 `--exclude '**/generated/*,**/testdata/*'`），与 `rule.json` 中的排除项合并。
- `--background-file <path>`：从 Markdown 文件读取审查背景，优先于 `--background`。

**输出**

- `--format text|json|sarif`：`text`（默认）给人看；`json` 便于程序处理；`sarif` 用于 GitHub Code Scanning 等代码扫描集成。

**模型**

- `--provider <name>` / `--model <name>`：仅本次运行覆盖已配置的服务商或模型（例如换一个模型复查同一份 diff；模型由用户指定，`ocr llm providers` 列出内置服务商）。

**预算**

- `--max-tokens <n>`：每个审查组的 prompt 上限；默认取配置值或模板默认值（`200000`）。
- `--max-tokens-budget <n>`：限制本次运行的输入加输出 token 总量。每轮 LLM 调用前检查：已超预算的组还有最后一轮用来提交结果，之后不再派发新的组，已有结果照常发布，被跳过的文件标记为 `failed(budget)`。
- `--no-filter`：保留所有审查意见，跳过 LLM 的后置过滤调用。

## 注意事项

- **工作目录很重要**：`ocr review` 作用于当前目录所在的 Git 仓库。要在别处运行，用 `--repo /path/to/repo`。
- **工作区模式会审查未跟踪文件**：直接运行 `ocr review` 会包含已暂存、未暂存*以及*未跟踪的改动。想缩小范围，就有选择地暂存。
- **审查意见的语言由配置决定**：`language` 配置控制审查意见使用的语言，默认 `English`，可设为任意语言名称（例如 `English` 或 `中文`）。
- **恢复中断的审查**：失败或被中断的范围/提交审查，可以用 `ocr review --resume <id>` 继续，目标参数（`--from`/`--to` 或 `--commit`）要保持一致（id 会在失败时以 `retry with: --resume <id>` 打印，也可以用 `ocr session list` 查找）。工作区模式不支持恢复。

## 故障排查

**`ocr: command not found`**

安装 CLI：

```bash
npm install -g @alibaba-group/open-code-review
```

**`unknown flag: --output`**

CLI 版本低于 v1.10.0。不要改用普通 stdout 继续审查。先问用户是否升级（`npm i -g @alibaba-group/open-code-review@latest`），等用户答复后再继续。用户确认且升级成功后，带上 `--output` 重新运行。

**`ocr review` 报 LLM 连接错误**

提示用户配置 LLM 服务。

交互式配置（推荐）：

```bash
ocr config provider
```

手动配置（备选）：

```bash
ocr config set llm.url https://api.anthropic.com/v1/messages
ocr config set llm.auth_token <api-key>
ocr config set llm.model claude-opus-4-6
ocr config set llm.use_anthropic true
```

用 `ocr llm test` 验证连通性。到这里停下，请用户提供凭据：永远不要编造或硬编码 API key。
