---
name: open-code-review-delegate
description: >
  Review workspace changes, PRs, commits, or branch comparisons with the
  host agent's model. OCR's CLI handles file selection and rule resolution;
  this skill defines the review workflow, coverage requirements, and output
  format.
  Use when the user has no separate API key for OCR or wants to reuse their
  coding-agent subscription or model setup. For OCR's full native pipeline
  with its own model provider, choose open-code-review instead.
license: Apache-2.0
compatibility: >
  Requires the `ocr` CLI installed (via `npm install -g
  @alibaba-group/open-code-review` or GitHub release binary). Does NOT
  require a configured LLM endpoint — delegation mode is LLM-free on the
  OCR side.
metadata:
  author: alibaba
  homepage: https://github.com/alibaba/open-code-review
  version: "1.0.0"
---

# Open Code Review — Delegation Mode

## Workflow

### Step 1: Preview — Determine What to Review

Skip OCR provider setup and `ocr llm test`; the host agent's authentication, usage limits, and costs still apply.

```bash
ocr delegate preview --format json [--from <ref> [--to <ref>]] [--commit <hash>] [--scan] [--exclude <patterns>]
```

With `--from` and no `--to`, both delegate subcommands default to `HEAD`. Range review compares the merge-base to the target commit and excludes staged, unstaged, and untracked changes. Omit range/commit flags for workspace mode; range and commit flags are mutually exclusive. `--scan` reviews the whole working tree instead of a diff and cannot be combined with `--from`, `--to`, or `--commit`.

This outputs:
- **mode** (workspace / range / commit / scan)
- **from / to / commit / merge_base** — ref metadata for constructing git commands
- **Reviewable file list** — paths, status, insertions/deletions
- **Excluded files** — with exclusion reason

**Common invocations:**

| Scenario | Command |
|----------|---------|
| Workspace changes | `ocr delegate preview` |
| Branch comparison | `ocr delegate preview --from main --to feature` |
| Current branch against main | `ocr delegate preview --from main` |
| Single commit | `ocr delegate preview -c abc123` |
| Whole repository | `ocr delegate preview --scan` |

Scan mode enumerates the entire working tree with the standard scan selection — `.gitignore`, include/exclude rules, the extension allowlist and secret paths — so no diff baseline is needed. Use it when the repository has no meaningful baseline commit.

If the CLI rejects `--scan` as an unknown flag, do not repeat the call: fall back to workspace mode with the same `--exclude`, `--rule`, and background flags, review the changed files, and state in the report that whole-repository scanning requires a newer OCR build. Do not silently switch to a range or commit review — those cover a different file set.

### Step 2: Get Rules for Files

```bash
ocr delegate rule --format json [--from <ref> [--to <ref>]] [--commit <hash>] <path1> <path2> ...
```

Pass the reviewable file paths from Step 1. Output is grouped by rule content — files sharing the same rule appear under one group, avoiding repetition.

Pass the same range or commit flags as in Step 1 so rules are resolved against the same target. For example, pair `ocr delegate preview --from main` with `ocr delegate rule --from main --format json <path...>`.

### Step 3: Get Diffs

Choose the diff command using the mode and refs from Step 1. Use `git --no-pager` for every Git call during the review.

The diff commands disable external diff programs, text conversion, and color to produce plain patches.

**Range mode** (merge_base provided in preview output):
```bash
git --no-pager diff --no-ext-diff --no-textconv --no-color <merge_base>..<to> -- "<path>"
```

**Commit mode**:
```bash
git --no-pager show --no-ext-diff --no-textconv --no-color <commit> -- "<path>"
```

**Workspace mode**:
```bash
# Tracked files
git --no-pager diff --no-ext-diff --no-textconv --no-color HEAD -- "<path>"
# New untracked files — read directly (entire file is new code)
cat "<path>"
```

**Scan mode** (no diff baseline):
```bash
# Read the entire file; review it as new code
cat "<path>"
```

Read each reviewable file directly, in chunks for large files; use the context commands below as needed.

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

### Step 4: Review Each File

Create a checklist containing every `reviewable_files` entry. For each reviewable file:

