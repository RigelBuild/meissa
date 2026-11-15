// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/samber/mo"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func newFixture(t *testing.T) (string, *fixtureRunner) {
	t.Helper()
	root := t.TempDir()
	for _, entry := range defaultEntries() {
		path := filepath.Join(moduleRoot(t), filepath.FromSlash(entry.File))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, root, entry.File, string(data))
	}
	modulePath := filepath.Join(moduleRoot(t), filepath.FromSlash(moduleFile))
	moduleData, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, moduleFile, string(moduleData))
	return root, &fixtureRunner{
		root: root,
		base: map[string]string{
			pinFile:                  readFixture(t, root, pinFile),
			defaultEntries()[2].File: readFixture(t, root, defaultEntries()[2].File),
			moduleFile:               readFixture(t, root, moduleFile),
		},
		originAvailable:         true,
		failFetch:               false,
		noGot:                   false,
		cancelAfterBuildFailure: false,
		cancelAndFailRestore:    false,
		cancelOnBaseReference:   false,
		cancelOnBuild:           false,
		cancel:                  nil,
		cancelCause:             nil,
		buildCount:              0,
		calls:                   []string{},
		gitDiffRefs:             []string{},
		evalExpressions:         []string{},
		lastEntry:               Entry{Name: "", File: "", SourceAttr: mo.None[string](), VendorAttr: ""},
		lastAttr:                "",
		lastDrv:                 "",
	}
}

func envValue(value string) func(string) string {
	return func(name string) string {
		if name == "RENOVATE_BASE_BRANCH" {
			return value
		}
		return ""
	}
}

func writeFixture(t *testing.T, root, relative, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755)
	if mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	writeErr := os.WriteFile(path, []byte(text), 0o600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
}

func readFixture(t *testing.T, root, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func extractBlock(text string, entry Entry) string {
	if _, hasSource := entry.SourceAttr.Get(); !hasSource {
		return text
	}
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), entry.Name+" = {") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "};" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	return ""
}

func replaceField(t *testing.T, text string, entry Entry, field, value string) string {
	t.Helper()
	block := extractBlock(text, entry)
	pattern := regexp.MustCompile(`(?m)^(\s*` + regexp.QuoteMeta(field) + `\s*=\s*")[^"]*("\s*;\s*)$`)
	if !pattern.MatchString(block) {
		t.Fatalf("missing field %s in %s", field, entry.Name)
	}
	updated := pattern.ReplaceAllString(block, `${1}`+value+`${2}`)
	return strings.Replace(text, block, updated, 1)
}

type fixtureRunner struct {
	root                    string
	base                    map[string]string
	originAvailable         bool
	failFetch               bool
	noGot                   bool
	cancelAfterBuildFailure bool
	cancelAndFailRestore    bool
	cancelOnBaseReference   bool
	cancelOnBuild           bool
	cancel                  context.CancelCauseFunc
	cancelCause             error
	buildCount              int
	calls                   []string
	gitDiffRefs             []string
	evalExpressions         []string
	lastEntry               Entry
	lastAttr                string
	lastDrv                 string
}

func (r *fixtureRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	contextErr := ctx.Err()
	if contextErr != nil {
		return "", "", 0, fmt.Errorf("fixture command context: %w", contextErr)
	}
	if name == "git" {
		return r.runGit(args)
	}
	if name != "nix" {
		return "", "unexpected command", 127, nil
	}
	return r.runNix(args)
}

func (r *fixtureRunner) runWithEnv(
	ctx context.Context,
	environment map[string]string,
	name string,
	args ...string,
) (string, string, int, error) {
	if len(args) >= 4 && name == "git" && args[0] == "-C" &&
		args[2] == "log" && environment["TZ"] == "UTC" {
		return "2026-01-03\n", "", 0, nil
	}
	return r.Run(ctx, name, args...)
}
func (r *fixtureRunner) runGit(args []string) (string, string, int, error) {
	switch {
	case len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--show-toplevel":
		return r.root, "", 0, nil
	case len(args) >= 4 && args[0] == "rev-parse" && args[1] == "--verify":
		return r.runGitBaseReference()
	case len(args) >= 5 && args[0] == "diff" && args[1] == "--quiet":
		return r.runGitDiff(args)
	case len(args) == 2 && args[0] == "show":
		return r.runGitShow(args)
	case len(args) >= 3 && args[0] == "-C" && args[2] == "init":
		return "", "", 0, nil
	case len(args) >= 3 && args[0] == "-C" && args[2] == "fetch":
		if r.failFetch {
			return "", "fatal: fetch failed\n", 1, nil
		}
		return "", "", 0, nil
	default:
		return "", "unexpected git command", 127, nil
	}
}

func (r *fixtureRunner) runGitBaseReference() (string, string, int, error) {
	if r.cancelOnBaseReference {
		r.cancel(r.cancelCause)
		return "", "fixture git call interrupted", 1, fmt.Errorf("%w", context.Canceled)
	}
	if r.originAvailable {
		return "origin/main\n", "", 0, nil
	}
	return "", "", 1, nil
}

