---
title: Delegation Mode
sidebar:
  order: 5
---

OCR handles deterministic engineering (file selection, rule resolution)
while the host agent performs the actual code review using its own LLM
capabilities. No LLM endpoint is required on the OCR side.

## When to use delegation mode

Delegation mode is designed for subscription-based AI coding agents —
such as Claude Code, Codex, Cursor, Open Code, Qoder, etc. — where you
already have an LLM subscription bundled with the host agent. Instead
of configuring a separate model endpoint for OCR, you reuse the host
agent's existing subscription quota to perform the review.

Use delegation mode when:

1. Your AI coding agent runs on a subscription plan and you want to
   reuse that quota for code review — no extra API key or model
   configuration needed.
2. You want OCR only for its engineering scaffolding — file filtering,
   rule resolution, exclusion logic — while the host agent handles all
   LLM reasoning.
3. You're building a custom agent pipeline that needs structured inputs
   (file list + rules) for its own review step.

## Prerequisites

The `ocr` CLI must be installed:

```bash
which ocr || npm install -g @alibaba-group/open-code-review
```

No LLM configuration (`ocr config set …` or environment variables) is
needed — delegation mode never calls an LLM on the OCR side.

## Install the skill / command

### Claude Code — Command

```bash
mkdir -p .claude/commands
curl -o .claude/commands/delegate-review.md \
  https://raw.githubusercontent.com/alibaba/open-code-review/main/plugins/open-code-review/claude-code/commands/delegate-review.md
```

### Any agent — Skill

```bash
npx skills add alibaba/open-code-review --skill open-code-review-delegate
```

Or copy the manifest manually:

```bash
cp -R /path/to/open-code-review/skills/open-code-review-delegate ~/.claude/skills/
```

## Workflow

### Step 1: Preview — determine what to review

```bash
ocr delegate preview [--from <ref> [--to <ref>]] [--commit <hash>] [--scan] [--exclude <patterns>]
```

Outputs:

- **mode** — workspace / range / commit / scan
- **ref metadata** — from, to, commit, merge\_base
- **Reviewable file list** — paths, status, insertions/deletions
- **Excluded files** — with exclusion reason

Common invocations:

| Scenario | Command |
|----------|---------|
| Workspace changes | `ocr delegate preview` |
| Branch comparison | `ocr delegate preview --from main --to feature` |
| Current branch against main | `ocr delegate preview --from main` |
| Single commit | `ocr delegate preview -c abc123` |
| Whole repository | `ocr delegate preview --scan` |

Scan mode enumerates the entire working tree with the standard scan
selection — `.gitignore`, include/exclude rules, the extension allowlist
and secret paths — so no diff baseline is needed. It cannot be combined
with `--from`, `--to`, or `--commit`.

### Step 2: Get rules for files

```bash
ocr delegate rule [--from <ref> [--to <ref>]] [--commit <hash>] <path1> <path2> ...
```

Pass the reviewable paths from Step 1. Output is grouped by rule
content — files sharing the same rule appear under one group, avoiding
repetition.

### Step 3: Get diffs

Choose the diff command using the mode and refs from Step 1. Use `git --no-pager` for every Git call during the review.

The diff commands disable external diff programs, text conversion, and color to produce plain patches.

**Range mode** (merge\_base provided):
```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color <merge_base>..<to> -- "<path>"
```

**Commit mode**:
```bash
git --no-pager show --no-ext-diff --no-textconv --no-color <commit> -- "<path>"
```

**Workspace mode**:
```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color HEAD -- "<path>"        # tracked files
cat "<path>"                     # new untracked files
```

**Scan mode** (no diff baseline):
```bash
cat "<path>"
```

Read each reviewable file directly, in chunks for large files; use the
context commands below as needed.

To read context:

```bash
git --no-pager log --no-color --oneline -- "<path>"
git --no-pager blame --no-textconv -- "<path>"
git --no-pager show --no-ext-diff --no-textconv --no-color "<ref>:<path>"
```

For large diffs, add `--output="<absolute-diff-file>"` to the command for the selected mode. Choose a unique absolute path outside the repository and create its parent directory. Use the same path in file-reading tool calls:

```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color --output="<absolute-diff-file>" <merge_base>..<to> -- "<path>"
```

