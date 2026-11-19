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

func TestExecuteDelegatePreview_Scan(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureDelegateStdout(t, func() {
		if err := executeDelegatePreview(delegateOptions{repoDir: dir, scan: true, format: "json"}); err != nil {
			t.Fatalf("executeDelegatePreview: %v", err)
		}
	})
	var got delegatePreviewJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode preview JSON: %v\n%s", err, out)
	}
	if got.Mode != session.InputModeScan {
		t.Errorf("mode = %q, want scan", got.Mode)
	}
	if got.From != "" || got.To != "" || got.Commit != "" || got.MergeBase != "" {
		t.Errorf("scan preview must not carry diff refs: %+v", got)
	}
	reviewable := make(map[string]bool)
	for _, f := range got.ReviewableFiles {
		reviewable[f.Path] = true
	}
	if !reviewable["app.go"] {
		t.Errorf("app.go missing from reviewable files: %+v", got.ReviewableFiles)
	}
	if reviewable["README.md"] {
		t.Errorf("README.md must not be reviewable: %+v", got.ReviewableFiles)
	}
	excluded := make(map[string]bool)
	for _, f := range got.ExcludedFiles {
		excluded[f.Path] = true
	}
	if !excluded["README.md"] {
		t.Errorf("README.md missing from excluded files: %+v", got.ExcludedFiles)
	}
}

func TestExecuteDelegateRecord_Scan(t *testing.T) {
	dir := initTestGitRepo(t)
	freshOCRHome(t)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := writeRecordResult(t, `{"files":[{"path":"app.go","outcome":"reviewed"}],
		"comments":[{"path":"app.go","content":"missing error handling","start_line":1,"end_line":1,"category":"bug","severity":"low"}],
		"usage":{"prompt_tokens":1200,"completion_tokens":80}}`)

	out := captureDelegateStdout(t, func() {
		if err := executeDelegateRecord(delegateOptions{repoDir: dir, scan: true, format: "json"}, input, "scan-model"); err != nil {
			t.Fatalf("executeDelegateRecord: %v", err)
		}
	})
	got := decodeRecordJSON(t, out)
	if got.RunManifest.Input.Mode != session.InputModeScan {
		t.Errorf("input = %+v", got.RunManifest.Input)
	}
	if got.RunManifest.Input.RequestedHead != "" || got.RunManifest.Input.ResolvedBase != "" {
		t.Errorf("scan input must carry no diff endpoints: %+v", got.RunManifest.Input)
	}
	if got.RunManifest.TerminalState != session.StateComplete {
		t.Errorf("state = %s, coverage = %+v", got.RunManifest.TerminalState, got.RunManifest.Coverage)
	}
	// The command records under git's canonical toplevel, which on macOS
	// resolves the /var -> /private/var symlink t.TempDir() hands back.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	summary, err := session.LoadSummary(resolved, got.SessionID)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if summary.ReviewMode != session.ReviewModeFullScan {
		t.Errorf("review mode = %q, want full_scan", summary.ReviewMode)
	}
}

func TestValidateDelegateOptions_Scan(t *testing.T) {
	valid := delegateOptions{scan: true, format: "text"}
	if err := validateDelegateOptions(&valid); err != nil {
		t.Fatalf("scan alone: %v", err)
	}
	cases := []delegateOptions{
		{scan: true, from: "main", format: "text"},
		{scan: true, to: "main", format: "text"},
		{scan: true, commit: "HEAD", format: "text"},
	}
	for _, opts := range cases {
		if err := validateDelegateOptions(&opts); err == nil || !strings.Contains(err.Error(), "--scan cannot be combined") {
			t.Errorf("opts %+v: err = %v", opts, err)
		}
	}
}

func TestDelegateScanFlagRegistration(t *testing.T) {
	if delegatePreviewCmd.Flags().Lookup("scan") == nil {
		t.Error("preview must expose --scan")
	}
	if delegateRecordCmd.Flags().Lookup("scan") == nil {
		t.Error("record must expose --scan")
	}
	if delegateRuleCmd.Flags().Lookup("scan") != nil {
		t.Error("rule takes explicit paths and must not expose --scan")
	}
}
