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

func TestGolangciLintTag(t *testing.T) {
	t.Run("derives valid golangci-lint tags and rejects malformed versions", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			version string
			want    string
			bad     bool
		}{
			{name: "release", version: "2.14.0", want: "v2.14.0", bad: false},
			{name: "prerelease", version: "2.14.0-rc.1", want: "v2.14.0-rc.1", bad: false},
			{name: "existing v prefix", version: "v2.14.0", want: "", bad: true},
			{name: "missing patch", version: "2.14", want: "", bad: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				got, err := golangciLintTag(test.version)
				if test.bad {
					if err == nil || !strings.Contains(err.Error(), "invalid golangci-lint version") {
						t.Fatalf("golangciLintTag(%q) error = %v", test.version, err)
					}
					return
				}
				if err != nil || got != test.want {
					t.Fatalf("golangciLintTag(%q) = %q, %v; want %q", test.version, got, err, test.want)
				}
			})
		}
	})
}

func TestRefresh(t *testing.T) {
	t.Run("refreshes a golangci-lint bump, preserves nilaway, and is idempotent", testRefreshGolangciLintBump)
	t.Run("refreshes nilaway hashes and UTC version from a negative-offset commit", testRefreshNilawayBump)
	t.Run("selects origin/main when RENOVATE_BASE_BRANCH is unset", testRefreshDefaultBranch)
	t.Run("no-ops without building when pins match the base branch", testRefreshNoop)
	t.Run("restores the original pins when SIGTERM interrupts a hash build", func(t *testing.T) {
		testRefreshInterrupted(t, "SIGTERM")
	})
	t.Run("reports SIGINT and restores the original pins", func(t *testing.T) {
		testRefreshInterrupted(t, "SIGINT")
	})
	t.Run("reports SIGINT when base-ref lookup is interrupted", testRefreshInterruptedBeforeSelection)
	t.Run("fails when a selected pin block is missing", testRefreshMissingPinBlock)
	t.Run("fails with a diagnostic and restores the changed pin on missing got hash", testRefreshMissingGotHash)
	t.Run("fails with the stable prefix on nilaway fetch errors and restores pins", testRefreshNilawayFetchFailure)
	t.Run("refreshes the package vendorHash without a source hash", testRefreshPackageHash)
	t.Run("refreshes package vendorHash when go.mod changes", testRefreshModuleHash)
}

func testRefreshGolangciLintBump(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	otherBefore := extractBlock(initial, defaultEntries()[0])
	changed := replaceField(t, initial, defaultEntries()[1], "version", "99.0.0")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	_, err := refresh(t.Context(), root, defaultEntries()[:2], runner, envValue("main"))
	if err != nil {
		t.Fatal(err)
	}
	after := readFixture(t, root, pinFile)
	lint := extractBlock(after, defaultEntries()[1])
	for _, want := range []string{
		`version = "99.0.0";`,
		`tag = "v99.0.0";`,
		`hash = "sha256-stub-golangci-lint-source";`,
		`vendorHash = "sha256-stub-golangci-lint-vendor";`,
	} {
		if !strings.Contains(lint, want) {
			t.Errorf("golangci-lint block missing %q:\n%s", want, lint)
		}
	}
	if got := extractBlock(after, defaultEntries()[0]); got != otherBefore {
		t.Errorf("nilaway block changed:\n%s", got)
	}
	if runner.buildCount != 2 {
		t.Errorf("nix build count = %d, want 2", runner.buildCount)
	}
	if len(runner.evalExpressions) != 2 ||
		!strings.Contains(runner.evalExpressions[0], "tools.golangci-lint.src.drvPath") ||
		!strings.Contains(runner.evalExpressions[1], "tools.golangci-lint.goModules.drvPath") {
		t.Errorf("nix eval expressions = %#v", runner.evalExpressions)
	}
	runner.base[pinFile] = after
	_, err = refresh(t.Context(), root, defaultEntries()[:2], runner, envValue("main"))
	if err != nil {
		t.Fatalf("idempotent refresh: %v", err)
	}
	if runner.buildCount != 2 {
		t.Errorf("idempotent refresh ran %d builds, want 2", runner.buildCount)
	}
}

