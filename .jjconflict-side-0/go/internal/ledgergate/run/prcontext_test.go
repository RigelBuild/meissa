// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113 // Keep TS-derived cases direct and readable.
package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// envOf returns a PRContextFrom lookup over a fixed map. A missing key reads as "".
func envOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// TestPRContextFromValidCoordinatesEnableLeg ports "valid PR coordinates enable the leg".
func TestPRContextFromValidCoordinatesEnableLeg(t *testing.T) {
	got := PRContextFrom(envOf(map[string]string{"REPO": "ExampleOrg/docs", "PR_NUMBER": "1315"}))
	want := PRContextPR{Repo: "ExampleOrg/docs", PRNumber: "1315"}
	if got != want {
		t.Fatalf("PRContextFrom = %#v, want %#v", got, want)
	}
}

// TestPRContextFromFailsClosed ports the table "%s fails closed".
func TestPRContextFromFailsClosed(t *testing.T) {
	cases := map[string]map[string]string{
		"missing coordinates on pull_request": {"GITHUB_EVENT_NAME": "pull_request"},
		"missing repo":                        {"PR_NUMBER": "1315"},
		"invalid number":                      {"REPO": "ExampleOrg/docs", "PR_NUMBER": "abc"},
		"zero":                                {"REPO": "ExampleOrg/docs", "PR_NUMBER": "0"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := PRContextFrom(envOf(env)).(PRContextError); !ok {
				t.Fatalf("PRContextFrom(%v) is not PRContextError", env)
			}
		})
	}
}

// TestPRContextFromSkipsWithoutCoordinates ports the table "%s without PR coordinates skips".
func TestPRContextFromSkipsWithoutCoordinates(t *testing.T) {
	cases := map[string]map[string]string{
		"push with REPO and empty number": {"GITHUB_EVENT_NAME": "push", "REPO": "ExampleOrg/docs", "PR_NUMBER": ""},
		"schedule":                        {"GITHUB_EVENT_NAME": "schedule"},
		"workflow dispatch":               {"GITHUB_EVENT_NAME": "workflow_dispatch"},
		"local":                           {},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if got := PRContextFrom(envOf(env)); got != (PRContextSkip{}) {
				t.Fatalf("PRContextFrom(%v) = %#v, want PRContextSkip", env, got)
			}
		})
	}
}

// TestPRContextFromNonPRDispatchWithCoordinatesRunsLeg ports
// "non-PR dispatch with valid PR coordinates runs the leg".
func TestPRContextFromNonPRDispatchWithCoordinatesRunsLeg(t *testing.T) {
	got := PRContextFrom(envOf(map[string]string{
		"GITHUB_EVENT_NAME": "workflow_dispatch",
		"REPO":              "ExampleOrg/docs",
		"PR_NUMBER":         "1315",
	}))
	want := PRContextPR{Repo: "ExampleOrg/docs", PRNumber: "1315"}
	if got != want {
		t.Fatalf("PRContextFrom = %#v, want %#v", got, want)
	}
}

