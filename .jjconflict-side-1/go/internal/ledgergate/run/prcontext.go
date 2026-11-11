// SPDX-License-Identifier: MIT OR Apache-2.0

package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// PRContext says where the gate reads its pull request: a GitHub PR, a
// Woodpecker pull request, a non-PR run, or invalid coordinates.
type PRContext interface{ isPRContext() }

// PRContextPR names one GitHub pull request. ReadPullRequest reads it.
type PRContextPR struct{ Repo, PRNumber string }

// PRContextWoodpecker carries the changed files and PR fields from the CI env.
type PRContextWoodpecker struct{ Changed core.Changed }

// PRContextSkip means the run is not a pull request, so PR checks are skipped.
type PRContextSkip struct{}

// PRContextError means the run is a pull request but its context is invalid.
type PRContextError struct{ Message string }

func (e PRContextError) Error() string { return e.Message }

func (PRContextPR) isPRContext()         {}
func (PRContextWoodpecker) isPRContext() {}
func (PRContextSkip) isPRContext()       {}
func (PRContextError) isPRContext()      {}

// PRMetadata is the PR body, head branch, changed files and author fields.
type PRMetadata struct {
	Changed            core.Changed
	PullRequestDetails core.PullRequestDetails
}

// GhRunner runs one command and returns its stdout.
type GhRunner func(ctx context.Context, args []string) (string, error)

var prNumberRE = regexp.MustCompile(`^[1-9][0-9]*$`)

var (
	errNoPRContext            = errors.New("the PR checks need valid PR context")
	errInvalidCIPipelineFiles = errors.New(
		"invalid CI_PIPELINE_FILES: CI_PIPELINE_FILES must be a JSON array of strings",
	)
	//nolint:staticcheck // TypeScript diagnostic text remains capitalized.
	errPRMetadataMustBeObject = errors.New("Pull request metadata must be an object")
	//nolint:staticcheck // TypeScript diagnostic text remains capitalized.
	errPRMetadataMalformed = errors.New("Pull request metadata is malformed")
)

// PRContextFrom reads the PR coordinates from the CI environment. env returns ""
// for an unset variable. A set but empty CI_PIPELINE_FILES is therefore treated
// as unset, where the TypeScript gate reports a parse error.
func PRContextFrom(env func(string) string) PRContext {
	if env("CI_PIPELINE_EVENT") == "pull_request" {
		files, err := woodpeckerFiles(env("CI_PIPELINE_FILES"))
		if err != nil {
			return PRContextError{Message: err.Error()}
		}
		return PRContextWoodpecker{Changed: core.Changed{
			Files:      files,
			Body:       mo.Some(env("CI_COMMIT_PULL_REQUEST_BODY")),
			HeadBranch: env("CI_COMMIT_SOURCE_BRANCH"),
		}}
	}
	repo := env("REPO")
	prNumber := env("PR_NUMBER")
	if repo != "" && prNumberRE.MatchString(prNumber) {
		return PRContextPR{Repo: repo, PRNumber: prNumber}
	}
	if env("GITHUB_EVENT_NAME") == "pull_request" || prNumber != "" {
		return PRContextError{Message: errNoPRContext.Error()}
	}
	return PRContextSkip{}
}

// woodpeckerFiles parses CI_PIPELINE_FILES as a JSON array of strings.
func woodpeckerFiles(value string) ([]string, error) {
	if value == "" {
		return nil, errNoPRContext
	}
	var raw []*string
	err := json.Unmarshal([]byte(value), &raw)
	if err != nil {
		if _, ok := errors.AsType[*json.SyntaxError](err); ok {
			return nil, fmt.Errorf("invalid CI_PIPELINE_FILES: %w", err)
		}
		return nil, errInvalidCIPipelineFiles
	}
	if raw == nil {
		return nil, errInvalidCIPipelineFiles
	}
	files := make([]string, 0, len(raw))
	for _, file := range raw {
		if file == nil {
			return nil, errInvalidCIPipelineFiles
		}
		files = append(files, *file)
	}
	return files, nil
}

