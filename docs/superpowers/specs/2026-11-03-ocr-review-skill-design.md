# ocr-review Skill 设计

日期：2026-11-03
状态：待审阅

## 背景与目标

现有的 `skills/open-code-review` Skill 只审查本地 git 变更，不读取 MR 信息，也不回写评论。`ocr` CLI 本身同样没有 MR 能力，MR 联动只存在于 CI 示例中（`examples/gitlab_ci/`）。

本设计新增一个 Skill：`ocr-review`。名称刻意不带 GitLab 或 MR，以免让人误以为它只能扫描 MR；SKILL.md 的 description 中同时写明“审查本地变更”和“审查 GitLab MR 并回写评论”两种触发场景。

- **使用场景：** 开发者在本地 Agent（如 Claude Code）中给出 GitLab MR 链接（gitlab.com 或自建实例）。Skill 拉取 MR 的提交并运行审查，然后**自动**把结果回写到 MR。
- **回写形式：**
  - **diff notes**：带 position 的行内 Discussion
  - **suggestion**：GitLab 原生 ` ```suggestion ` 块，可一键采纳
  - **问题引入者**：通过 git blame 确定，并 @ 对应的 GitLab 用户
- **兼容性：** 用户没有给出 MR 时，行为与原 Skill 完全一致，即审查本地变更、不回写。新 Skill 是原 Skill 的超集。
- **成功标准：** 给出 `https://<host>/<group>/<project>/-/merge_requests/<iid>` 后，MR 上出现定位到行的 Discussion 和一条总结。每条评论附带引入者；有建议代码的评论附带 suggestion。重复运行不会刷出重复评论。

### 非目标

- 不修改 `ocr` Go CLI，不新增 `--mr` 参数。
- 不修改原 `skills/open-code-review` Skill。
- 不支持 GitHub、Codeup 等其他平台。
- 不自动 clone。MR 模式必须在该项目的本地 clone 中运行，且 `origin` 指向该项目；当前处在哪个分支不限。
- 不接入仓库 CI。

## 方案选择

采用**方案 A：由 Skill 编排流程，回写复用 `examples/gitlab_ci/post_review.py`**。

`post_review.py` 只依赖 Python 标准库，由环境变量驱动，并且已有 1716 行测试覆盖。它已实现：

- 通过 `versions` 接口取得 `base_sha`/`start_sha`/`head_sha`，把评论发成 diff notes；定位失败时降级为普通 note
- 当 `existing_code` 与 `suggestion_code` 同时存在时，生成 ` ```suggestion:-N+0 ` 块（`post_review.py:264-282`）
- 置顶总结（sticky summary）、增量去重、按严重度把评论分流到总结里，以及限流重试
- `--dry-run` 预览

放弃的方案：

- **B：纯 SKILL.md，由 Agent 自行拼 position 并调 API。** 行定位、去重和限流都依赖 Agent 临场处理，可靠性达不到自动回写的要求。
- **C：给 CLI 加 `--mr` 参数。** 改动上游接口，超出本次范围。

## 目录结构

```
skills/ocr-review/
├── SKILL.md
└── scripts/
    ├── mr_context.py        # 解析 MR 链接，获取 token，读取 MR 元数据
    ├── mr_context_test.py
    ├── blame_enrich.py      # git blame 加用户名查询，把引入者写入评论
    ├── blame_enrich_test.py
    └── post_review.py       # 从 examples/gitlab_ci/post_review.py 原样拷贝，不做修改