Use `(path, status)` as the checklist identity. Workspace mode can report the same path twice when a staged deletion is followed by an untracked recreation.

1. Get its diff (Step 3)
2. Consult its Rule Group (from Step 2) for the review checklist
3. Conduct a thorough review, using appropriate context tools as needed
4. Mark the file `reviewed`, or `skipped` with a concrete reason

For large changes, review in bounded batches grouped by shared rules and diff size. Do not stop after finding the first high-severity issue.

### Step 5: Format Output

Each comment must follow this structure:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| path | string | yes | Relative file path |
| content | string | yes | Review comment describing the issue |
| existing_code | string | no | Snippet the suggestion replaces (before side of the fix) |
| suggestion_code | string | no | Proposed replacement code (after side of the fix) |
| start_line | integer | no | Start line in the new file |
| end_line | integer | no | End line in the new file |
| category | enum | no | bug, security, performance, maintainability, test, style, documentation, other |
| severity | enum | no | critical, high, medium, low |

A comment that suggests a fix carries both `existing_code` and `suggestion_code`. Delegation does not anchor comments through `existing_code` — `start_line` and `end_line` position them — but viewers and IDE extensions only render an applicable edit when `suggestion_code` is present.

### Step 6: Classify and Report

Identify the review mode as delegation.

Before reporting, verify that every previewed file is accounted for. Include `total_files`, `reviewed_files`, `skipped_files`, and `coverage_rate` in the summary. A skipped file must include its reason.

Group findings by severity:

- **Critical/High**: Bugs, security issues, data loss risks — always report
- **Medium**: Performance concerns, error handling gaps, maintainability issues — report with context
- **Low**: Style nits, minor suggestions — report only if clearly valuable

Discard likely false positives silently.

### Step 6.5: Record the Session (Optional, Best-Effort)

Persist the finished review as a standard OCR session, viewable with `ocr session` and `ocr viewer`:

1. Write the final result to a JSON file at a unique absolute path outside the repository (create its parent directory). `files` must contain every `reviewable_files` entry from Step 1 exactly once; `comments` are the Step 5 findings and must reference files reported as `reviewed`:

```json
{
  "files": [
    {"path": "<path>", "outcome": "reviewed"},
    {"path": "<path>", "outcome": "skipped", "reason": "<why>"}
  ],
  "comments": [
    {"path": "<path>", "content": "<issue>",
     "existing_code": "<replaced snippet>", "suggestion_code": "<replacement>",
     "start_line": 1, "end_line": 2, "category": "bug", "severity": "high"}
  ],
  "usage": {"prompt_tokens": 12000, "completion_tokens": 900,
            "cache_read_tokens": 4000, "cache_write_tokens": 0}
}
```

`existing_code` and `suggestion_code` are optional, but a comment with a fix must carry both: the recorded session then renders an applicable diff in `ocr viewer` and the IDE extensions. Comments without `suggestion_code` show the finding text only.

`usage` is required: your own token counts for the whole review, read from your runtime (for Claude Code, the session usage or `/cost`). Supply zeros only when the runtime exposes no counts, and say so in the report. OCR did not observe those calls, so it stores them as a separate `delegate_usage` record: `ocr viewer` adds them to the session totals and marks the numbers host-reported. A result without `usage` is rejected.

2. Run with the same mode flags as Step 1 — `--scan` for a whole-repository run, otherwise the same range/commit flags — **before** applying any Step 7 fixes (OCR re-runs preview to fix the selected set):

```bash
ocr delegate record --format json --input <result-file> [--scan | --from <ref> [--to <ref>] | --commit <hash>] [--model <your-model-name>]
```

3. Append the returned `session_id` to the report. Recording never blocks the review: on `unknown command "record"` (older CLI) skip this step and note that no session was recorded; on a validation error fix the JSON per the message and retry once, then skip if it still fails.

### Step 7: Fix (Optional)

If the user requested "review and fix":
- Apply High/Critical fixes directly
- Describe Medium fixes that require manual intervention
- Skip Low-priority items unless trivial

## Sub-commands Reference

