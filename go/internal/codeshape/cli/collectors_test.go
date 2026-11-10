// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func goTestRepo(t *testing.T, source string) string {
	t.Helper()
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	trackedFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.25\n")
	trackedFile(t, root, "x.go", source)
	commitFiles(t, root)
	return root
}
func runGoGate(t *testing.T, root string) (int, string, string) {
	t.Helper()
	var out, errout strings.Builder
	code := Main(t.Context(), []string{root}, func(string) string { return "" }, &out, &errout)
	return code, out.String(), errout.String()
}
func TestSkipOnlyModuleIsValid(t *testing.T) {
	root := goTestRepo(t, "//go:build integration\n\npackage p\nfunc F() {}\n")
	code, out, stderr := runGoGate(t, root)
	if code != 0 || out != "" || !strings.Contains(stderr, "code-shape-gate: skip x.go: not in default Go build\n") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr)
	}
}

func TestGoLimitsCLI(t *testing.T) {
	var source strings.Builder
	source.WriteString("package p\nfunc Cognitive(x int) {\n")
	for range 21 {
		source.WriteString("if x > 0 { println(x) }\n")
	}
	source.WriteString("}\nfunc Lines() {\n")
	for range 60 {
		source.WriteString("\n")
	}
	source.WriteString("println(1)\n}\nfunc Statements() {\n")
	for range 41 {
		source.WriteString("println(1)\n")
	}
	source.WriteString("}\n")
	root := goTestRepo(t, source.String())
	code, out, errout := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "go-cognitive value=21") ||
		!strings.Contains(out, "go-lines value=61") || !strings.Contains(out, "go-statements value=41") {
		t.Fatalf("code=%d out=%s stderr=%s", code, out, errout)
	}
}
func TestGoMissingBinary(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc F() {}\n")
	dir := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(git, filepath.Join(dir, "git"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "golangci-lint") {
		t.Fatalf("code=%d stderr=%s", code, errout)
	}
}
func fakeGoLint(t *testing.T, output string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'golangci-lint has version 2.15.0'; exit 0; fi\n" +
		"printf '%s' '" + output + "'\nexit 1\n"
	err := os.WriteFile(filepath.Join(dir, "golangci-lint"), []byte(script), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
func TestGoTruncatedOutput(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc F() {}\n")
	fakeGoLint(t, `{"Issues":[`)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "parse golangci-lint") {
		t.Fatalf("code=%d stderr=%s", code, errout)
	}
}
func TestGoUnparseableIssue(t *testing.T) {
	root := goTestRepo(t, "package p\nfunc F() {}\n")
	fakeGoLint(t, `{"Issues":[{"FromLinter":"gocognit","Text":"unexpected",`+
		`"Pos":{"Filename":"x.go","Line":2,"Column":1}}],`+
		`"Report":{"Linters":[{"Name":"gocognit","Enabled":true}]}}`)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "cannot parse") {
		t.Fatalf("code=%d stderr=%s", code, errout)
	}
}

const tsBiomeConfig = `{"linter":{"rules":{"complexity":{"noExcessiveCognitiveComplexity":` +
	`{"level":"warn","options":{"maxAllowedComplexity":15}},` +
	`"noExcessiveLinesPerFunction":{"level":"warn","options":{"maxLines":50}}}}}}`

func tsRepo(t *testing.T, source string) string {
	t.Helper()
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, strings.Replace(validConfig, `".go", ".cue"`, `".go", ".cue", ".ts"`, 1), "files: {}\n")
	trackedFile(t, root, "biome.json", tsBiomeConfig)
	trackedFile(t, root, "x.ts", source)
	commitFiles(t, root)
	return root
}

func TestSuppressionCoexistsWithJudgeAndSeed(t *testing.T) {
	root := tsRepo(t, "// biome-ignore lint: reason\nfunction f() {}\n")
	trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
	commitFiles(t, root)
	code, out, stderr := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "x.ts:1") || !strings.Contains(out, "large.go") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr)
	}
	var stdout, errout strings.Builder
	code = Main(t.Context(), []string{"--write", "--seed", root}, func(string) string { return "" }, &stdout, &errout)
	allow, err := os.ReadFile(filepath.Join(root, "tools/code-shape-gate/allowlist.cue"))
	if code != 1 || err != nil || !strings.Contains(string(allow), "large.go") ||
		!strings.Contains(stdout.String(), "x.ts:1") {
		t.Fatalf("code=%d out=%q stderr=%q allow=%q err=%v", code, stdout.String(), errout.String(), allow, err)
	}
}
func TestTSCognitiveLimit(t *testing.T) {
	root := tsRepo(t, "function f(x: boolean) {\n"+strings.Repeat("if (x) { console.log(x); }\n", 16)+"}\n")
	code, out, errout := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "ts-cognitive value=16") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errout)
	}
}
func TestTSLinesLimit(t *testing.T) {
	root := tsRepo(t, "function f() {\n"+strings.Repeat("console.log(1);\n", 51)+"}\n")
	code, out, errout := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "ts-lines value=51") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errout)
	}
}

func TestBiomeMeasuredErrors(t *testing.T) {
	source := "function f(x: boolean) {\n" + strings.Repeat("if (x) { console.log(x); }\n", 51) + "}\n"
	root := tsRepo(t, source)
	trackedFile(t, root, "biome.json", strings.ReplaceAll(tsBiomeConfig, `"warn"`, `"error"`))
	commitFiles(t, root)
	code, out, stderr := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "ts-lines") || !strings.Contains(out, "ts-cognitive") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr)
	}
}
func TestTSDrift(t *testing.T) {
	root := tsRepo(t, "function f() {}\n")
	err := os.WriteFile(filepath.Join(root, "biome.json"),
		[]byte(strings.Replace(tsBiomeConfig, `"maxLines":50`, `"maxLines":49`, 1)), 0600)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errout := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "biome.json") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errout)
	}
}
func TestTSSuppression(t *testing.T) {
	root := tsRepo(t, "// biome-ignore lint/complexity/noExcessiveLinesPerFunction: reason\nfunction f() {}\n")
	code, out, errout := runGoGate(t, root)
	if code != 1 || !strings.Contains(out, "x.ts:1") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errout)
	}
}
func TestTSMissingBinary(t *testing.T) {
	root := tsRepo(t, "function f() {}\n")
	dir := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(git, filepath.Join(dir, "git"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "biome") {
		t.Fatalf("code=%d err=%s", code, errout)
	}
}
func TestTSTruncatedJSON(t *testing.T) {
	root := tsRepo(t, "function f() {}\n")
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'Version: 2.5.15'; exit 0; fi\n" +
		"printf '{\"summary\":'\n"
	err := os.WriteFile(filepath.Join(dir, "biome"), []byte(script), 0755)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code, _, errout := runGoGate(t, root)
	if code != 2 || !strings.Contains(errout, "parse biome") {
		t.Fatalf("code=%d err=%s", code, errout)
	}
}