```

- 在 `plugins/open-code-review/skills/ocr-review/` 放一份镜像，规则与现有 Skill 的镜像相同。
- 所有脚本只用 Python 3 标准库，并带 SPDX 头。新增的 `.py` 文件用 `make license-add` 添加头部。
- `post_review.py` 文件头加一行注释，说明它拷贝自 `examples/gitlab_ci/post_review.py`，修改应先改原件再同步。SKILL.md 中写明它的本地审查章节需要和 `skills/open-code-review/SKILL.md` 同步。

## SKILL.md 流程

### 模式判断

- 用户输入包含 GitLab MR 链接，或在已知 GitLab 项目中使用 `!<iid>` 写法时，进入 **MR 模式**。
- 否则进入**本地模式**。本地模式的内容从原 SKILL.md 原样保留：收集背景、`ocr review --audience agent -b ...`、按严重度汇报、仅在用户明确要求时修复。本地模式不做 blame，也不回写。

### MR 模式

1. **解析 MR 上下文**：运行 `python3 scripts/mr_context.py <MR链接>`，输出如下 JSON：
   ```json
   {"server_url": "...", "project_path": "group/sub/proj", "project_id": 123, "iid": 45,
    "title": "...", "description": "...", "target_branch": "main", "web_url": "...",
    "diff_refs": {"base_sha": "...", "start_sha": "...", "head_sha": "..."}}
   ```
   token 不写入输出。后续步骤需要 token 时，各脚本按同一顺序自行获取，见“token 获取”。
2. **拉取提交**：
   ```bash
   git fetch origin "refs/merge-requests/<iid>/head" "<target_branch>"
   ```
   使用 `refs/merge-requests/<iid>/head` 是为了让 fork MR 也能拉到。该命令不切换当前分支，也不改动工作区。
   因为范围模式下 `file_read`、`file_find`、`code_search` 都按 `--to` 那个提交读取内容，本地处在任何分支、工作区有未提交改动，都不影响审查结果。
   唯一的例外是项目规则：`.opencodereview/rule.json` 通过 `os.ReadFile` 从当前工作区读取（`internal/config/rules/system_rules.go`）。如果 MR 本身修改了规则文件，本次审查仍按当前分支里的旧规则进行。
3. **审查**：
   ```bash
   mkdir -p .ocr
   ocr review --from <base_sha> --to <head_sha> --format json --audience agent \
     --background-file .ocr/mr-background.md > .ocr/ocr-result.json 2> .ocr/ocr-stderr.log
   ```
   `.ocr/mr-background.md` 由第 1 步的 `mr_context.py --background-out` 写出，内容是标题加描述，总长截断到 2000 字符（即 `ocr` 的软上限）。MR 描述里可能含有引号、反引号或 `$()`，用文件传递是为了避免把它拼进 shell 命令。
4. **补充引入者**：
   ```bash
   python3 scripts/blame_enrich.py .ocr/ocr-result.json \
     --head <head_sha> --server-url <server_url>
   ```
   脚本原地改写这个文件。
5. **回写**：
   ```bash
   CI_SERVER_URL=<server_url> CI_PROJECT_ID=<project_id> CI_MERGE_REQUEST_IID=<iid> \
   GITLAB_API_TOKEN=<token> python3 scripts/post_review.py .ocr/ocr-result.json
   ```
   - token 由 Agent 通过 `python3 scripts/mr_context.py --print-token <MR链接>` 取得，并直接放进这一条命令的环境变量。Agent 不应在回复中展示 token。
   - `OCR_STICKY_SUMMARY` 默认为 true，重复运行时会就地更新总结。
   - 如果用户要求“只看不发”，就加 `--dry-run`。
6. **汇报**：向用户报告以下内容：
   - 行内评论数和降级为普通 note 的数量，取自 `post_review.py` 的输出和 `.ocr/ocr-stats.env`
   - 总结评论的链接
   - 按严重度列出的评论摘要

## 组件设计

### mr_context.py

- **链接解析**：
  - 用 `urllib.parse` 拆出 scheme、host 和 path，再按 `/-/merge_requests/` 把 path 分成两段：前一段是项目路径，后一段开头的数字是 iid。允许任意尾部，例如 `/diffs` 或 `#note_1`。
  - `server_url` 取 scheme 加 host。如果自建实例部署在子路径下（如 `https://h/gitlab/...`），无法从链接区分子路径和组路径，因此支持用 `--server-url` 显式覆盖。
  - 解析失败时退出码为 2，并给出示例链接。
- **token 获取**：只要拿到非空值就停止。
  1. 环境变量 `GITLAB_TOKEN`
  2. 环境变量 `GITLAB_API_TOKEN`，与 CI 示例保持一致
  3. 安装了 `glab` 时，执行 `glab config get token --host <host>`

  三者都拿不到时退出码为 3，提示运行 `glab auth login --hostname <host>` 或设置 `GITLAB_TOKEN`。
