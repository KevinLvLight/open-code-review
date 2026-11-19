---
title: 委托模式
sidebar:
  order: 5
---

OCR 负责确定性工程（文件筛选、规则解析），宿主 Agent 使用自身的 LLM 能力执行实际的代码审查。OCR 端无需配置 LLM。

## 何时使用委托模式

委托模式专为订阅制 AI 编码代理设计 — 如 Claude Code、Codex、Cursor、Open Code、Qoder 等。这些工具已内置 LLM 订阅额度，使用委托模式可以直接复用宿主 Agent 的订阅额度进行代码审查，无需额外配置模型或 API Key。

适用于以下场景：

1. 你的 AI 编码代理使用订阅制，希望复用已有额度进行代码审查 — 无需额外配置 API Key 或模型端点。
2. 你只需要 OCR 的工程脚手架 — 文件过滤、规则解析、排除逻辑 — 由宿主 Agent 负责所有 LLM 推理。
3. 你正在构建自定义 Agent 流水线，需要结构化输入（文件列表 + 规则）作为自身审查步骤的输入。

## 前置条件

需要安装 `ocr` CLI：

```bash
which ocr || npm install -g @alibaba-group/open-code-review
```

无需配置 LLM（`ocr config set …` 或环境变量）— 委托模式在 OCR 端不调用任何 LLM。

## 安装 Skill / Command

### Claude Code — Command

```bash
mkdir -p .claude/commands
curl -o .claude/commands/delegate-review.md \
  https://raw.githubusercontent.com/alibaba/open-code-review/main/plugins/open-code-review/claude-code/commands/delegate-review.md
```

### 任意 Agent — Skill

```bash
npx skills add alibaba/open-code-review --skill open-code-review-delegate
```

或手动复制：

```bash
cp -R /path/to/open-code-review/skills/open-code-review-delegate ~/.claude/skills/
```

## 工作流程

### 第 1 步：Preview — 确定审查范围

```bash
ocr delegate preview [--from <ref> [--to <ref>]] [--commit <hash>] [--scan] [--exclude <patterns>]
```

输出内容：

- **mode** — workspace / range / commit / scan
- **ref 元数据** — from、to、commit、merge\_base
- **可审查文件列表** — 路径、状态、插入/删除行数
- **已排除文件** — 及排除原因

常见用法：

| 场景 | 命令 |
|------|------|
| 工作区变更 | `ocr delegate preview` |
| 分支对比 | `ocr delegate preview --from main --to feature` |
| 当前分支相对 main | `ocr delegate preview --from main` |
| 单次提交 | `ocr delegate preview -c abc123` |
| 整个仓库 | `ocr delegate preview --scan` |

Scan 模式用标准扫描选择规则枚举整个工作树——`.gitignore`、include/exclude 规则、扩展名白名单和敏感路径都会生效——因此不需要 diff 基线。它不能与 `--from`、`--to` 或 `--commit` 组合使用。

### 第 2 步：获取文件规则

```bash
ocr delegate rule [--from <ref> [--to <ref>]] [--commit <hash>] <path1> <path2> ...
```

传入第 1 步中的可审查文件路径。输出按规则内容分组 — 共享相同规则的文件归为一组，避免重复。

### 第 3 步：获取 diff

根据第 1 步的模式和引用信息选择 diff 命令。审查中的所有 Git 调用均使用 `git --no-pager`。

以下 diff 命令关闭外部 diff 程序、文本转换和颜色输出，以获取纯文本补丁。

**Range 模式**（有 merge\_base）：
```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color <merge_base>..<to> -- "<path>"
```

**Commit 模式**：
```bash
git --no-pager show --no-ext-diff --no-textconv --no-color <commit> -- "<path>"
```

**Workspace 模式**：
```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color HEAD -- "<path>"        # 已跟踪文件
cat "<path>"                     # 新的未跟踪文件
```

**Scan 模式**（没有 diff 基线）：
```bash
cat "<path>"
```

直接读取每个可审查文件的完整内容（大文件分块读取）；需要时用下面的 git 命令补充上下文。

查询上下文：

```bash
git --no-pager log --no-color --oneline -- "<path>"
git --no-pager blame --no-textconv -- "<path>"
git --no-pager show --no-ext-diff --no-textconv --no-color "<ref>:<path>"
```

较大的 diff 可在对应模式的命令中添加 `--output="<absolute-diff-file>"`。选择仓库外的唯一绝对路径，并先创建父目录。后续文件读取工具使用同一路径：

```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color --output="<absolute-diff-file>" <merge_base>..<to> -- "<path>"
```

Git 以退出码 0 结束后，分块读取并审查整个文件，再删除临时文件。命令失败或超时时，重试或标为 `skipped` 并记录错误。输出意外为空时，重新运行 preview 并核对。

### 第 4 步：审查每个文件

对每个可审查文件：

1. 获取其 diff（第 3 步）
2. 参照匹配的规则组（第 2 步）作为审查清单
3. 进行深入审查，按需探索上下文

将每个 preview 中的 `(path, status)` 标为 `reviewed` 或 `skipped`，为跳过的条目说明原因。报告中包含 `total_files`、`reviewed_files`、`skipped_files` 和 `coverage_rate`。