func testRefreshNilawayBump(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	otherBefore := extractBlock(initial, defaultEntries()[1])
	changed := replaceField(t, initial, defaultEntries()[0], "rev", "0123456789abcdef0123456789abcdef01234567")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	_, err := refresh(t.Context(), root, defaultEntries()[:2], runner, envValue("main"))
	if err != nil {
		t.Fatal(err)
	}
	after := readFixture(t, root, pinFile)
	nilaway := extractBlock(after, defaultEntries()[0])
	for _, want := range []string{
		`version = "0-unstable-2026-01-03";`,
		`rev = "0123456789abcdef0123456789abcdef01234567";`,
		`hash = "sha256-stub-nilaway-source";`,
		`vendorHash = "sha256-stub-nilaway-vendor";`,
	} {
		if !strings.Contains(nilaway, want) {
			t.Errorf("nilaway block missing %q:\n%s", want, nilaway)
		}
	}
	if got := extractBlock(after, defaultEntries()[1]); got != otherBefore {
		t.Errorf("golangci-lint block changed:\n%s", got)
	}
	if runner.buildCount != 2 {
		t.Errorf("nix build count = %d, want 2", runner.buildCount)
	}
}

func testRefreshDefaultBranch(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	base := replaceField(t, initial, defaultEntries()[1], "version", "99.0.9")
	changed := replaceField(t, initial, defaultEntries()[1], "version", "99.0.0")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = base
	_, err := refresh(t.Context(), root, defaultEntries()[:2], runner, envValue(""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extractBlock(readFixture(t, root, pinFile), defaultEntries()[1]), `tag = "v99.0.0";`) {
		t.Fatal("refresh did not use the changed working pin")
	}
	if len(runner.gitDiffRefs) == 0 || runner.gitDiffRefs[0] != "origin/main" {
		t.Fatalf("base refs = %#v, want origin/main", runner.gitDiffRefs)
	}
}

func testRefreshNoop(t *testing.T) {
	root, runner := newFixture(t)
	before := readFixture(t, root, pinFile)
	_, err := refresh(t.Context(), root, defaultEntries(), runner, envValue("main"))
	if err != nil {
		t.Fatal(err)
	}
	if runner.buildCount != 0 {
		t.Fatalf("no-op ran %d nix builds", runner.buildCount)
	}
	if got := readFixture(t, root, pinFile); got != before {
		t.Fatal("no-op changed pin file")
	}
}

func testRefreshInterrupted(t *testing.T, signalName string) {
	t.Helper()
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := strings.Replace(initial, `sha256-m9PoJHescRuT9DiKsb6LL+lQlc7RKAWDiRyf8pNMffA=`, "sha256-previous", 1)
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	ctx, cancel := context.WithCancelCause(t.Context())
	runner.cancel = cancel
	runner.cancelCause = ErrRefreshSIGTERM
	if signalName == "SIGINT" {
		runner.cancelCause = ErrRefreshSIGINT
	}
	runner.cancelOnBuild = true
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	code := mainWithRunner(ctx, nil, envValue("main"), stdout, stderr, runner)
	interruptionMessage := "interrupted by " + signalName
	if code != 1 || !strings.Contains(stderr.String(), interruptionMessage) {
		t.Fatalf("Main = %d, stderr %q; want interruption %q", code, stderr.String(), interruptionMessage)
	}
	if got := readFixture(t, root, pinFile); got != changed {
		t.Fatal("interrupted refresh did not restore the original pin file")
	}
}

func testRefreshInterruptedBeforeSelection(t *testing.T) {
	t.Helper()
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := strings.Replace(initial, `sha256-m9PoJHescRuT9DiKsb6LL+lQlc7RKAWDiRyf8pNMffA=`, "sha256-previous", 1)
	writeFixture(t, root, pinFile, changed)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	runner.cancel = cancel
	runner.cancelCause = ErrRefreshSIGINT
	runner.cancelOnBaseReference = true
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	code := mainWithRunner(ctx, nil, envValue("main"), stdout, stderr, runner)
	want := "renovate-go-analysis: interrupted by SIGINT"
	if code != 1 || !strings.Contains(stderr.String(), want) {
		t.Fatalf("Main = %d, stderr %q; want %q", code, stderr.String(), want)
	}
	if got := readFixture(t, root, pinFile); got != changed {
		t.Fatal("interrupted refresh changed the pin file")
	}
}

func testRefreshMissingPinBlock(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	missingEntry := defaultEntries()[0]
	current := replaceField(t, initial, defaultEntries()[1], "version", "2.14.8")
	current = strings.Replace(current, extractBlock(current, missingEntry), "", 1)
	base := replaceField(t, initial, defaultEntries()[1], "version", "2.14.9")
	base = strings.Replace(base, extractBlock(base, missingEntry), "", 1)
	writeFixture(t, root, pinFile, current)
	runner.base[pinFile] = base
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	code := mainWithRunner(t.Context(), nil, envValue("main"), stdout, stderr, runner)
	want := "renovate-go-analysis: missing pin block for nilaway"
	if code != 1 || !strings.Contains(stderr.String(), want) {
		t.Fatalf("Main = %d, stderr %q; want %q", code, stderr.String(), want)
	}
}

func testRefreshMissingGotHash(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[1], "version", "2.14.3")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	runner.noGot = true
	_, err := refresh(t.Context(), root, defaultEntries()[:2], runner, envValue("main"))
	if err == nil || !strings.Contains(err.Error(), "renovate-go-analysis:") ||
		!strings.Contains(err.Error(), "no got hash") {
		t.Fatalf("refresh error = %v, want prefixed missing got hash", err)
	}
	if got := readFixture(t, root, pinFile); got != changed {
		t.Fatal("failed refresh did not restore the original pin file")
	}
}