- **API 调用**：`GET <server_url>/api/v4/projects/<urlencoded path>/merge_requests/<iid>`，请求头为 `PRIVATE-TOKEN`。
  - 401/403：提示 token 权限不足，需要 `api` scope 才能回写。
  - 404：提示 MR 不存在，或 token 无权访问。
- **`--print-token`**：只输出 token，供第 5 步使用。

### blame_enrich.py

- **输入**：OCR 的 JSON 文件（`comments[]` 中的字段包括 `path`、`start_line`、`end_line`、`content`），以及 `--head` 和 `--server-url`。
- **对每条评论**：
  1. 如果 `start_line == end_line == 0`，或文件在 `head` 中不存在，就跳过，不写引入者。
  2. 运行 `git blame --porcelain -L <start>,<end> <head> -- <path>` 并解析。如果这几行来自多个提交，取 `committer-time` **最新**的那个提交。
  3. 用作者邮箱查询用户名：调用 `GET /api/v4/users?search=<email>`，只接受 `public_email` 或 `email` 与邮箱精确匹配（忽略大小写）的唯一结果。邮箱是 GitLab noreply 地址（`<id>-<username>@users.noreply.<host>`）时，直接从地址中解析出 username。同一个邮箱在单次运行中只查询一次。
  4. 在 `content` 末尾追加一行（文案为英文，原因见“合规事项”）：
     - 能查到用户名：`\n\nIntroduced by: @<username> (<short_sha>)`
     - 查不到用户名：`\n\nIntroduced by: <author_name> (<short_sha>)`，不 @ 任何人
- 不区分引入提交是否在本 MR 范围内，一律标注 blame 作者。用户已确认这一点，接受因此 @ 到历史代码作者。
- 任何一条评论处理失败（blame 出错、API 出错），都只跳过这一条，在 stderr 记录警告，退出码仍为 0。
- 输入 JSON 解析失败时，保持文件不变并以 0 退出，交给 `post_review.py` 的错误路径处理：它会把 stderr 发到 MR 总结里。

## 错误处理汇总

| 情况 | 处理 |
|---|---|
| 链接解析失败或缺少 token | `mr_context.py` 非零退出，Skill 中止并给出配置提示 |
| `git fetch` 失败 | 中止，提示检查 `origin` 是否指向该项目 |
| `ocr review` 非零退出 | 仍然执行 `post_review.py`，由它把 stderr 发到 MR 总结；之后向用户报告失败 |
| blame 或用户查询失败 | 该条评论不写引入者，其余照常 |
| 评论无法定位到 diff 行 | `post_review.py` 降级为普通 note |
| 回写 API 失败 | `post_review.py` 内部重试；最终失败时报告退出码 |

## 测试

- `mr_context_test.py`（`unittest`，网络调用用 mock）：
  - 链接解析：gitlab.com、自建域名、多级子组、`/diffs` 后缀、`#note_` 锚点、非法链接
  - token 获取的优先级
  - 404/401 的错误提示
- `blame_enrich_test.py`：
  - porcelain 输出解析与“取最新提交”的规则
  - noreply 邮箱直接解析出 username
  - 用户名查询的精确匹配与降级
  - 跳过行号为 0 的评论
  - 单条失败不影响其余评论
  - 输入 JSON 损坏时文件保持不变
- 运行方式：`python3 -m unittest discover -s skills/ocr-review/scripts -p '*_test.py'`
- 端到端验证：
  1. 对测试 MR 运行完整流程，`post_review.py` 加 `--dry-run`，检查行内评论、suggestion 和引入者。
  2. 去掉 `--dry-run` 真实回写一次；再运行一次，确认总结被就地更新、没有重复的 Discussion。

## 合规事项

- 新文件带 SPDX 头。Python 源文件中的标识符、注释和字符串必须为英文，`make english-check` 会扫描 `.py`。因此引入者文案用英文 `Introduced by:`。如需中文，后续可加 `--label` 参数，由 SKILL.md 传入。
- 文本文件使用 LF 换行。
- 提交前按 AGENTS.md 要求运行 `ocr review --audience agent --background ...`，并在 PR 中披露 AI 使用情况。