### 第 5 步：报告

按严重程度分类：

- **Critical/High** — Bug、安全问题、数据丢失风险。始终报告。
- **Medium** — 性能问题、错误处理缺失。附带上下文报告。
- **Low** — 风格建议、细微改进。静默丢弃，除非确有价值。

### 第 6 步：记录会话（可选）

把完成的审查持久化为标准 OCR 会话，与原生运行一样可用
`ocr session` 和 `ocr viewer` 查看：

```bash
ocr delegate record --input <result.json> [--scan | --from <ref> [--to <ref>] | --commit <hash>] [--model <宿主模型名>]
```

将最终结果写入仓库外的 JSON 文件：

```json
{
  "files": [
    {"path": "internal/app.go", "outcome": "reviewed"},
    {"path": "large.lock", "outcome": "skipped", "reason": "generated file"}
  ],
  "comments": [
    {"path": "internal/app.go", "content": "missing error handling",
     "existing_code": "f, err := os.Open(path)",
     "suggestion_code": "f, err := os.Open(path)\nif err != nil {\n    return err\n}",
     "start_line": 12, "end_line": 12, "category": "bug", "severity": "high"}
  ],
  "usage": {"prompt_tokens": 12000, "completion_tokens": 900,
            "cache_read_tokens": 4000, "cache_write_tokens": 0}
}
```

- `files` 覆盖第 1 步的每个 `reviewable_files` 条目。`outcome` 取 `reviewed` 或 `skipped`（必须给 `reason`）。漏报的文件记为 failed，运行终态为 `partial`。
- `comments` 使用第 5 步的意见字段，且必须引用报告为 `reviewed` 的文件。
- `existing_code` 与 `suggestion_code` 可选；带修复建议的意见应同时给出二者（被替换片段与替换后的代码），`ocr viewer` 与 IDE 插件会将其渲染为可应用的 diff。行号来自 `start_line`/`end_line`，委托模式不通过 `existing_code` 锚定。
- `usage` 可选：宿主 agent 本次审查自身的 token 用量。OCR 以独立的 `delegate_usage` 记录保存，`ocr viewer` 会将其计入会话总量并标注为宿主上报。
- 传入与第 1 步相同的模式标志（整仓运行用 `--scan`，否则沿用相同的范围/提交标志）。OCR 会重跑 preview 确定 selected 集，因此工作区模式下请先记录再修复。
- `--model` 记录宿主 agent 的模型名，用于溯源，并用于标注宿主上报的用量。

命令打印 session id；`--format json` 连同 run manifest 一起返回。不写入任何 LLM 记录（OCR 未观测宿主 agent 的 LLM 调用），且 `ocr review --resume` 会拒绝委托会话。

## 子命令参考

| 命令 | 用途 |
|------|------|
| `ocr delegate preview [--scan]` | 列出可审查文件 + mode/ref 元数据 |
| `ocr delegate rule <path...>` | 按内容分组解析审查规则 |
| `ocr delegate record --input <file>` | 将宿主 agent 的结果持久化为可查看的会话 |

## 通用标志

所有子命令都支持将 `--from main` 作为 `--from main --to HEAD` 的简写。范围审查比较 `merge-base(from, to)..to`，不包含暂存、未暂存和未跟踪改动；省略范围及提交标志则使用工作区模式。向 `preview`、`rule` 和 `record` 传入相同的范围或提交标志，以使用相同目标。范围标志与提交标志互斥。`--scan` 让 `preview` 和 `record` 切换为整仓运行，不能与范围/提交标志组合使用。`record` 另需 `--input <path>`，并接受 `--model <name>`（宿主模型溯源）与 `--format <text|json>`。

| 标志 | 描述 |
|------|------|
| `--from <ref>` | Range 模式的源引用 |
| `--to <ref>` | Range 模式的目标引用；指定 `--from` 时默认取 `HEAD` |
| `-c, --commit <hash>` | 单次提交模式 |
| `--scan` | 整仓运行而非 diff（仅 preview/record） |
| `--repo <path>` | 仓库根目录（默认：cwd） |
| `--rule <path>` | 自定义 rule.json 路径 |
| `--exclude <patterns>` | 逗号分隔的排除模式 |
| `-b, --background <text>` | 业务上下文 |
| `-B, --background-file <path>` | 从 Markdown 文件读取业务上下文（优先于 `-b`） |

## 与其他集成方式的对比

| 模式 | 谁调用 LLM？ | 适用场景 |
|------|-------------|----------|
| [Agent Skill](../agent-skill/) | OCR | Agent 调用 `ocr review`，OCR 驱动完整审查 |
| [Command（Claude Code）](../claude-code/) | OCR | Claude Code 中的斜杠命令，OCR 驱动审查 |
| **委托模式** | 宿主 Agent | OCR 提供脚手架，Agent 驱动审查 |

## 另请参阅

- [Agent Skill](../agent-skill/) — OCR 代表 Agent 驱动完整审查。
- [Command（Claude Code）](../claude-code/) — 斜杠命令风格，含自动修复。
