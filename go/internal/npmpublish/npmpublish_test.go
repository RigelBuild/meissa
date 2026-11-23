// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113,exhaustruct_v5,noinlineerr // The Go table keeps the original TypeScript parity cases direct.
package npmpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAllPackages(t *testing.T) {
	t.Run("a manual dispatch lists exactly the public packages, sorted", func(t *testing.T) {
		root := t.TempDir()
		packages := map[string]any{
			"zeta":     map[string]string{"name": "@rigelbuild/zeta"},
			"alpha":    map[string]any{"name": "@rigelbuild/alpha", "private": false},
			"internal": map[string]any{"name": "@rigelbuild/internal", "private": true},
		}
		for dir, manifest := range packages {
			packageDir := filepath.Join(root, "packages", dir)
			if err := os.MkdirAll(packageDir, 0o755); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(packageDir, "package.json"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "packages", "README.md"), []byte("not a package"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := allPackages(root)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"packages/alpha", "packages/zeta"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("allPackages() = %v, want %v", got, want)
		}
	})
}

type testDeps struct {
	statuses   map[string]int
	attempted  []string
	published  []string
	lines      []string
	publishErr error
}

func (d *testDeps) Read(path string) (Manifest, error) {
	return Manifest{Name: "@rigelbuild/" + filepath.Base(path), Version: "1.0.0"}, nil
}

func (d *testDeps) Status(_ context.Context, url string) (int, error) {
	status, ok := d.statuses[url]
	if !ok {
		return http.StatusNotFound, nil
	}
	return status, nil
}

func (d *testDeps) Publish(_ context.Context, path string) error {
	d.attempted = append(d.attempted, path)
	if d.publishErr != nil {
		return d.publishErr
	}
	d.published = append(d.published, path)
	return nil
}

func (d *testDeps) Log(line string) {
	d.lines = append(d.lines, line)
}

func registryURL(pkg string) string {
	return "https://registry.npmjs.org/@rigelbuild%2f" + pkg + "/1.0.0"
}

func TestParsePaths(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
		bad  bool
	}{
		{
			name: "accepts a non-empty string array",
			raw:  `["packages/a","packages/b"]`,
			want: []string{"packages/a", "packages/b"},
		},
		{name: `rejects ""`, raw: "", bad: true},
		{name: "rejects []", raw: "[]", bad: true},
		{name: `rejects "packages/a"`, raw: `"packages/a"`, bad: true},
		{name: "rejects [1]", raw: "[1]", bad: true},
		{name: "rejects undefined", raw: "", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePaths(tt.raw)
			if tt.bad {
				if err == nil {
					t.Fatal("ParsePaths() succeeded, want an error")
				}
				if !errors.Is(err, errBadPathsReleased) {
					t.Fatalf("ParsePaths() error = %v, want %v", err, errBadPathsReleased)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParsePaths() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPublished(t *testing.T) {
	t.Run("fails on a registry error instead of publishing", func(t *testing.T) {
		deps := &testDeps{
			statuses:  map[string]int{registryURL("a"): http.StatusServiceUnavailable},
			published: []string{},
			lines:     []string{},
		}
		_, err := IsPublished(
			context.Background(),
			Manifest{Name: "@rigelbuild/a", Version: "1.0.0"},
			deps,
		)
		if err == nil || err.Error() != registryURL("a")+": registry returned 503" {
			t.Fatalf("IsPublished() error = %v, want registry returned 503", err)
		}
	})
}

func TestPublishAll(t *testing.T) {
	t.Run("re-run skips versions already on npm and publishes the rest", func(t *testing.T) {
		deps := &testDeps{
			statuses:  map[string]int{registryURL("gate"): http.StatusOK},
			published: []string{},
			lines:     []string{},
		}
		if err := PublishAll(context.Background(), []string{"packages/gate", "packages/list"}, deps); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(deps.published, []string{"packages/list"}) {
			t.Fatalf("published = %v, want [packages/list]", deps.published)
		}
		want := []string{
			"skip @rigelbuild/gate@1.0.0: already on npm",
			"published @rigelbuild/list@1.0.0",
		}
		if !reflect.DeepEqual(deps.lines, want) {
			t.Fatalf("logs = %v, want %v", deps.lines, want)
		}
	})

	t.Run("stops at the first failed publish", func(t *testing.T) {
		deps := &testDeps{
			statuses:   map[string]int{},
			published:  []string{},
			lines:      []string{},
			publishErr: errors.New("npm publish failed in packages/a"),
		}
		err := PublishAll(context.Background(), []string{"packages/a", "packages/b"}, deps)
		if err == nil || err.Error() != "npm publish failed in packages/a" {
			t.Fatalf("PublishAll() error = %v, want first package failure", err)
		}
		if !reflect.DeepEqual(deps.attempted, []string{"packages/a"}) {
			t.Fatalf("publish attempts = %v, want [packages/a]", deps.attempted)
		}
	})
}

func TestPublishReleased(t *testing.T) {
	t.Run("publishes only package paths from a mixed release", func(t *testing.T) {
		deps := &testDeps{statuses: map[string]int{}, published: []string{}, lines: []string{}, publishErr: nil}
		if err := publishReleased(context.Background(), []string{"go", "packages/dl-list", "docs"}, deps); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(deps.published, []string{"packages/dl-list"}) {
			t.Fatalf("published = %v, want [packages/dl-list]", deps.published)
		}
	})

	t.Run("skips when no npm package was released", func(t *testing.T) {
		deps := &testDeps{statuses: map[string]int{}, published: []string{}, lines: []string{}, publishErr: nil}
		if err := publishReleased(context.Background(), []string{"go"}, deps); err != nil {
			t.Fatal(err)
		}
		if len(deps.published) != 0 {
			t.Fatalf("published = %v, want none", deps.published)
		}
		want := []string{"skip: no npm package released"}
		if !reflect.DeepEqual(deps.lines, want) {
			t.Fatalf("logs = %v, want %v", deps.lines, want)
		}
	})
}

func TestMainUsesPublishAllAndGitHubWorkspace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "packages", "internal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"name":"@rigelbuild/internal","version":"1.0.0","private":true}`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env := func(name string) string {
		switch name {
		case "PUBLISH_ALL":
			return "true"
		case "GITHUB_WORKSPACE":
			return root
		default:
			return ""
		}
	}
	if code := Main(context.Background(), nil, env, &stdout, &stderr); code != 0 {
		t.Fatalf("Main() code = %d, want 0; stderr = %s", code, stderr.String())
	}
	if got, want := strings.TrimSpace(stdout.String()), "skip: no npm package released"; got != want {
		t.Fatalf("Main() stdout = %q, want %q", got, want)
	}
}

func TestRunMain(t *testing.T) {
	t.Run("returns exit 1 and writes publish error to stderr", func(t *testing.T) {
		publishErr := errors.New("npm publish failed in packages/a")
		deps := &testDeps{
			statuses:   map[string]int{},
			published:  []string{},
			lines:      []string{},
			publishErr: publishErr,
		}
		root := t.TempDir()
		env := func(name string) string {
			switch name {
			case "PATHS_RELEASED":
				return `["packages/a"]`
			case "GITHUB_WORKSPACE":
				return root
			default:
				return ""
			}
		}
		var stderr bytes.Buffer
		code := runMain(context.Background(), env, &stderr, func(string) Deps { return deps })
		if code != 1 {
			t.Fatalf("runMain() code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), publishErr.Error()) {
			t.Fatalf("runMain() stderr = %q, want it to contain %q", stderr.String(), publishErr)
		}
	})

	t.Run("uses cwd when GitHub workspace is unset", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		packageDir := filepath.Join(root, "packages", "x")
		if err := os.MkdirAll(packageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := []byte(`{"name":"x","version":"1.0.0"}`)
		if err := os.WriteFile(filepath.Join(packageDir, "package.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		env := func(name string) string {
			if name == "PUBLISH_ALL" {
				return "true"
			}
			return ""
		}
		deps := &testDeps{statuses: map[string]int{}, published: []string{}, lines: []string{}}
		var gotRoot string
		var stderr bytes.Buffer
		code := runMain(context.Background(), env, &stderr, func(root string) Deps {
			gotRoot = root
			return deps
		})
		if code != 0 {
			t.Fatalf("runMain() code = %d, want 0; stderr = %s", code, stderr.String())
		}
		if gotRoot != root {
			t.Fatalf("depsForRoot() root = %q, want %q", gotRoot, root)
		}
	})
}
