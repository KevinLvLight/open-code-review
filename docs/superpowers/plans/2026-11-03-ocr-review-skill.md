# ocr-review Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `ocr-review` Skill。它在原 open-code-review Skill 的本地审查能力之上，增加 GitLab MR 审查，并以 diff notes 加 suggestion 的形式自动回写，每条评论标注问题引入者（@ 对应用户）。

**Architecture:**
- SKILL.md 负责编排：判断模式，再依次调用三个只用 Python 标准库的脚本。
  - `mr_context.py`：解析 MR 链接，读取 MR 元数据
  - `blame_enrich.py`：用 git blame 找到引入者，并把用户 @ 写进评论
  - `post_review.py`：从 `examples/gitlab_ci/` 原样拷贝，负责回写 diff notes 和 suggestion
- 审查本身仍由 `ocr review --from <base_sha> --to <head_sha>` 在本地仓库完成。

**Tech Stack:** Python 3（仅标准库：`argparse`、`json`、`re`、`subprocess`、`urllib`、`unittest`），git，`ocr` CLI，可选 `glab`。

**Spec:** `docs/superpowers/specs/2026-11-03-ocr-review-skill-design.md`

## Global Constraints

- 所有脚本只用 Python 标准库，不新增依赖。
- 新建的 `.py` 文件第 1 行为 `#!/usr/bin/env python3`，随后是 SPDX 头，用 `make license-add` 添加。
- `.py` 中的标识符、注释、字符串全部用英文（`make english-check` 会检查）。引入者文案固定为 `Introduced by: @<username> (<short_sha>)`；查不到用户名时为 `Introduced by: <author_name> (<short_sha>)`。
- `short_sha` 取提交 SHA 的前 8 位。
- Token 获取顺序：`GITLAB_TOKEN` → `GITLAB_API_TOKEN` → `glab config get token --host <host>`（仅在 `glab` 存在时尝试）。Token 不写入任何文件或 stdout，唯一例外是 `--print-token`。
- API 请求头用 `PRIVATE-TOKEN: <token>`，接口前缀为 `<server_url>/api/v4`。
- MR 模式必须在该项目的本地 clone 里运行，并要求 `origin` 指向该项目；对当前所在分支没有要求。不做自动 clone。
- 原 `skills/open-code-review/` 不做任何修改。
- 提交信息用英文，并保持简短。按 AGENTS.md 要求，**不加** `Co-Authored-By` 或任何 AI 署名 trailer；PR 描述中需披露 AI 使用情况。
- 文本文件必须是 LF 换行。
- 运行测试：`python3 -m unittest discover -s skills/ocr-review/scripts -p '*_test.py'`

## Review Focus

1. **MR 链接带有尾巴**（`/diffs`、`?tab=overview`、`#note_123`），或项目位于多级子组下（`a/b/c/proj`）。这些情况都应解析出正确的 path 和 iid。测试见 Task 1：`test_parse_trailing_parts`、`test_parse_nested_groups`。
2. **评论的行号超出文件长度，或文件在 head 中已被删除或重命名**，此时 `git blame` 会报错。这种情况应只跳过这一条评论，不影响其他评论。测试见 Task 2：`test_enrich_skips_when_blame_fails`。
3. **token 没有调用用户查询接口的权限**（`/users` 返回 403），或网络错误。这时不 @ 任何人，改为输出作者姓名，程序不能崩溃。测试见 Task 2：`test_lookup_http_error_returns_none`。
4. **MR 描述里含有引号、反引号、`$()` 或超长文本**。这些内容通过 `--background-file` 传递，不会进入 shell，写入的文件长度不超过 2000 字符。测试见 Task 1：`test_background_file_truncated_and_verbatim`。
5. **`/users?search=` 返回多个相似用户**，例如 `bob@x.com` 也匹配到 `bob@x.com.cn`。只有精确匹配且唯一时才 @。测试见 Task 2：`test_lookup_requires_unique_exact_match`。

---

### Task 1: mr_context.py

**Files:**
- Create: `skills/ocr-review/scripts/mr_context.py`
- Test: `skills/ocr-review/scripts/mr_context_test.py`
- Also: 新建分支，并把 spec 和本计划随第一个提交一起提交

