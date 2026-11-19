// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package delegate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alibaba/open-code-review/internal/diff"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
)

// RecordInput is the host agent's final delegation result: one outcome per
// reviewable file plus the findings. The selected set, identities, timestamps
// and coverage arithmetic are derived by OCR from a fresh preview, never
// trusted from this file.
type RecordInput struct {
	SchemaVersion string             `json:"schema_version,omitempty"`
	Files         []RecordFile       `json:"files"`
	Comments      []model.LlmComment `json:"comments"`
	Usage         *RecordUsage       `json:"usage,omitempty"`
}

// RecordUsage is the token usage the host agent reports for its own run. OCR
// never observes these calls, so the counts are persisted on a record type of
// their own and the viewer labels them as host-reported.
type RecordUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// RecordFile is the host agent's outcome for one reviewable file.
type RecordFile struct {
	Path    string `json:"path"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

const (
	// OutcomeReviewed marks a file the host agent reviewed.
	OutcomeReviewed = "reviewed"
	// OutcomeSkipped marks a file the host agent deliberately did not review;
	// Reason must say why.
	OutcomeSkipped = "skipped"

	recordSchemaVersion = "1"

	// maxRecordInputBytes caps the result file. Findings for a large review can
	// legitimately exceed the background-context limit, so this is generous.
	maxRecordInputBytes = 10 << 20
)

// LoadRecordInput reads and decodes the host agent's result file.
func LoadRecordInput(path string) (*RecordInput, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read record input: %w", err)
	}
	if info.Size() > maxRecordInputBytes {
		return nil, fmt.Errorf("record input %s exceeds %d bytes", path, maxRecordInputBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read record input: %w", err)
	}
	var input RecordInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("parse record input %s: %w", path, err)
	}
	return &input, nil
}

// ValidateRecordInput checks the host-supplied result against the authoritative
// preview. Unknown paths, duplicate entries, invalid enums and comments on
// skipped files are hard errors: they mean the result does not describe this
// diff. Missing coverage is NOT an error here — Record marks unreported files
// failed so the manifest reports partial instead of losing the whole run.
func ValidateRecordInput(preview *model.Preview, input *RecordInput) error {
	if preview == nil {
		return fmt.Errorf("record validation: preview is required")
	}
	if input == nil {
		return fmt.Errorf("record validation: input is required")
	}
	if input.SchemaVersion != "" && input.SchemaVersion != recordSchemaVersion {
		return fmt.Errorf("record validation: unsupported schema_version %q (want %q)", input.SchemaVersion, recordSchemaVersion)
	}
	if input.Files == nil {
		return fmt.Errorf("record validation: files array is required")
	}
	if u := input.Usage; u != nil {
		// The viewer decodes persisted counts as JSON numbers, which are exact
		// only up to 2^53-1; anything larger would display corrupted totals.
		const maxExactJSONInt = 1<<53 - 1
		for _, v := range []int{u.PromptTokens, u.CompletionTokens, u.CacheReadTokens, u.CacheWriteTokens} {
			if v < 0 || v > maxExactJSONInt {
				return fmt.Errorf("record validation: usage counts must be between 0 and %d", maxExactJSONInt)
			}
		}
	}

	reviewable := make(map[string]bool)
	for _, e := range preview.Entries {
		if !e.WillReview {
			continue
		}
		if reviewable[e.Path] {
			return fmt.Errorf("record validation: preview lists reviewable path %q more than once", e.Path)
		}
		reviewable[e.Path] = true
	}

	outcome := make(map[string]string, len(input.Files))
	var unknown []string
	for _, f := range input.Files {
		if f.Path == "" {
			return fmt.Errorf("record validation: file entry with empty path")
		}
		if !reviewable[f.Path] {
			unknown = append(unknown, f.Path)
			continue
		}
		if _, dup := outcome[f.Path]; dup {
			return fmt.Errorf("record validation: duplicate file entry %q", f.Path)
		}
		switch f.Outcome {
		case OutcomeReviewed:
		case OutcomeSkipped:
			if strings.TrimSpace(f.Reason) == "" {
				return fmt.Errorf("record validation: skipped file %q requires a reason", f.Path)
			}
		default:
			return fmt.Errorf("record validation: file %q has invalid outcome %q (want %q or %q)", f.Path, f.Outcome, OutcomeReviewed, OutcomeSkipped)
		}
		outcome[f.Path] = f.Outcome
	}
	if len(unknown) > 0 {
		return fmt.Errorf("record validation: files not in the reviewable set: %s", strings.Join(unknown, ", "))
	}

	for i, c := range input.Comments {
		if !reviewable[c.Path] {
			return fmt.Errorf("record validation: comment %d references path %q outside the reviewable set", i+1, c.Path)
		}
		if outcome[c.Path] != OutcomeReviewed {
			return fmt.Errorf("record validation: comment %d references %q, which was not reported as reviewed", i+1, c.Path)
		}
		if strings.TrimSpace(c.Content) == "" {
			return fmt.Errorf("record validation: comment %d on %q has empty content", i+1, c.Path)
		}
		if !validSeverity(c.Severity) {
			return fmt.Errorf("record validation: comment %d on %q has invalid severity %q", i+1, c.Path, c.Severity)
		}
		if !validCategory(c.Category) {
			return fmt.Errorf("record validation: comment %d on %q has invalid category %q", i+1, c.Path, c.Category)
		}
		if c.StartLine < 0 || c.EndLine < 0 || (c.StartLine > 0 && c.EndLine > 0 && c.StartLine > c.EndLine) {
			return fmt.Errorf("record validation: comment %d on %q has invalid line range %d..%d", i+1, c.Path, c.StartLine, c.EndLine)
		}
	}
	// Every delegated run reports usage so cost stays comparable across runs.
	// A host whose runtime exposes no counts supplies zeros and says so in the
	// report; the field itself is never omitted.
	if input.Usage == nil {
		return fmt.Errorf("record validation: usage is required for delegated runs (report the host agent's token counts; use zeros when the runtime exposes none)")
	}
	return nil
}

func validSeverity(s string) bool {
	switch s {
	case "", "critical", "high", "medium", "low":
		return true
	}
	return false
}

func validCategory(c string) bool {
	switch c {
	case "", "bug", "security", "performance", "maintainability", "test", "style", "documentation", "other":
		return true
	}
	return false
}

// sessionReviewMode maps the manifest input mode to the session-level review
// mode. Scan keeps the native "full_scan" label so session listings classify a
// delegate whole-repository run the same way as a native scan.
func sessionReviewMode(mode string) string {
	if mode == session.InputModeScan {
		return session.ReviewModeFullScan
	}
	return mode
}

// RecordArgs carries everything Record needs, already resolved by the caller
// so the recording path itself runs no git commands and stays testable.
type RecordArgs struct {
	RepoDir        string
	GitBranch      string
	Mode           string // session.InputModeWorkspace / InputModeRange / InputModeCommit / InputModeScan
	From           string
	To             string
	Commit         string
	Resolution     diff.InputResolution
	RemoteIdentity string
	Preview        *model.Preview
	Input          *RecordInput
	Model          string // host agent model name, provenance only
}

// Record persists a delegation result as a standard OCR session: session_start,
// one checkpoint per reviewable file, and session_end embedding the run
// manifest. No llm_request/llm_response records are written — OCR did not
// observe the host agent's LLM calls, and fabricating them would misrepresent
// provenance. Checkpoint fingerprints stay empty so the native engine's resume
// can never reuse a delegate conclusion as its own.
func Record(args RecordArgs) (string, *session.RunManifest, error) {
	if err := ValidateRecordInput(args.Preview, args.Input); err != nil {
		return "", nil, err
	}

	sh := session.New(args.RepoDir, args.GitBranch, args.Model, session.SessionOptions{
		ReviewMode: sessionReviewMode(args.Mode),
		DiffFrom:   args.From,
		DiffTo:     args.To,
		DiffCommit: args.Commit,
		LLMSource:  "delegate",
		Operation:  session.OperationDelegate,
	})
	if !sh.HasPersistence() {
		return "", nil, sh.Finalize()
	}

	b := sh.Manifest()
	b.SetExecution(session.ManifestExecution{
		OCRVersion: llm.AppVersion,
		Provider:   "delegate",
		Model:      args.Model,
	})
	in := session.ManifestInput{
		Mode:         args.Mode,
		ResolvedBase: args.Resolution.ResolvedBase,
		ResolvedHead: args.Resolution.ResolvedHead,
		ExactRange:   args.Resolution.ExactRange,
	}
	switch args.Mode {
	case session.InputModeRange:
		in.RequestedFrom = args.From
		in.RequestedHead = args.To
	case session.InputModeCommit:
		in.RequestedHead = args.Commit
	case session.InputModeScan:
		// Scan has no diff baseline, so no endpoints are recorded.
	}
	// SourceArtifactSHA256 stays empty: it is built from raw diff fingerprints,
	// which delegate checkpoints deliberately do not carry. Resume refuses
	// delegate sessions on operation, so the artifact identity is not needed.
	b.SetInput(in)
	if args.RemoteIdentity != "" {
		sum := sha256.Sum256([]byte(args.RemoteIdentity))
		b.SetRepository(session.ManifestRepository{IdentitySHA256: hex.EncodeToString(sum[:])})
	}

	// Preview carries no old path, so every item is keyed on the new path alone.
	// The derivation only needs to be consistent within this run: delegate
	// sessions are never resumed or cross-referenced against engine items.
	itemID := func(path string) string {
		return session.ItemID(session.OperationDelegate, args.Mode, "", path)
	}

	reviewable := make(map[string]bool)
	for _, e := range args.Preview.Entries {
		if !e.WillReview {
			continue
		}
		reviewable[e.Path] = true
		if err := b.RegisterSelected(session.CoverageItem{ItemID: itemID(e.Path), Path: e.Path}); err != nil {
			return sh.SessionID, nil, fmt.Errorf("register selected %q: %w", e.Path, err)
		}
	}
	for _, e := range args.Preview.Entries {
		if e.WillReview || reviewable[e.Path] {
			continue
		}
		if err := b.RegisterExcluded(session.ExcludedItem{ItemID: itemID(e.Path), Path: e.Path, ExcludeReason: e.ExcludeReason}); err != nil {
			return sh.SessionID, nil, fmt.Errorf("register excluded %q: %w", e.Path, err)
		}
	}
	if err := b.SealSelected(); err != nil {
		return sh.SessionID, nil, fmt.Errorf("seal selected: %w", err)
	}

	commentsByPath := make(map[string][]model.LlmComment)
	for _, c := range args.Input.Comments {
		commentsByPath[c.Path] = append(commentsByPath[c.Path], c)
	}
	outcomeByPath := make(map[string]RecordFile, len(args.Input.Files))
	for _, f := range args.Input.Files {
		outcomeByPath[f.Path] = f
	}

	for _, e := range args.Preview.Entries {
		if !e.WillReview {
			continue
		}
		id := itemID(e.Path)
		f, reported := outcomeByPath[e.Path]
		switch {
		case reported && f.Outcome == OutcomeReviewed:
			sh.RecordReviewItemDone(e.Path, "", e.Path, "", commentsByPath[e.Path])
			if err := b.MarkCompleted(id); err != nil {
				return sh.SessionID, nil, fmt.Errorf("mark completed %q: %w", e.Path, err)
			}
		case reported:
			// The checkpoint error field is shown by `ocr session show` alongside
			// the manifest's redacted copy; both must pass the same redaction floor.
			reason := "host agent skipped: " + session.SanitizeReason(f.Reason)
			sh.RecordReviewItemFailed(e.Path, "", e.Path, "", reason)
			if err := b.MarkFailed(id, session.FailureUnknown, reason); err != nil {
				return sh.SessionID, nil, fmt.Errorf("mark failed %q: %w", e.Path, err)
			}
		default:
			const reason = "not reported by host agent"
			sh.RecordReviewItemFailed(e.Path, "", e.Path, "", reason)
			if err := b.MarkFailed(id, session.FailureUnknown, reason); err != nil {
				return sh.SessionID, nil, fmt.Errorf("mark failed %q: %w", e.Path, err)
			}
		}
	}

	if u := args.Input.Usage; u != nil {
		sh.RecordHostUsage(args.Model, session.TokenUsage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			CacheReadTokens:  u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		})
	}

	m, err := b.Finalize(time.Since(sh.StartTime))
	if err != nil {
		// Persist what was recorded in legacy form rather than losing the run.
		_ = sh.Finalize()
		return sh.SessionID, nil, fmt.Errorf("finalize run manifest: %w", err)
	}
	sh.SetFinalManifest(&m)
	if err := sh.Finalize(); err != nil {
		return sh.SessionID, &m, fmt.Errorf("persist session: %w", err)
	}
	return sh.SessionID, &m, nil
}