After Git exits with code 0, read and review the entire file in chunks, then remove it. If Git fails or times out, retry or record `skipped` with the error. Check unexpectedly empty output against a fresh preview.

### Step 4: Review each file

For each reviewable file:

1. Get its diff (Step 3)
2. Consult the matching Rule Group (Step 2) as the review checklist
3. Conduct a thorough review, using context exploration as needed

Record each previewed `(path, status)` as `reviewed` or `skipped`. Give a reason for every skipped entry, and report `total_files`, `reviewed_files`, `skipped_files`, and `coverage_rate`.

### Step 5: Report

Classify each finding by severity:

- **Critical/High** — bugs, security issues, data loss risks. Always report.
- **Medium** — performance concerns, error handling gaps. Report with context.
- **Low** — style nits, minor suggestions. Discard silently unless clearly valuable.

### Step 6: Record the session (optional)

Persist the finished review as a standard OCR session, viewable with
`ocr session` and `ocr viewer` alongside native runs:

```bash
ocr delegate record --input <result.json> [--scan | --from <ref> [--to <ref>] | --commit <hash>] [--model <host-model>]
```

Write the final result to a JSON file outside the repository:

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
  ]
}
```

- `files` covers every `reviewable_files` entry from Step 1. `outcome`
  is `reviewed` or `skipped` (a `reason` is required). Unreported files
  are recorded as failed and the run ends `partial`.
- `comments` carry the finding fields from Step 5 and must reference a
  file reported as `reviewed`.
- `existing_code` and `suggestion_code` are optional; a comment that
  proposes a fix carries both (the replaced snippet and its replacement),
  and `ocr viewer` and the IDE extensions render it as an applicable
  diff. Line numbers come from `start_line` / `end_line`; delegation
  does not anchor comments through `existing_code`.
- Pass the same mode flags as Step 1 (`--scan` for a whole-repository
  run, otherwise the same range/commit flags). OCR re-runs preview to fix
  the selected set, so in workspace mode record before applying fixes.
- `--model` records the host agent's model name for provenance.

The command prints the session id; `--format json` returns it together
with the run manifest. No LLM records are written (OCR did not observe
the host agent's LLM calls), and `ocr review --resume` refuses delegate
sessions.

## Sub-commands reference

| Command | Purpose |
|---------|---------|
| `ocr delegate preview [--scan]` | List reviewable files + mode/ref metadata |
| `ocr delegate rule <path...>` | Resolve review rules grouped by content |
| `ocr delegate record --input <file>` | Persist the host agent's result as a viewable session |

## Shared flags

All subcommands accept `--from main` as shorthand for `--from main --to HEAD`. Range review compares `merge-base(from, to)..to` and excludes staged, unstaged, and untracked changes; omit range/commit flags for workspace mode. Pass the same range or commit flags to `preview`, `rule`, and `record` so they use the same target. Range and commit flags are mutually exclusive. `--scan` switches `preview` and `record` to a whole-repository run and cannot be combined with range/commit flags. `record` additionally requires `--input <path>` and accepts `--model <name>` (host model provenance) and `--format <text|json>`.

| Flag | Description |
|------|-------------|
| `--from <ref>` | Source ref for range mode |
| `--to <ref>` | Target ref for range mode; defaults to `HEAD` when `--from` is supplied |
| `-c, --commit <hash>` | Single commit mode |
| `--scan` | Whole repository instead of a diff (preview/record only) |
| `--repo <path>` | Repository root (default: cwd) |
| `--rule <path>` | Custom rule.json path |
| `--exclude <patterns>` | Comma-separated exclude patterns |
| `-b, --background <text>` | Business context |
| `-B, --background-file <path>` | Business context from Markdown file (takes precedence over `-b`) |

## Comparison with other integration modes

| Mode | Who calls the LLM? | Use case |
|------|-------------------|----------|
| [Agent Skill](../agent-skill/) | OCR | Agent invokes `ocr review`; OCR drives the full review |
| [Command (Claude Code)](../claude-code/) | OCR | Slash command in Claude Code; OCR drives the review |
| **Delegation Mode** | Host agent | OCR provides scaffolding; agent drives the review |

## See Also

- [Agent Skill](../agent-skill/) — OCR drives the full review on behalf of the agent.
- [Command (Claude Code)](../claude-code/) — slash-command flavor with auto-fix.
