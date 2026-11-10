// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshInterruptions(t *testing.T) {
	t.Run("restores the original pins when SIGTERM interrupts a hash build", func(t *testing.T) {
		testRefreshInterrupted(t, "SIGTERM")
	})
	t.Run("reports SIGINT and restores the original pins", func(t *testing.T) {
		testRefreshInterrupted(t, "SIGINT")
	})
	t.Run("reports SIGINT when base-ref lookup is interrupted", testRefreshInterruptedBeforeSelection)
	t.Run("classifies canceled exec child as SIGINT", testRefreshClassifiesCanceledExecChild)
	t.Run("reports SIGINT when derivation path resolution is interrupted", testResolveDrvPathCancellation)
	t.Run("reports SIGINT when nilaway git init is interrupted", testRefreshNilawayInitCancellation)
	t.Run("reports SIGINT when nilaway git fetch is interrupted", testRefreshNilawayFetchCancellation)
	t.Run("reports SIGINT when nilaway git log is interrupted", testRefreshNilawayLogCancellation)
	t.Run("reports SIGINT when Git root lookup is interrupted", testMainGitRootCancellation)
	t.Run(
		"keeps the original failure when build fails before context cancellation",
		testRefreshBuildFailureBeforeCancellation,
	)
	t.Run("reports interrupt and restore write failure", testRefreshInterruptAndRestoreWriteFailure)
	t.Run("fails when a selected pin block is missing", testRefreshMissingPinBlock)
	t.Run("fails with a diagnostic and restores the changed pin on missing got hash", testRefreshMissingGotHash)
	t.Run("fails with the stable prefix on nilaway fetch errors and restores pins", testRefreshNilawayFetchFailure)
	t.Run("refreshes the package vendorHash without a source hash", testRefreshPackageHash)
	t.Run("refreshes package vendorHash when go.mod changes", testRefreshModuleHash)
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
	pinPath := filepath.Join(root, filepath.FromSlash(pinFile))
	err := os.Chtimes(pinPath, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(pinPath)
	if err != nil {
		t.Fatal(err)
	}
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
	after, err := os.Stat(pinPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("interrupted refresh wrote the pin file")
	}
}

func testRefreshClassifiesCanceledExecChild(t *testing.T) {
	for _, command := range []string{"sh", "sleep", "touch"} {
		_, err := exec.LookPath(command)
		if err != nil {
			t.Skipf("required command %q is unavailable: %v", command, err)
		}
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	root := t.TempDir()
	readyPath := filepath.Join(root, "child-ready")
	errCh := make(chan error, 1)
	go func() {
		_, err := refresh(ctx, root, nil, execRunnerCancellationRunner{readyPath: readyPath}, envValue("main"))
		errCh <- err
	}()

	timeout := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer timeout.Stop()
	defer ticker.Stop()
	for {
		_, statErr := os.Stat(readyPath)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) {
			cancel(ErrRefreshSIGINT)
			childErr := <-errCh
			t.Fatalf("checking exec child readiness: %v (child error: %v)", statErr, childErr)
		}
		select {
		case childErr := <-errCh:
			t.Fatalf("exec child returned before readiness: %v", childErr)
		case <-timeout.C:
			cancel(ErrRefreshSIGINT)
			childErr := <-errCh
			if childErr != nil {
				t.Fatalf("exec child returned before readiness: %v", childErr)
			}
			t.Fatal("exec child did not start")
		case <-ticker.C:
		}
	}
	cancel(ErrRefreshSIGINT)
	err := <-errCh
	if !errors.Is(err, ErrRefreshSIGINT) || !strings.Contains(err.Error(), "interrupted by SIGINT") {
		t.Fatalf("refresh error = %v; want SIGINT interruption", err)
	}
}

func testResolveDrvPathCancellation(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[0], "rev", "0123456789abcdef0123456789abcdef01234567")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cancelOnEval := cancelDuringNixEvalRunner{fixtureRunner: runner, cancel: cancel}
	_, err := refresh(ctx, root, defaultEntries()[:2], cancelOnEval, envValue("main"))
	if !errors.Is(err, ErrRefreshSIGINT) || strings.Count(err.Error(), "interrupted by SIGINT") != 1 {
		t.Fatalf("refresh error = %v; want one SIGINT interruption headline", err)
	}
}

func testRefreshNilawayInitCancellation(t *testing.T) {
	testRefreshNilawayCommandCancellation(t, "init")
}

func testRefreshNilawayFetchCancellation(t *testing.T) {
	testRefreshNilawayCommandCancellation(t, "fetch")
}

func testRefreshNilawayCommandCancellation(t *testing.T, command string) {
	t.Helper()
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[0], "rev", "0123456789abcdef0123456789abcdef01234567")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cancelOnCommand := cancelDuringNilawayInitRunner{fixtureRunner: runner, cancel: cancel, command: command}
	_, err := refresh(ctx, root, defaultEntries()[:2], cancelOnCommand, envValue("main"))
	if !errors.Is(err, ErrRefreshSIGINT) || strings.Count(err.Error(), "interrupted by SIGINT") != 1 {
		t.Fatalf("refresh error = %v; want SIGINT and one interruption headline", err)
	}
}