**Interfaces:**
- Produces（Task 2 会 import 这些接口）：
  - `class MRRef(NamedTuple): server_url: str; project_path: str; iid: int`
  - `class GitLabError(Exception)`：包含属性 `status: int`（网络错误时为 0）
  - `parse_mr_url(url: str, server_url: Optional[str] = None) -> MRRef`：解析失败时抛出 `ValueError`
  - `resolve_token(host: str, env: Mapping[str, str], which=shutil.which, run=subprocess.run) -> str`：取不到时返回 `""`
  - `api_get(server_url: str, path: str, token: str, opener=urllib.request.urlopen) -> Any`：返回解析后的 JSON；HTTP 或网络错误时抛出 `GitLabError`
  - `fetch_mr(ref: MRRef, token: str, opener=...) -> dict`：返回的键为 `server_url, project_path, project_id, iid, title, description, target_branch, web_url, diff_refs`
  - `background_text(title: str, description: str, limit: int = 2000) -> str`
- CLI：`python3 mr_context.py <url> [--server-url URL] [--print-token] [--background-out PATH]`
  - 默认在 stdout 输出 `fetch_mr` 结果的 JSON
  - `--print-token` 只输出 token
  - 退出码：0 成功，2 链接解析失败，3 没有 token，4 API 错误

- [ ] **Step 1: 建分支并提交 spec 和计划**

```bash
git checkout -b feat/ocr-review-skill
git add docs/superpowers/specs/2026-11-03-ocr-review-skill-design.md docs/superpowers/plans/2026-11-03-ocr-review-skill.md
git commit -m "docs: add ocr-review skill design and plan"
```

- [ ] **Step 2: 写失败的测试**

与 `examples/gitlab_ci/post_review_test.py` 风格一致：用 `unittest`，测试文件 `import mr_context`，`opener` 和 `run` 用假对象注入。