// TestPRContextFromWoodpeckerCarriesFilesBodyAndBranch ports
// "Woodpecker pull requests carry changed files, body, and branch".
func TestPRContextFromWoodpeckerCarriesFilesBodyAndBranch(t *testing.T) {
	got := PRContextFrom(envOf(map[string]string{
		"CI_PIPELINE_EVENT":           "pull_request",
		"CI_PIPELINE_FILES":           `["docs/designs/ui/record.md"]`,
		"CI_COMMIT_PULL_REQUEST_BODY": "Issue details",
		"CI_COMMIT_SOURCE_BRANCH":     "feature/design",
	}))
	want := PRContextWoodpecker{Changed: core.Changed{
		Files:      []string{"docs/designs/ui/record.md"},
		Body:       mo.Some("Issue details"),
		HeadBranch: "feature/design",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PRContextFrom = %#v, want %#v", got, want)
	}
}

// TestPRContextFromNonPRWoodpeckerEventSkips ports "non-PR Woodpecker events skip pull-request checks".
func TestPRContextFromNonPRWoodpeckerEventSkips(t *testing.T) {
	got := PRContextFrom(envOf(map[string]string{
		"CI_PIPELINE_EVENT": "push",
		"CI_PIPELINE_FILES": `["docs/designs/ui/record.md"]`,
	}))
	if got != (PRContextSkip{}) {
		t.Fatalf("PRContextFrom = %#v, want PRContextSkip", got)
	}
}

func TestWoodpeckerFilesErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "invalid JSON",
			value: `["file"`,
			want:  "invalid CI_PIPELINE_FILES: unexpected end of JSON input",
		},
		{
			name:  "wrong shape",
			value: `[1]`,
			want:  "invalid CI_PIPELINE_FILES: CI_PIPELINE_FILES must be a JSON array of strings",
		},
		{
			name:  "null element",
			value: `["file",null]`,
			want:  "invalid CI_PIPELINE_FILES: CI_PIPELINE_FILES must be a JSON array of strings",
		},
		{
			name:  "null array",
			value: `null`,
			want:  "invalid CI_PIPELINE_FILES: CI_PIPELINE_FILES must be a JSON array of strings",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := woodpeckerFiles(tt.value)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("woodpeckerFiles error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestParsePRMetadataCrossRepositoryOwnerAndAppAuthor ports
// "parses cross-repository owner and GitHub App author metadata".
func TestParsePRMetadataCrossRepositoryOwnerAndAppAuthor(t *testing.T) {
	raw := []byte(`{
		"headRefName": "feature/head",
		"body": "body",
		"isCrossRepository": true,
		"headRepositoryOwner": {"login": "contributor"},
		"author": {"login": "app/renovate"}
	}`)
	got, err := ParsePRMetadata(raw)
	if err != nil {
		t.Fatalf("ParsePRMetadata error: %v", err)
	}
	want := PRMetadata{
		Changed: core.Changed{Files: nil, Body: mo.Some("body"), HeadBranch: "feature/head"},
		PullRequestDetails: core.PullRequestDetails{
			IsCrossRepository:   mo.Some(true),
			HeadRepositoryOwner: mo.Some("contributor"),
			Author:              mo.Some("renovate[bot]"),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRMetadata = %#v, want %#v", got, want)
	}
}

// TestParsePRMetadataRejectsNonObject checks the object guard that TypeScript enforces in parsePrMetadata.
func TestParsePRMetadataRejectsNonObject(t *testing.T) {
	_, err := ParsePRMetadata([]byte(`[]`))
	if err == nil {
		t.Fatal("ParsePRMetadata accepted a JSON array")
	}
}

func TestReadPullRequestReturnsNonObjectMetadataErrorUnchanged(t *testing.T) {
	metadataErr := func() error {
		_, err := ParsePRMetadata([]byte(`[]`))
		return err
	}()
	var calls int
	ghRun := func(context.Context, []string) (string, error) {
		calls++
		return `[]`, nil
	}
	_, err := readPullRequestWith(context.Background(), ghRun, "public/project", "12")
	if err == nil || err.Error() != metadataErr.Error() {
		t.Fatalf("error = %v, want original ParsePRMetadata error %v", err, metadataErr)
	}
	if calls != 1 {
		t.Fatalf("gh calls = %d, want 1", calls)
	}
}

func TestReadPullRequestReturnsFilesErrorUnchanged(t *testing.T) {
	filesErr := errors.New("gh files failed")
	ghRun := func(_ context.Context, args []string) (string, error) {
		if slices.Contains(args, "view") {
			return `{"headRefName":"feature/head","body":"body"}`, nil
		}
		return "", filesErr
	}
	_, err := readPullRequestWith(context.Background(), ghRun, "public/project", "12")
	if err == nil || err.Error() != filesErr.Error() {
		t.Fatalf("error = %v, want original gh files error %v", err, filesErr)
	}
}

// TestReadPullRequestThroughInjectedRunner ports "reads PR metadata and file names through injected GitHub runner".
func TestReadPullRequestThroughInjectedRunner(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, args []string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if slices.Contains(args, "view") {
			return `{"headRefName":"feature/head","body":"body","isCrossRepository":true,` +
				`"headRepositoryOwner":{"login":"contributor"},"author":{"login":"app/renovate"}}`, nil
		}
		return "src/main.ts\ndocs/designs/decision.md\n", nil
	}
	got, err := readPullRequestWith(context.Background(), run, "public/project", "12")
	if err != nil {
		t.Fatalf("readPullRequestWith error: %v", err)
	}
	wantFiles := []string{"src/main.ts", "docs/designs/decision.md"}
	if !reflect.DeepEqual(got.Changed.Files, wantFiles) {
		t.Fatalf("files = %v, want %v", got.Changed.Files, wantFiles)
	}
	if got.Changed.HeadBranch != "feature/head" || !reflect.DeepEqual(got.Changed.Body, mo.Some("body")) {
		t.Fatalf("changed = %+v, want head feature/head and body", got.Changed)
	}
	if len(calls) != 2 {
		t.Fatalf("runner calls = %d, want 2", len(calls))
	}
}

// TestReadPullRequestPropagatesRunnerError checks that a failed gh call fails the read.
func TestReadPullRequestPropagatesRunnerError(t *testing.T) {
	boom := errors.New("gh unavailable")
	run := func(context.Context, []string) (string, error) { return "", boom }
	_, err := readPullRequestWith(context.Background(), run, "public/project", "12")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
}

func TestRunGhCommandReturnsTrimmedStderr(t *testing.T) {
	t.Run("stderr", func(t *testing.T) {
		command := filepath.Join(t.TempDir(), "gh")
		script := []byte(
			"#!/bin/sh\nprintf '  command rejected  \\n' >&2\nexit 1\n",
		)
		err := os.WriteFile(command, script, 0o700)
		if err != nil {
			t.Fatal(err)
		}
		_, err = runGhCommand(context.Background(), []string{command})
		if err == nil || err.Error() != "command rejected" {
			t.Fatalf("runGhCommand error = %v, want trimmed stderr", err)
		}
	})
	t.Run("empty stderr remains empty", func(t *testing.T) {
		command := filepath.Join(t.TempDir(), "gh")
		script := []byte("#!/bin/sh\nexit 1\n")
		err := os.WriteFile(command, script, 0o700)
		if err != nil {
			t.Fatal(err)
		}
		_, err = runGhCommand(context.Background(), []string{command})
		if err == nil || err.Error() != "" {
			t.Fatalf("runGhCommand error = %v, want empty stderr", err)
		}
	})
}