func testRefreshNilawayLogCancellation(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[0], "rev", "0123456789abcdef0123456789abcdef01234567")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cancelOnLog := cancelDuringNilawayLogRunner{fixtureRunner: runner, cancel: cancel}
	_, err := refresh(ctx, root, defaultEntries()[:2], cancelOnLog, envValue("main"))
	if !errors.Is(err, ErrRefreshSIGINT) || strings.Count(err.Error(), "interrupted by SIGINT") != 1 {
		t.Fatalf("refresh error = %v; want SIGINT and one interruption headline", err)
	}
}

func testMainGitRootCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	stderr := new(strings.Builder)
	code := mainWithRunner(ctx, nil, envValue("main"), new(strings.Builder), stderr,
		cancelDuringGitRootRunner{cancel: cancel},
	)
	if code != 1 || !strings.Contains(stderr.String(), "interrupted by SIGINT") ||
		strings.Contains(stderr.String(), "could not find Git root") {
		t.Fatalf("Main = %d, stderr %q; want interruption without Git root failure", code, stderr.String())
	}
}

type cancelDuringNilawayInitRunner struct {
	*fixtureRunner

	cancel  context.CancelCauseFunc
	command string
}

func (r cancelDuringNilawayInitRunner) Run(
	ctx context.Context,
	name string,
	args ...string,
) (string, string, int, error) {
	if name == "git" && len(args) >= 3 && args[0] == "-C" && args[2] == r.command {
		r.cancel(ErrRefreshSIGINT)
		return "", "", -1, fmt.Errorf("run git: %w", context.Cause(ctx))
	}
	return r.fixtureRunner.Run(ctx, name, args...)
}

type cancelDuringNilawayLogRunner struct {
	*fixtureRunner

	cancel context.CancelCauseFunc
}

func (r cancelDuringNilawayLogRunner) runWithEnv(
	ctx context.Context,
	extraEnv map[string]string,
	name string,
	args ...string,
) (string, string, int, error) {
	if name == "git" && len(args) >= 3 && args[0] == "-C" && args[2] == "log" {
		r.cancel(ErrRefreshSIGINT)
		return "", "", 1, nil
	}
	return r.fixtureRunner.runWithEnv(ctx, extraEnv, name, args...)
}

type cancelDuringGitRootRunner struct {
	cancel context.CancelCauseFunc
}

func (r cancelDuringGitRootRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	if name == "git" && len(args) == 2 && args[0] == "rev-parse" && args[1] == "--show-toplevel" {
		r.cancel(ErrRefreshSIGINT)
		return "", "", -1, fmt.Errorf("run git: %w", context.Cause(ctx))
	}
	return "", "unexpected command", 127, nil
}

type cancelDuringNixEvalRunner struct {
	*fixtureRunner

	cancel context.CancelCauseFunc
}

func (r cancelDuringNixEvalRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	if name == "nix" && len(args) > 0 && args[0] == "eval" {
		r.cancel(ErrRefreshSIGINT)
		return "", "", -1, fmt.Errorf("run nix: %w", context.Cause(ctx))
	}
	return r.fixtureRunner.Run(ctx, name, args...)
}

type execRunnerCancellationRunner struct {
	readyPath string
}

func (r execRunnerCancellationRunner) Run(ctx context.Context, _ string, _ ...string) (string, string, int, error) {
	return (execRunner{root: ""}).Run(ctx, "sh", "-c", `touch "$1"; exec sleep 10`, "sh", r.readyPath)
}

func testRefreshBuildFailureBeforeCancellation(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[1], "version", "2.14.3")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	runner.cancel = cancel
	runner.cancelCause = ErrRefreshSIGINT
	runner.noGot = true
	runner.cancelAfterBuildFailure = true
	stderr := new(strings.Builder)
	code := mainWithRunner(ctx, nil, envValue("main"), new(strings.Builder), stderr, runner)
	if code != 1 || !strings.Contains(stderr.String(), "no got hash") {
		t.Fatalf("Main = %d, stderr %q; want original build failure", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "interrupted by SIGINT") {
		t.Fatalf("non-cancellation error was rewritten: %q", stderr.String())
	}
}

func testRefreshInterruptAndRestoreWriteFailure(t *testing.T) {
	root, runner := newFixture(t)
	initial := readFixture(t, root, pinFile)
	changed := replaceField(t, initial, defaultEntries()[1], "version", "2.14.3")
	writeFixture(t, root, pinFile, changed)
	runner.base[pinFile] = initial
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	runner.cancel = cancel
	runner.cancelCause = ErrRefreshSIGINT
	runner.cancelAndFailRestore = true
	stderr := new(strings.Builder)
	code := mainWithRunner(ctx, nil, envValue("main"), new(strings.Builder), stderr, runner)
	for _, want := range []string{"interrupted by SIGINT", "could not restore", pinFile} {
		if code != 1 || !strings.Contains(stderr.String(), want) {
			t.Fatalf("Main = %d, stderr %q; want %q", code, stderr.String(), want)
		}
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
	changed := strings.Replace(initial, `sha256-gXUE5nvRY4WOvIP0ZM5WscVD6BCXFgjD0OQ+gx/XBFs=`, "sha256-previous", 1)
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
	changedModule := strings.Replace(readFixture(t, root, moduleFile), "v0.17.1", "v0.17.2", 1)
	writeFixture(t, root, moduleFile, changedModule)
	runner.base[moduleFile] = strings.Replace(changedModule, "v0.17.2", "v0.17.1", 1)
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