| Command | Purpose |
|---------|---------|
| `ocr delegate preview [--scan]` | Which files to review + mode/ref metadata |
| `ocr delegate rule <path...>` | Review rules grouped by content |
| `ocr delegate record --input <file>` | Persist the result as a viewable OCR session |

## Shared Flags

| Flag | Description |
|------|-------------|
| `--from <ref>` | Source ref for range mode |
| `--to <ref>` | Target ref for range mode; defaults to `HEAD` when `--from` is supplied in either subcommand |
| `-c, --commit <hash>` | Single commit mode |
| `--scan` | Whole repository instead of a diff (preview/record only); cannot be combined with range/commit flags |
| `--repo <path>` | Repository root (default: cwd) |
| `--rule <path>` | Custom rule.json path |
| `--exclude <patterns>` | Comma-separated exclude patterns |
| `-b, --background <text>` | Business context |
| `-B, --background-file <path>` | Business context from Markdown file (takes precedence over `-b`) |
| `-f, --format <text\|json>` | Output format; use `json` for agent integrations |

## Gotchas

- **Host-driven analysis** — OCR's native review agent, positioning, and reflection modules do not run in delegation mode.
- **Rules are grouped** — Files sharing the same rule are grouped together in the output. You can pass any number of paths per call; for large changes, fetch rules per-batch as you review.
- **Working directory matters** — `ocr delegate` operates on the Git repo at the current directory. Use `--repo /path` to override.
- **Untracked files in workspace mode** — `preview` includes untracked files. For these, read the file directly instead of using `git diff`.
- **Scan mode has no baseline** — every reviewable file is reviewed whole, and `--scan` must be passed to both `preview` and `record` so the recorded selection matches the review.
- **`--scan` needs a recent CLI** — an older build rejects the flag; fall back to workspace mode and say so in the report rather than switching to a range or commit review, which covers a different file set.
- **Fix suggestions are supplied, not derived** — a comment only renders an applicable edit when it carries `suggestion_code` (with `existing_code`); there is no diff for OCR to infer a fix from.
- **Background context** — pass `--background` to `preview` when you have requirement context; it appears in the output for your reference during review.
- **Coverage is mandatory** — every `reviewable_files` entry must end as reviewed or explicitly skipped; do not silently omit files.

### Recovering Oversized Background Context

`--background-file` has two independent limits. The raw file must not exceed
1 MiB, and the sanitized content must not exceed 8000 characters. Either
condition aborts the command. When the command reports either limit:

1. Do not silently truncate the source file.
2. Summarize the original material while preserving its requirements,
   constraints, acceptance criteria, and other review-critical details.
3. Retry the affected command by passing the summary as one shell-safe
   argument (for example, use a quoted/escaped argument produced by the host
   shell, or write it to a new size-bounded file and pass that file). Do not
   place untrusted summary text directly in a double-quoted shell template;
   `$()`, backticks, quotes, and variable references can still be evaluated.
   Omit the original `--background-file` so the CLI does not reload the same
   oversized file and fail again.
4. If a faithful summary is not possible, omit the OCR background entirely and
   read the original material directly during the review.

### Troubleshooting CLI Version Compatibility

If an OCR command fails with `command not found`, install the CLI with `npm install -g @alibaba-group/open-code-review` if your environment permits, then retry the same command. If installation is not permitted, explain the prerequisite and stop; do not substitute `ocr review` or ask for an API key.

The `--format` flag is available in `ocr` v1.9.0 and later. The Skill and the
installed CLI can be updated independently. If a requested `preview` or `rule`
command with `--format json` fails specifically with `unknown flag: --format`,
rerun it without the flag and use text output for the rest of the delegation
run. Preserve the explicit mode, ref, file, and rule information from that
output; do not parse text output as JSON or invent missing schema fields. Do
not retry without the flag for any other error; report it and stop the affected
workflow.

The host-agent Skill may consume the equivalent text output to complete its
review checklist. Programmatic integrations that require `schema_version` or
other JSON fields must require a JSON-capable CLI instead: verify with
`ocr --version` and upgrade when necessary:

```bash
npm install -g @alibaba-group/open-code-review
```
