// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/diff"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
)

func reviewable(path string) model.PreviewEntry {
	return model.PreviewEntry{Path: path, Status: "MODIFIED", WillReview: true}
}

func excluded(path string, reason model.ExcludeReason) model.PreviewEntry {
	return model.PreviewEntry{Path: path, Status: "MODIFIED", WillReview: false, ExcludeReason: reason}
}

func validInput() *RecordInput {
	return &RecordInput{
		Files: []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
		Comments: []model.LlmComment{{
			Path: "a.go", Content: "nil dereference", StartLine: 3, EndLine: 5,
			Category: "bug", Severity: "high",
		}},
	}
}

func TestValidateRecordInput(t *testing.T) {
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go"), excluded("b.lock", model.ExcludeDefaultPath)}}
	cases := []struct {
		name    string
		preview *model.Preview
		input   *RecordInput
		wantErr string
	}{
		{"valid", preview, validInput(), ""},
		{"nil preview", nil, validInput(), "preview is required"},
		{"nil input", preview, nil, "input is required"},
		{"bad schema", preview, &RecordInput{SchemaVersion: "2", Files: []RecordFile{}}, "schema_version"},
		{"nil files", preview, &RecordInput{}, "files array is required"},
		{"empty path", preview, &RecordInput{Files: []RecordFile{{Outcome: OutcomeReviewed}}}, "empty path"},
		{"unknown file", preview, &RecordInput{Files: []RecordFile{{Path: "ghost.go", Outcome: OutcomeReviewed}}}, "not in the reviewable set"},
		{"excluded file reported", preview, &RecordInput{Files: []RecordFile{{Path: "b.lock", Outcome: OutcomeReviewed}}}, "not in the reviewable set"},
		{"duplicate file", preview, &RecordInput{Files: []RecordFile{
			{Path: "a.go", Outcome: OutcomeReviewed}, {Path: "a.go", Outcome: OutcomeReviewed},
		}}, "duplicate file entry"},
		{"bad outcome", preview, &RecordInput{Files: []RecordFile{{Path: "a.go", Outcome: "done"}}}, "invalid outcome"},
		{"skipped without reason", preview, &RecordInput{Files: []RecordFile{{Path: "a.go", Outcome: OutcomeSkipped}}}, "requires a reason"},
		{"skipped with reason", preview, &RecordInput{Files: []RecordFile{{Path: "a.go", Outcome: OutcomeSkipped, Reason: "too large"}}}, ""},
		{"comment unknown path", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
			Comments: []model.LlmComment{{Path: "ghost.go", Content: "x"}},
		}, "outside the reviewable set"},
		{"comment on skipped", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeSkipped, Reason: "r"}},
			Comments: []model.LlmComment{{Path: "a.go", Content: "x"}},
		}, "not reported as reviewed"},
		{"comment on unreported", preview, &RecordInput{
			Files:    []RecordFile{},
			Comments: []model.LlmComment{{Path: "a.go", Content: "x"}},
		}, "not reported as reviewed"},
		{"empty content", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
			Comments: []model.LlmComment{{Path: "a.go", Content: "  "}},
		}, "empty content"},
		{"bad severity", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
			Comments: []model.LlmComment{{Path: "a.go", Content: "x", Severity: "urgent"}},
		}, "invalid severity"},
		{"bad category", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
			Comments: []model.LlmComment{{Path: "a.go", Content: "x", Category: "typo"}},
		}, "invalid category"},
		{"negative line", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
			Comments: []model.LlmComment{{Path: "a.go", Content: "x", StartLine: -1}},
		}, "invalid line range"},
		{"reversed range", preview, &RecordInput{
			Files:    []RecordFile{{Path: "a.go", Outcome: OutcomeReviewed}},
			Comments: []model.LlmComment{{Path: "a.go", Content: "x", StartLine: 9, EndLine: 2}},
		}, "invalid line range"},
		{"duplicate reviewable in preview", &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go"), reviewable("a.go")}}, validInput(), "more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRecordInput(tc.preview, tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRecordInput(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"files":[{"path":"a.go","outcome":"reviewed"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := LoadRecordInput(good)
	if err != nil || len(input.Files) != 1 {
		t.Fatalf("LoadRecordInput(good) = %+v, %v", input, err)
	}

	if _, err := LoadRecordInput(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecordInput(bad); err == nil {
		t.Fatal("expected error for malformed JSON")
	}

	huge := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(huge, make([]byte, maxRecordInputBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecordInput(huge); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}
}

// recordTestHome redirects session persistence to a temp home, mirroring the
// cmd-layer helpers (USERPROFILE matters on Windows).
func recordTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func workspaceArgs(repoDir string, preview *model.Preview, input *RecordInput) RecordArgs {
	return RecordArgs{
		RepoDir:        repoDir,
		GitBranch:      "main",
		Mode:           session.InputModeWorkspace,
		Resolution:     diff.InputResolution{ResolvedBase: "base-sha"},
		RemoteIdentity: "https://example.com/repo.git",
		Preview:        preview,
		Input:          input,
		Model:          "host-model",
	}
}

func TestRecord_WorkspaceComplete(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go"), excluded("b.lock", model.ExcludeDefaultPath)}}

	id, m, err := Record(workspaceArgs(repoDir, preview, validInput()))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if id == "" || m == nil {
		t.Fatalf("Record returned id=%q manifest=%v", id, m)
	}
	if m.TerminalState != session.StateComplete {
		t.Errorf("terminal state = %s, want complete", m.TerminalState)
	}
	if m.Operation != session.OperationDelegate {
		t.Errorf("operation = %s, want delegate", m.Operation)
	}
	if len(m.Coverage.Selected) != 1 || len(m.Coverage.Completed) != 1 || len(m.Coverage.Excluded) != 1 {
		t.Errorf("coverage = %+v", m.Coverage)
	}
	if m.Execution.Provider != "delegate" || m.Execution.Model != "host-model" {
		t.Errorf("execution = %+v", m.Execution)
	}
	if m.Input.Mode != session.InputModeWorkspace || m.Input.ResolvedBase != "base-sha" {
		t.Errorf("input = %+v", m.Input)
	}
	if m.Repository.IdentitySHA256 == "" {
		t.Error("repository identity not recorded")
	}

	comments, err := session.LoadComments(repoDir, id)
	if err != nil || len(comments) != 1 || comments[0].Path != "a.go" {
		t.Fatalf("LoadComments = %+v, %v", comments, err)
	}
	summary, err := session.LoadSummary(repoDir, id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if summary.TotalComments != 1 || summary.CompletedFiles != 1 || summary.RunManifest == nil {
		t.Errorf("summary = %+v", summary)
	}

	path, err := session.SessionFilePath(repoDir, id)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var first, last map[string]any
	var types []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("bad JSONL record: %v", err)
		}
		typ, _ := rec["type"].(string)
		types = append(types, typ)
		if first == nil {
			first = rec
		}
		last = rec
	}
	if first["type"] != "session_start" || first["llmSource"] != "delegate" || first["model"] != "host-model" {
		t.Errorf("session_start = %+v", first)
	}
	if last["type"] != "session_end" || last["run_manifest"] == nil {
		t.Errorf("session_end = %+v", last)
	}
	for _, typ := range types {
		if typ == "llm_request" || typ == "llm_response" {
			t.Errorf("delegate session must not fabricate LLM records, found %s", typ)
		}
	}
}

func TestRecord_PartialWhenFileUnreported(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go"), reviewable("b.go")}}

	id, m, err := Record(workspaceArgs(repoDir, preview, validInput()))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m.TerminalState != session.StatePartial {
		t.Errorf("terminal state = %s, want partial", m.TerminalState)
	}
	if len(m.Coverage.Failed) != 1 || m.Coverage.Failed[0].Path != "b.go" {
		t.Fatalf("failed = %+v", m.Coverage.Failed)
	}
	if !strings.Contains(m.Coverage.Failed[0].Reason, "not reported") {
		t.Errorf("reason = %q", m.Coverage.Failed[0].Reason)
	}
	if _, err := session.LoadSummary(repoDir, id); err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
}

func TestRecord_SkippedFile(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go")}}
	input := &RecordInput{Files: []RecordFile{{Path: "a.go", Outcome: OutcomeSkipped, Reason: "diff too large"}}}

	_, m, err := Record(workspaceArgs(repoDir, preview, input))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m.TerminalState != session.StateFailed {
		t.Errorf("terminal state = %s, want failed (nothing covered)", m.TerminalState)
	}
	if len(m.Coverage.Failed) != 1 || !strings.Contains(m.Coverage.Failed[0].Reason, "diff too large") {
		t.Errorf("failed = %+v", m.Coverage.Failed)
	}
}

func TestRecord_SkippedReasonRedactedInCheckpoint(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go")}}
	input := &RecordInput{Files: []RecordFile{{
		Path: "a.go", Outcome: OutcomeSkipped,
		Reason: "fetch failed: Bearer sk-secret123 https://user:pass@host/repo",
	}}}

	id, m, err := Record(workspaceArgs(repoDir, preview, input))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	_, items, err := session.LoadDetail(repoDir, id)
	if err != nil || len(items) != 1 {
		t.Fatalf("LoadDetail = %+v, %v", items, err)
	}
	for _, s := range []string{items[0].Error, m.Coverage.Failed[0].Reason} {
		if strings.Contains(s, "sk-secret123") || strings.Contains(s, "user:pass") {
			t.Errorf("secret leaked into persisted reason: %q", s)
		}
		if !strings.Contains(s, "[REDACTED]") {
			t.Errorf("expected redaction marker in %q", s)
		}
	}
}

func TestRecord_DeletedRecreatedPath(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{
		{Path: "x.go", Status: "DELETED", WillReview: false, ExcludeReason: model.ExcludeDeleted},
		{Path: "x.go", Status: "ADDED", WillReview: true},
	}}
	input := &RecordInput{Files: []RecordFile{{Path: "x.go", Outcome: OutcomeReviewed}}}

	_, m, err := Record(workspaceArgs(repoDir, preview, input))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m.TerminalState != session.StateComplete || len(m.Coverage.Selected) != 1 {
		t.Errorf("state=%s coverage=%+v", m.TerminalState, m.Coverage)
	}
	if len(m.Coverage.Excluded) != 0 {
		t.Errorf("recreated path must not stay excluded: %+v", m.Coverage.Excluded)
	}
}

func TestRecord_EmptyReviewableSet(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{excluded("b.lock", model.ExcludeDefaultPath)}}
	input := &RecordInput{Files: []RecordFile{}}

	id, m, err := Record(workspaceArgs(repoDir, preview, input))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m.TerminalState != session.StateSkipped {
		t.Errorf("terminal state = %s, want skipped", m.TerminalState)
	}
	if _, err := session.LoadSummary(repoDir, id); err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
}

func TestRecord_RangeAndCommitIdentity(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go")}}

	rangeArgs := workspaceArgs(repoDir, preview, validInput())
	rangeArgs.Mode = session.InputModeRange
	rangeArgs.From, rangeArgs.To = "main", "feature"
	rangeArgs.Resolution = diff.InputResolution{ResolvedBase: "b", ResolvedHead: "h", ExactRange: "b..h"}
	_, m, err := Record(rangeArgs)
	if err != nil {
		t.Fatalf("Record(range): %v", err)
	}
	if m.Input.RequestedFrom != "main" || m.Input.RequestedHead != "feature" || m.Input.ExactRange != "b..h" {
		t.Errorf("range input = %+v", m.Input)
	}

	commitArgs := workspaceArgs(repoDir, preview, validInput())
	commitArgs.Mode = session.InputModeCommit
	commitArgs.Commit = "abc123"
	commitArgs.Resolution = diff.InputResolution{ResolvedBase: "p", ResolvedHead: "abc123"}
	_, m, err = Record(commitArgs)
	if err != nil {
		t.Fatalf("Record(commit): %v", err)
	}
	if m.Input.RequestedHead != "abc123" || m.Input.RequestedFrom != "" {
		t.Errorf("commit input = %+v", m.Input)
	}
}

func TestRecord_ScanMode(t *testing.T) {
	recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{
		{Path: "a.go", Status: "scan", WillReview: true},
		{Path: "README.md", Status: "scan", WillReview: false, ExcludeReason: model.ExcludeExtension},
	}}
	args := workspaceArgs(repoDir, preview, validInput())
	args.Mode = session.InputModeScan
	args.Resolution = diff.InputResolution{}

	id, m, err := Record(args)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m.Input.Mode != session.InputModeScan {
		t.Errorf("input mode = %q, want scan", m.Input.Mode)
	}
	if m.Input.RequestedHead != "" || m.Input.RequestedFrom != "" || m.Input.ResolvedBase != "" || m.Input.ExactRange != "" {
		t.Errorf("scan input must carry no diff endpoints: %+v", m.Input)
	}
	if m.TerminalState != session.StateComplete || len(m.Coverage.Selected) != 1 || len(m.Coverage.Excluded) != 1 {
		t.Errorf("state=%s coverage=%+v", m.TerminalState, m.Coverage)
	}
	summary, err := session.LoadSummary(repoDir, id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if summary.ReviewMode != session.ReviewModeFullScan {
		t.Errorf("session review mode = %q, want full_scan", summary.ReviewMode)
	}
}

func TestRecord_ValidationFailureCreatesNoSession(t *testing.T) {
	home := recordTestHome(t)
	repoDir := t.TempDir()
	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go")}}
	input := &RecordInput{Files: []RecordFile{{Path: "ghost.go", Outcome: OutcomeReviewed}}}

	if _, _, err := Record(workspaceArgs(repoDir, preview, input)); err == nil {
		t.Fatal("expected validation error")
	}
	store := filepath.Join(home, ".opencodereview", "sessions")
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Errorf("validation failure must not open the session store (stat err = %v)", err)
	}
}

func TestRecord_PersistenceFailure(t *testing.T) {
	// A regular file as HOME makes the session directory creation fail, which
	// session.New caches and Finalize surfaces as the delivery error.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blocker)
	t.Setenv("USERPROFILE", blocker)

	preview := &model.Preview{Entries: []model.PreviewEntry{reviewable("a.go")}}
	if _, _, err := Record(workspaceArgs(t.TempDir(), preview, validInput())); err == nil {
		t.Fatal("expected persistence error")
	}
}