// ReadPullRequest reads the PR metadata and changed file names with the gh CLI.
func ReadPullRequest(ctx context.Context, repo, number string) (PRMetadata, error) {
	return readPullRequestWith(ctx, runGhCommand, repo, number)
}

// readPullRequestWith keeps the TypeScript argv, including the timeout wrappers,
// so a test runner can replace gh.
func readPullRequestWith(ctx context.Context, ghRun GhRunner, repo, number string) (PRMetadata, error) {
	metadataText, err := ghRun(ctx, []string{
		"timeout", "30", "gh", "pr", "view", number, "--repo", repo,
		"--json", "headRefName,body,isCrossRepository,headRepositoryOwner,author",
	})
	if err != nil {
		return PRMetadata{}, err
	}
	metadata, err := ParsePRMetadata([]byte(metadataText))
	if err != nil {
		return PRMetadata{}, err
	}
	files, err := ghRun(ctx, []string{
		"timeout", "60", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/pulls/%s/files", repo, number), "--jq", ".[].filename",
	})
	if err != nil {
		return PRMetadata{}, err
	}
	metadata.Changed.Files = nonEmptyLines(files)
	return metadata, nil
}

// nonEmptyLines splits newline-separated output and drops empty lines.
func nonEmptyLines(text string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// runGhCommand runs one command and returns its stdout, or trimmed stderr on failure.
func runGhCommand(ctx context.Context, args []string) (string, error) {
	//nolint:gosec // This command runs fixed gh argv using trusted CI coordinates.
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			message := strings.TrimSpace(string(exitErr.Stderr))
			//nolint:err113 // Preserve the TypeScript diagnostic, including an empty message.
			return "", errors.New(message)
		}
		return "", err //nolint:wrapcheck // Preserve the TypeScript diagnostic.
	}
	return string(out), nil
}

// ParsePRMetadata reads the `gh pr view --json` object. A missing or non-string
// login leaves its field absent, so no branch is exempt on that field.
func ParsePRMetadata(raw []byte) (PRMetadata, error) {
	var value any
	jsonErr := json.Unmarshal(raw, &value)
	if jsonErr != nil {
		return PRMetadata{}, jsonErr //nolint:wrapcheck // Preserve the TypeScript diagnostic.
	}
	view, ok := value.(map[string]any)
	if !ok {
		return PRMetadata{}, errPRMetadataMustBeObject
	}
	headRef, refOK := view["headRefName"].(string)
	body, bodyOK := view["body"].(string)
	cross, crossPresent := view["isCrossRepository"]
	crossValue, crossOK := cross.(bool)
	if !refOK || !bodyOK || (crossPresent && !crossOK) {
		return PRMetadata{}, errPRMetadataMalformed
	}
	details := core.PullRequestDetails{
		IsCrossRepository:   mo.None[bool](),
		HeadRepositoryOwner: loginOf(view["headRepositoryOwner"], false),
		Author:              loginOf(view["author"], true),
	}
	if crossPresent {
		details.IsCrossRepository = mo.Some(crossValue)
	}
	return PRMetadata{
		Changed:            core.Changed{Files: nil, Body: mo.Some(body), HeadBranch: headRef},
		PullRequestDetails: details,
	}, nil
}

// loginOf returns the `login` of a GitHub user object. An App author is mapped
// to the bot form the exempt rules compare against.
func loginOf(value any, normalize bool) mo.Option[string] {
	record, ok := value.(map[string]any)
	if !ok {
		return mo.None[string]()
	}
	login, ok := record["login"].(string)
	if !ok {
		return mo.None[string]()
	}
	if normalize {
		login = core.NormalizeAuthor(login)
	}
	return mo.Some(login)
}