func testRefreshNilawayFetchFailure(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[0], "rev", "0123456789abcdef0123456789abcdef01234567")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	runner.failFetch = true
	_, err := refresh(t.Context(), root, defaultEntries()[:2], runner, envValue("main"))
	if err == nil || !strings.Contains(err.Error(), "renovate-go-analysis: nilaway revision fetch failed") {
		t.Fatalf("refresh error = %v", err)
	}
	if got := readFixture(t, root, pinFile); got != changed {
		t.Fatal("failed refresh did not restore the original pin file")
	}
}

func testRefreshPackageHash(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, defaultEntries()[2].File)
	changed := strings.Replace(initial, `sha256-52zI++kDGPe4ytAL76eoTFy/FRg0d6fD+TH+m3dR/s8=`, "sha256-previous", 1)
	writeFixture(t, root, defaultEntries()[2].File, changed)
	runner.base[defaultEntries()[2].File] = initial
	_, err := refresh(t.Context(), root, defaultEntries()[2:], runner, envValue("main"))
	if err != nil {
		t.Fatal(err)
	}
	after := readFixture(t, root, defaultEntries()[2].File)
	if !strings.Contains(after, `vendorHash = "sha256-stub-meissa-tools-vendor";`) {
		t.Fatalf("package vendorHash was not refreshed:\n%s", after)
	}
	if runner.buildCount != 1 || len(runner.evalExpressions) != 1 ||
		!strings.Contains(runner.evalExpressions[0], "packages.x86_64-linux.tools.goModules.drvPath") {
		t.Fatalf("package hash commands: builds=%d evals=%#v", runner.buildCount, runner.evalExpressions)
	}
}

func testRefreshModuleHash(t *testing.T) {
	root, runner := newFixture(t)
	changedModule := strings.Replace(readFixture(t, root, moduleFile), "v2.2.4", "v2.2.5", 1)
	writeFixture(t, root, moduleFile, changedModule)
	runner.base[moduleFile] = strings.Replace(changedModule, "v2.2.5", "v2.2.4", 1)
	_, err := refresh(t.Context(), root, defaultEntries()[2:], runner, envValue("main"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		readFixture(t, root, defaultEntries()[2].File),
		`vendorHash = "sha256-stub-meissa-tools-vendor";`,
	) {
		t.Fatalf("go.mod change did not refresh package vendorHash: builds=%d diffs=%v evals=%v",
			runner.buildCount, runner.gitDiffRefs, runner.evalExpressions)
	}
	if len(runner.evalExpressions) != 1 ||
		!strings.Contains(runner.evalExpressions[0], "packages.x86_64-linux.tools.goModules.drvPath") {
		t.Fatalf("package hash evals = %#v", runner.evalExpressions)
	}
}

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
		originAvailable:       true,
		failFetch:             false,
		noGot:                 false,
		cancel:                nil,
		cancelCause:           nil,
		buildCount:            0,
		calls:                 []string{},
		gitDiffRefs:           []string{},
		evalExpressions:       []string{},
		lastEntry:             Entry{Name: "", File: "", SourceAttr: mo.None[string](), VendorAttr: ""},
		lastAttr:              "",
		lastDrv:               "",
		cancelOnBaseReference: false,
		cancelOnBuild:         false,
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
	root                  string
	base                  map[string]string
	originAvailable       bool
	failFetch             bool
	noGot                 bool
	cancelOnBaseReference bool
	cancelOnBuild         bool
	cancel                context.CancelCauseFunc
	cancelCause           error
	buildCount            int
	calls                 []string
	gitDiffRefs           []string
	evalExpressions       []string
	lastEntry             Entry
	lastAttr              string
	lastDrv               string
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
	if r.cancelOnBuild {
		cause := r.cancelCause
		r.cancel(cause)
		return "", "interrupted by " + cause.Error(), 1, nil
	}
	if r.noGot {
		return "", "error: build failed without a hash mismatch\n", 1, nil
	}
	path := filepath.Join(r.root, filepath.FromSlash(r.lastEntry.File))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err.Error(), 1, fmt.Errorf("read fixture for nix build: %w", err)
	}
	return r.nixHashMismatch(string(data))
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
