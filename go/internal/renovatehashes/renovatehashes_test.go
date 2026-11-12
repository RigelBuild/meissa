// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"strings"
	"testing"
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
	for _, test := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{name: "refreshes a golangci-lint bump, preserves nilaway, and is idempotent", fn: testRefreshGolangciLintBump},
		{name: "refreshes nilaway hashes and UTC version from a negative-offset commit", fn: testRefreshNilawayBump},
		{name: "selects origin/main when RENOVATE_BASE_BRANCH is unset", fn: testRefreshDefaultBranch},
		{name: "no-ops without building when pins match the base branch", fn: testRefreshNoop},
	} {
		t.Run(test.name, test.fn)
	}
}
func testRefreshGolangciLintBump(t *testing.T) {
	t.Helper()
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
	t.Helper()
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
	t.Helper()
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
	t.Helper()
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