func (r *fixtureRunner) runGitDiff(args []string) (string, string, int, error) {
	ref, relative := args[2], args[len(args)-1]
	r.gitDiffRefs = append(r.gitDiffRefs, ref)
	current, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(relative)))
	if err != nil {
		return "", err.Error(), 1, fmt.Errorf("read fixture for git diff: %w", err)
	}
	if string(current) == r.base[relative] {
		return "", "", 0, nil
	}
	return "", "", 1, nil
}

func (r *fixtureRunner) runGitShow(args []string) (string, string, int, error) {
	refPath := strings.SplitN(args[1], ":", 2)
	if len(refPath) != 2 {
		return "", "invalid git show argument", 1, nil
	}
	text, ok := r.base[refPath[1]]
	if !ok {
		return "", "path not in base", 1, nil
	}
	return text, "", 0, nil
}

func (r *fixtureRunner) runNix(args []string) (string, string, int, error) {
	if len(args) == 0 {
		return "", "missing nix command", 2, nil
	}
	switch args[0] {
	case "eval":
		return r.runNixEval(args)
	case "build":
		return r.runNixBuild()
	default:
		return "", "unexpected nix command", 127, nil
	}
}

func (r *fixtureRunner) runNixEval(args []string) (string, string, int, error) {
	expression := valueAfter(args, "--expr")
	r.evalExpressions = append(r.evalExpressions, expression)
	for _, entry := range defaultEntries() {
		r.recordNixEval(entry, expression)
	}
	if r.lastEntry.Name == "" {
		return "", "unexpected nix eval expression", 2, nil
	}
	r.lastDrv = "/nix/store/" + r.lastEntry.Name + "-" + r.lastAttr + ".drv"
	return r.lastDrv + "\n", "", 0, nil
}

func (r *fixtureRunner) recordNixEval(entry Entry, expression string) {
	source, hasSource := entry.SourceAttr.Get()
	if !hasSource {
		if strings.Contains(expression, "packages.x86_64-linux.tools.goModules.drvPath") {
			r.lastEntry, r.lastAttr = entry, entry.VendorAttr
		}
		return
	}
	if !strings.Contains(expression, "tools."+entry.Name+".") {
		return
	}
	if strings.Contains(expression, "."+source+".drvPath") {
		r.lastEntry, r.lastAttr = entry, source
	} else if strings.Contains(expression, "."+entry.VendorAttr+".drvPath") {
		r.lastEntry, r.lastAttr = entry, entry.VendorAttr
	}
}

func (r *fixtureRunner) runNixBuild() (string, string, int, error) {
	r.buildCount++
	if r.cancelAndFailRestore {
		return r.runInterruptedBuild(true)
	}
	if r.cancelOnBuild {
		return r.runInterruptedBuild(false)
	}
	if r.noGot {
		if r.cancelAfterBuildFailure {
			r.cancel(r.cancelCause)
		}
		return "", "error: build failed without a hash mismatch\n", 1, nil
	}
	path := filepath.Join(r.root, filepath.FromSlash(r.lastEntry.File))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err.Error(), 1, fmt.Errorf("read fixture for nix build: %w", err)
	}
	return r.nixHashMismatch(string(data))
}

func (r *fixtureRunner) runInterruptedBuild(failRestore bool) (string, string, int, error) {
	if failRestore {
		path := filepath.Join(r.root, filepath.FromSlash(pinFile))
		err := os.Remove(path)
		if err != nil {
			return "", "", 0, fmt.Errorf("remove fixture pins: %w", err)
		}
		err = os.Mkdir(path, 0o700)
		if err != nil {
			return "", "", 0, fmt.Errorf("replace fixture pins with directory: %w", err)
		}
	}
	r.cancel(r.cancelCause)
	return "", "", 0, fmt.Errorf("fixture build interrupted: %w", context.Canceled)
}

func (r *fixtureRunner) nixHashMismatch(text string) (string, string, int, error) {
	field := "vendorHash"
	got := "sha256-stub-" + r.lastEntry.Name + "-vendor"
	if r.lastAttr != r.lastEntry.VendorAttr {
		field = "hash"
		got = "sha256-stub-" + r.lastEntry.Name + "-source"
	}
	current, ok := fixtureHashValue(text, field, r.lastEntry)
	if !ok {
		return "", "missing hash field", 2, nil
	}
	if current == got {
		return "", "", 0, nil
	}
	output := fmt.Sprintf(
		"error: hash mismatch in fixed-output derivation '%s':\n         specified: sha256-fake\n            got:    %s\n",
		r.lastDrv,
		got,
	)
	return "", output, 1, nil
}

func fixtureHashValue(text, field string, entry Entry) (string, bool) {
	block := extractBlock(text, entry)
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `\s*=\s*"([^"]+)"`)
	match := pattern.FindStringSubmatch(block)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func valueAfter(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
