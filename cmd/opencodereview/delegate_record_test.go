// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/session"
)

// writeRecordResult writes a host-agent result JSON outside the repository and
// returns its path.
func writeRecordResult(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write result: %v", err)
	}
	return path
}

func decodeRecordJSON(t *testing.T, out []byte) delegateRecordJSON {
	t.Helper()
	var got delegateRecordJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode record JSON: %v\n%s", err, out)
	}
	return got
}

func TestExecuteDelegateRecord_Workspace(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := writeRecordResult(t, `{"files":[{"path":"app.go","outcome":"reviewed"}],
		"comments":[{"path":"app.go","content":"missing doc comment","start_line":1,"end_line":1,"category":"documentation","severity":"low"}],
		"usage":{"prompt_tokens":1200,"completion_tokens":80}}`)

	var got delegateRecordJSON
	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(delegateOptions{repoDir: dir, format: "json"}, input, "test-model"); err != nil {
			t.Fatalf("executeDelegateRecord: %v", err)
		}
	})
	got = decodeRecordJSON(t, out)
	if got.SessionID == "" || got.RunManifest == nil {
		t.Fatalf("record JSON = %+v", got)
	}
	if got.RunManifest.Operation != session.OperationDelegate || got.RunManifest.TerminalState != session.StateComplete {
		t.Errorf("manifest = %+v", got.RunManifest)
	}
	if got.RunManifest.Execution.Model != "test-model" {
		t.Errorf("execution = %+v", got.RunManifest.Execution)
	}
	// The command records under git's canonical toplevel, which on macOS
	// resolves the /var -> /private/var symlink t.TempDir() hands back.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	comments, err := session.LoadComments(resolved, got.SessionID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("LoadComments = %+v, %v", comments, err)
	}
	summary, err := session.LoadSummary(resolved, got.SessionID)
	if err != nil || summary.ReviewMode != session.ReviewModeWorkspace || summary.TotalComments != 1 {
		t.Fatalf("LoadSummary = %+v, %v", summary, err)
	}
}

func TestExecuteDelegateRecord_Range(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	gitCommitFile(t, dir, "b.go", "package b\n", "add b")
	input := writeRecordResult(t, `{"files":[{"path":"b.go","outcome":"reviewed"}],"usage":{"prompt_tokens":1}}`)

	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(delegateOptions{repoDir: dir, from: "HEAD~1", to: "HEAD", format: "json"}, input, ""); err != nil {
			t.Fatalf("executeDelegateRecord(range): %v", err)
		}
	})
	got := decodeRecordJSON(t, out)
	if got.RunManifest.Input.Mode != session.InputModeRange || got.RunManifest.Input.ExactRange == "" {
		t.Errorf("input = %+v", got.RunManifest.Input)
	}
	if got.RunManifest.TerminalState != session.StateComplete {
		t.Errorf("state = %s", got.RunManifest.TerminalState)
	}
}

func TestExecuteDelegateRecord_Commit(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	gitCommitFile(t, dir, "c.go", "package c\n", "add c")
	input := writeRecordResult(t, `{"files":[{"path":"c.go","outcome":"reviewed"}],"usage":{"prompt_tokens":1}}`)

	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(delegateOptions{repoDir: dir, commit: "HEAD", format: "json"}, input, ""); err != nil {
			t.Fatalf("executeDelegateRecord(commit): %v", err)
		}
	})
	got := decodeRecordJSON(t, out)
	if got.RunManifest.Input.Mode != session.InputModeCommit || got.RunManifest.Input.RequestedHead != "HEAD" {
		t.Errorf("input = %+v", got.RunManifest.Input)
	}
}

func TestExecuteDelegateRecord_FromOnlyDefaultsToHEAD(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	gitCommitFile(t, dir, "d.go", "package d\n", "add d")
	input := writeRecordResult(t, `{"files":[{"path":"d.go","outcome":"reviewed"}],"usage":{"prompt_tokens":1}}`)

	opts := delegateOptions{repoDir: dir, from: "HEAD~1", format: "json"}
	if err := validateDelegateOptions(&opts); err != nil {
		t.Fatalf("validateDelegateOptions: %v", err)
	}
	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(opts, input, ""); err != nil {
			t.Fatalf("executeDelegateRecord(from-only): %v", err)
		}
	})
	got := decodeRecordJSON(t, out)
	if got.RunManifest.Input.Mode != session.InputModeRange || got.RunManifest.Input.RequestedHead != "HEAD" {
		t.Errorf("input = %+v", got.RunManifest.Input)
	}
}

func TestExecuteDelegateRecord_TextOutput(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := writeRecordResult(t, `{"files":[{"path":"app.go","outcome":"reviewed"}],"usage":{"prompt_tokens":1}}`)

	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(delegateOptions{repoDir: dir}, input, ""); err != nil {
			t.Fatalf("executeDelegateRecord(text): %v", err)
		}
	})
	if !strings.Contains(string(out), "Recorded session") || !strings.Contains(string(out), "ocr viewer") {
		t.Errorf("text output = %s", out)
	}
}

func TestExecuteDelegateRecord_SkippedIsPartialOrFailed(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := writeRecordResult(t, `{"files":[{"path":"app.go","outcome":"skipped","reason":"diff too large"}],"usage":{"prompt_tokens":1}}`)

	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(delegateOptions{repoDir: dir, format: "json"}, input, ""); err != nil {
			t.Fatalf("executeDelegateRecord(skipped): %v", err)
		}
	})
	got := decodeRecordJSON(t, out)
	if got.RunManifest.TerminalState != session.StateFailed || len(got.RunManifest.Coverage.Failed) != 1 {
		t.Errorf("manifest = %+v", got.RunManifest)
	}
}

func TestExecuteDelegateRecord_UnknownPathRejected(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := writeRecordResult(t, `{"files":[{"path":"ghost.go","outcome":"reviewed"}]}`)

	err := executeDelegateRecord(delegateOptions{repoDir: dir}, input, "")
	if err == nil || !strings.Contains(err.Error(), "not in the reviewable set") {
		t.Fatalf("error = %v, want unknown-path rejection", err)
	}
}

func TestExecuteDelegateRecord_MissingInputFile(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	err := executeDelegateRecord(delegateOptions{repoDir: dir}, filepath.Join(t.TempDir(), "absent.json"), "")
	if err == nil {
		t.Fatal("expected error for missing input file")
	}
}