```python
def test_parse_gitlab_com(self):
    r = parse_mr_url("https://gitlab.com/grp/proj/-/merge_requests/12")
    self.assertEqual(r, MRRef("https://gitlab.com", "grp/proj", 12))

def test_parse_nested_groups(self):
    r = parse_mr_url("https://git.corp.com/a/b/c/proj/-/merge_requests/7")
    self.assertEqual((r.server_url, r.project_path, r.iid), ("https://git.corp.com", "a/b/c/proj", 7))

def test_parse_trailing_parts(self):
    for u in ["https://h.com/g/p/-/merge_requests/3/diffs",
              "https://h.com/g/p/-/merge_requests/3?tab=overview",
              "https://h.com/g/p/-/merge_requests/3#note_99"]:
        self.assertEqual(parse_mr_url(u), MRRef("https://h.com", "g/p", 3))

def test_parse_server_url_override_with_subpath(self):
    r = parse_mr_url("https://h.com/gitlab/g/p/-/merge_requests/5", server_url="https://h.com/gitlab")
    self.assertEqual(r, MRRef("https://h.com/gitlab", "g/p", 5))

def test_parse_invalid_raises(self):
    for u in ["https://h.com/g/p/-/issues/1", "not a url", "https://h.com/g/p/-/merge_requests/abc"]:
        with self.assertRaises(ValueError): parse_mr_url(u)

def test_token_order(self):
    self.assertEqual(resolve_token("h.com", {"GITLAB_TOKEN": "a", "GITLAB_API_TOKEN": "b"}), "a")
    self.assertEqual(resolve_token("h.com", {"GITLAB_API_TOKEN": "b"}), "b")

def test_token_from_glab(self):
    # which returns a path; run returns CompletedProcess(stdout="glab-tok\n", returncode=0)
    # assert called with ["glab", "config", "get", "token", "--host", "h.com"]; result == "glab-tok"

def test_token_missing(self):
    self.assertEqual(resolve_token("h.com", {}, which=lambda _: None), "")

def test_fetch_mr_shapes_output(self):
    # fake opener returns MR JSON with id, project_id=42, iid=12, title, description,
    # target_branch, web_url, diff_refs; assert the request URL is
    # https://gitlab.com/api/v4/projects/grp%2Fproj/merge_requests/12 and the header is PRIVATE-TOKEN,
    # and the returned dict has exactly the nine documented keys with project_id == 42

def test_api_get_404_raises(self):
    # opener raises HTTPError 404 -> GitLabError with status 404

def test_background_file_truncated_and_verbatim(self):
    t = background_text("Fix `x` $(rm)", "d" * 5000)
    self.assertTrue(t.startswith("Fix `x` $(rm)\n\n"))
    self.assertLessEqual(len(t), 2000)

def test_main_exit_codes(self):
    # bad url -> 2; valid url with empty env and no glab -> 3; API GitLabError -> 4
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `python3 -m unittest discover -s skills/ocr-review/scripts -p 'mr_context_test.py'`
Expected: 失败，原因是 `ModuleNotFoundError: mr_context`

- [ ] **Step 4: 实现 `mr_context.py`**

- `parse_mr_url`：
  - 用 `urllib.parse.urlsplit` 解析 URL。
  - 如果 path 中不含 `/-/merge_requests/`，抛出 `ValueError`。
  - 否则在这个分隔符处切开：前半段去掉首尾的 `/` 后作为 project path；后半段开头的数字作为 iid，必须是纯数字。
  - 传入 `server_url` 时，从 URL path 的开头去掉它的 path 部分。
- `fetch_mr`：project path 用 `urllib.parse.quote(path, safe="")` 编码。
- `main` 的错误信息：
  - 退出码 2：`error: not a GitLab merge request URL: <url> (expected https://<host>/<group>/<project>/-/merge_requests/<iid>)`
  - 退出码 3：`error: no GitLab token. Run 'glab auth login --hostname <host>' or set GITLAB_TOKEN (api scope).`
  - 退出码 4：
    - 401/403 时为 `error: GitLab API returned <status>; the token needs api scope to post comments.`
    - 404 时为 `error: merge request not found or not visible to this token.`
    - 其余为 `error: GitLab API request failed: <msg>`
- `--background-out PATH`：把 `background_text(title, description)` 写入 PATH，按需创建父目录。

- [ ] **Step 5: 运行测试，确认通过**

Run: 同 Step 3
Expected: 全部 OK

- [ ] **Step 6: 添加 license 头，跑检查，提交**

```bash
make license-add && make english-check
git add skills/ocr-review/scripts/mr_context.py skills/ocr-review/scripts/mr_context_test.py
git commit -m "feat(skill): add mr_context helper for ocr-review"
```

---

### Task 2: blame_enrich.py

**Files:**
- Create: `skills/ocr-review/scripts/blame_enrich.py`
- Test: `skills/ocr-review/scripts/blame_enrich_test.py`

**Interfaces:**
- Consumes: `mr_context.resolve_token`、`mr_context.api_get`、`mr_context.GitLabError`（通过同目录 import）
- Produces:
  - `parse_blame_porcelain(text: str) -> list[dict]`：每个提交对应一个元素，键为 `sha, author, author_mail, committer_time:int`
  - `newest(commits: list[dict]) -> Optional[dict]`：返回 `committer_time` 最大的元素
  - `username_from_noreply(email: str) -> Optional[str]`：匹配 `^\d+-([^@]+)@users\.noreply\.`，不区分大小写
  - `class UserLookup(server_url: str, token: str, opener=urllib.request.urlopen)`，提供方法 `username(email: str) -> Optional[str]`，结果按邮箱缓存
  - `introducer_line(commit: dict, username: Optional[str]) -> str`
  - `enrich(result: dict, head: str, blame: Callable[[str, str, int, int], Optional[str]], lookup: Callable[[str], Optional[str]]) -> int`：返回补充了引入者的评论条数
- CLI：`python3 blame_enrich.py <result.json> --head <sha> --server-url <url>`，原地改写文件，任何情况下退出码都是 0。

- [ ] **Step 1: 写失败的测试**

```python
PORCELAIN = ("aaaaaaaa11111111 3 3 1\nauthor Alice\nauthor-mail <alice@x.com>\ncommitter-time 100\n\tline\n"
             "bbbbbbbb22222222 4 4 1\nauthor Bob\nauthor-mail <bob@x.com>\ncommitter-time 200\n\tline\n"
             "aaaaaaaa11111111 5 5\n\tline\n")

def test_parse_porcelain_dedupes_commits(self):
    cs = parse_blame_porcelain(PORCELAIN)
    self.assertEqual({c["sha"] for c in cs}, {"aaaaaaaa11111111", "bbbbbbbb22222222"})
    self.assertEqual(next(c for c in cs if c["author"] == "Bob")["author_mail"], "bob@x.com")

def test_newest_picks_latest_committer_time(self):
    self.assertEqual(newest(parse_blame_porcelain(PORCELAIN))["author"], "Bob")

def test_noreply(self):
    self.assertEqual(username_from_noreply("123-jdoe@users.noreply.gitlab.com"), "jdoe")
    self.assertEqual(username_from_noreply("jdoe@corp.com"), None)

def test_lookup_requires_unique_exact_match(self):
    # opener returns [{"username":"bob","public_email":"bob@x.com"},{"username":"bob2","public_email":"bob@x.com.cn"}]
    # -> "bob"; two entries both exactly matching -> None; match is case-insensitive on "Bob@X.com"

def test_lookup_caches_per_email(self):
    # two calls with same email -> opener called once

def test_lookup_http_error_returns_none(self):
    # opener raises HTTPError 403 -> None, no exception

def test_introducer_line(self):
    c = {"sha": "bbbbbbbb22222222", "author": "Bob"}
    self.assertEqual(introducer_line(c, "bob"), "Introduced by: @bob (bbbbbbbb)")
    self.assertEqual(introducer_line(c, None), "Introduced by: Bob (bbbbbbbb)")

def test_enrich_appends_and_counts(self):
    result = {"comments": [{"path": "a.go", "start_line": 3, "end_line": 5, "content": "bug"}]}
    n = enrich(result, "HEAD", blame=lambda *a: PORCELAIN, lookup=lambda e: "bob")
    self.assertEqual(n, 1)
    self.assertEqual(result["comments"][0]["content"], "bug\n\nIntroduced by: @bob (bbbbbbbb)")

def test_enrich_skips_zero_lines(self):
    # start_line == end_line == 0 -> blame not called, content unchanged

def test_enrich_skips_when_blame_fails(self):
    # blame returns None for first comment, PORCELAIN for second -> n == 1, first unchanged

def test_main_leaves_broken_json_untouched(self):
    # write "{not json" to a temp file; main([...]) returns 0; file bytes unchanged
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `python3 -m unittest discover -s skills/ocr-review/scripts -p 'blame_enrich_test.py'`
Expected: 失败，原因是 `ModuleNotFoundError: blame_enrich`

- [ ] **Step 3: 实现 `blame_enrich.py`**

- **默认的 `blame` 实现**：执行 `git blame --porcelain -L <start>,<end> <head> -- <path>`。返回码非 0 时返回 `None`，不抛异常。
- **`lookup` 的顺序**：先用 `username_from_noreply`；有 token 时再用 `UserLookup.username`；都没有结果则返回 `None`。
  - `UserLookup` 请求的是 `GET /users?search=<quote(email)>`。
  - 只接受 `public_email` 或 `email` 与目标邮箱（忽略大小写）完全相等、而且只有一条匹配的结果。
  - 遇到 `GitLabError` 时返回 `None`。
- **`main`**：
  - token 用 `resolve_token(urlsplit(server_url).hostname, os.environ)` 获取；
  - 读不到 JSON，或者 JSON 里没有 `comments` 列表，就在 stderr 打一条警告，然后返回 0，不改动文件；
  - 读取成功时用 `json.dump(..., ensure_ascii=False, indent=2)` 写回原文件，并在 stderr 打印 `Introducer added to <n>/<total> comment(s)`。
  - 单条评论处理失败时，打印 `warning: <path>:<start>-<end>: <reason>` 并跳过这一条。

- [ ] **Step 4: 运行测试，确认通过**

Run: `python3 -m unittest discover -s skills/ocr-review/scripts -p '*_test.py'`
Expected: 全部 OK

- [ ] **Step 5: 添加 license 头、检查并提交**

```bash
make license-add && make english-check
git add skills/ocr-review/scripts/blame_enrich.py skills/ocr-review/scripts/blame_enrich_test.py
git commit -m "feat(skill): add blame_enrich to tag comment introducers"
```

---

### Task 3: 拷贝 post_review.py，并加同步校验

**Files:**
- Create: `skills/ocr-review/scripts/post_review.py`（从 `examples/gitlab_ci/post_review.py` 拷贝）
- Test: `skills/ocr-review/scripts/sync_test.py`

**Interfaces:**
- Produces: `post_review.py` 的 CLI 与原件一致。它读取的环境变量为 `CI_SERVER_URL, CI_PROJECT_ID, CI_MERGE_REQUEST_IID, GITLAB_API_TOKEN`，参数为 `[input] [--dry-run]`。

- [ ] **Step 1: 写失败的测试**

```python
SYNC_NOTE = "# Copied verbatim from examples/gitlab_ci/post_review.py; edit the original and re-copy.\n"

def test_post_review_matches_example(self):
    here = Path(__file__).resolve().parent
    original = here.parents[2] / "examples" / "gitlab_ci" / "post_review.py"
    if not original.exists():
        self.skipTest("standalone skill install; examples/ not present")
    copy = (here / "post_review.py").read_text()
    self.assertIn(SYNC_NOTE, copy)
    self.assertEqual(copy.replace(SYNC_NOTE, "", 1), original.read_text())
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `python3 -m unittest discover -s skills/ocr-review/scripts -p 'sync_test.py'`
Expected: 失败，原因是 `FileNotFoundError ... post_review.py`

- [ ] **Step 3: 拷贝文件，并在 SPDX 头之后插入 `SYNC_NOTE` 一行**

```bash
cp examples/gitlab_ci/post_review.py skills/ocr-review/scripts/post_review.py
```

把 `SYNC_NOTE` 插在原件第 4 行（Copyright 行）之后，其余内容一个字节都不改。

- [ ] **Step 4: 运行全部测试，确认通过**

Run: `python3 -m unittest discover -s skills/ocr-review/scripts -p '*_test.py'`
Expected: 全部 OK

- [ ] **Step 5: 提交**

```bash
make license-add && make english-check
git add skills/ocr-review/scripts/post_review.py skills/ocr-review/scripts/sync_test.py
git commit -m "feat(skill): vendor post_review.py into ocr-review"
```

---

### Task 4: SKILL.md 与 plugin 镜像

**Files:**
- Create: `skills/ocr-review/SKILL.md`
- Create: `plugins/open-code-review/skills/ocr-review/SKILL.md`
- Create: `plugins/open-code-review/skills/ocr-review/scripts/`，内容与 `skills/ocr-review/scripts/` 下的非测试文件相同，即 `mr_context.py`、`blame_enrich.py`、`post_review.py`

**Interfaces:**
- Consumes: Task 1–3 中的三个 CLI 及其参数和退出码。

- [ ] **Step 1: 写 `skills/ocr-review/SKILL.md`**

- **frontmatter**：
  - `name: ocr-review`
  - `description`：同时写明两件事：
    - 审查本地变更：工作区、提交、分支对比；
    - 用户给出 GitLab merge request 链接时，审查该 MR，并把行内评论、suggestion 和引入者自动发回 MR。
  - `license`、`compatibility`、`metadata` 照抄原 Skill。`compatibility` 里补一句：MR 模式需要 `python3`，以及 `GITLAB_TOKEN` 或 `glab` 登录。
- **正文结构**：
  1. 一行同步说明：`Local-mode sections mirror skills/open-code-review/SKILL.md; keep them in sync.`
  2. `## Mode Selection`：输入中有 `/-/merge_requests/<iid>` 链接时进入 MR 模式，否则进入本地模式。
  3. `## Local Mode`：原 SKILL.md 从 `## Workflow` 到 `## References` 的全部内容原样搬过来，只把标题层级下调一级。
  4. `## MR Mode`：spec 中“MR 模式”的 6 步。命令里的脚本路径写成 `<skill-dir>/scripts/...`，并说明 `<skill-dir>` 指本 SKILL.md 所在的目录。要点：
     - 第 1 步：
       ```bash
       python3 <skill-dir>/scripts/mr_context.py <url> --background-out .ocr/mr-background.md > .ocr/mr.json
       ```
       退出码非 0 时，原样转述 stderr 给用户，然后停止。
     - 第 2 步：`git fetch origin "refs/merge-requests/<iid>/head" "<target_branch>"`。失败时提示用户检查 `origin` 是否指向这个项目，然后停止。
     - 第 3 步：使用 `--background-file .ocr/mr-background.md`。不论 `ocr` 的退出码是什么，都继续执行第 4、5 步。
     - 第 5 步：token 用 `$(python3 <skill-dir>/scripts/mr_context.py <url> --print-token)` 内联到命令里。不得 echo token，也不得把它写进回复。
       - 用户说“只看不发”或“dry run”时，加 `--dry-run`。
       - 用户要求进一步调整时，可设置环境变量 `OCR_INCREMENTAL`、`OCR_ROUTE_SEVERITY_BELOW`，取值含义见 `examples/gitlab_ci/README.md`。
     - 第 6 步：汇报以下内容：
       - 读取 `.ocr/ocr-stats.env`，报告已发出的行内评论数和降级为普通 note 的数量；
       - 总结评论的 URL；
       - 按严重度列出的摘要。
     - 注意事项：
       - 只要求在项目的 clone 中运行，不要求切换分支；
       - `.opencodereview/rule.json` 读取的是当前工作区里的版本；
       - 如果用户之后要求修复问题，在 MR 的源分支上修改，不要在当前分支上改。
- 正文全部用英文，与原 Skill 保持一致。

- [ ] **Step 2: 生成 plugin 镜像**

- 把 SKILL.md 拷贝到 plugin 目录，并在第一个 `# ` 标题之后插入一段与原镜像同样措辞的说明，只把路径换成 `skills/ocr-review/SKILL.md`。原镜像 `plugins/open-code-review/skills/open-code-review/SKILL.md` 中对应的是第 25–29 行。
- 把三个非测试脚本拷贝到镜像的 `scripts/` 目录下。

- [ ] **Step 3: 验证镜像一致**

Run:
```bash
diff skills/ocr-review/SKILL.md plugins/open-code-review/skills/ocr-review/SKILL.md
for f in mr_context.py blame_enrich.py post_review.py; do cmp skills/ocr-review/scripts/$f plugins/open-code-review/skills/ocr-review/scripts/$f; done
```
Expected:
- `diff` 只输出新增的那段镜像说明；
- `cmp` 没有任何输出。

- [ ] **Step 4: 运行检查并提交**

```bash
make license-add && make english-check && make license-check
git add skills/ocr-review/SKILL.md plugins/open-code-review/skills/ocr-review
git commit -m "feat(skill): add ocr-review skill with GitLab MR mode"
```

---

### Task 5: 端到端验证与提交前审查

**Files:** 不改动文件；如发现问题，回到对应 Task 修复。

- [ ] **Step 1: 本地模式冒烟测试**

在任一有改动的仓库中，按 `Local Mode` 的说明运行 `ocr review --audience agent -b "..."`。
Expected：行为与原 Skill 相同，不触发 blame，也不调用任何 GitLab API。

- [ ] **Step 2: MR 模式 dry-run**

在一个 GitLab 测试项目的 clone 中，故意先切到一个与 MR 无关的分支，然后对一个测试 MR 跑完第 1–5 步，第 5 步带上 `--dry-run`。
Expected：
- 输出的 discussion 带有 position；
- 有 `suggestion_code` 的评论里包含 `` ```suggestion:-N+0 `` 块；
- 每条定位成功的评论末尾都有一行 `Introduced by:`。

- [ ] **Step 3: 真实回写两次**

去掉 `--dry-run` 跑一次，然后原样再跑一次。
Expected：
- MR 上出现行内 Discussion 和一条总结；
- 第二次运行只更新同一条总结，不会新增重复的 Discussion；
- 被 @ 的用户收到通知。

- [ ] **Step 4: 按 AGENTS.md 的要求做提交前审查**

```bash
ocr review --audience agent --background "Add ocr-review skill: local review plus GitLab MR review with diff notes, suggestions and blame-based introducer mentions"
```
按严重度处理审查结果。需要修改的，回到对应 Task 改完，再单独提交。

- [ ] **Step 5: 跑完整测试并做最终检查**

```bash
python3 -m unittest discover -s skills/ocr-review/scripts -p '*_test.py'
make check
git add --renormalize . && git status --short
```
Expected：
- 测试全部 OK；
- `make check` 打印 `check passed`；
- `git status --short` 没有任何输出，说明换行符没有变化。
