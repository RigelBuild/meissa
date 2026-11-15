// SPDX-License-Identifier: MIT OR Apache-2.0

package npmpublish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var errFakePublish = errors.New("npm publish failed")

// fakeDeps records publishes and log lines. Statuses maps a registry URL to an HTTP status.
type fakeDeps struct {
	statuses  map[string]int
	published []string
	lines     []string
	publishFn func(path string) error
}

func (f *fakeDeps) Read(path string) (packageManifest, error) {
	return packageManifest{Name: "@rigelbuild/" + strings.Split(path, "/")[1], Version: "1.0.0"}, nil
}

func (f *fakeDeps) Status(_ context.Context, url string) (int, error) {
	if status, ok := f.statuses[url]; ok {
		return status, nil
	}
	return 404, nil
}

func (f *fakeDeps) Publish(_ context.Context, path string) error {
	if f.publishFn != nil {
		return f.publishFn(path)
	}
	f.published = append(f.published, path)
	return nil
}

func (f *fakeDeps) Log(line string) { f.lines = append(f.lines, line) }

func url(pkg string) string {
	return "https://registry.npmjs.org/@rigelbuild%2f" + pkg + "/1.0.0"
}

func TestParsePaths(t *testing.T) {
	t.Run("accepts a non-empty string array", func(t *testing.T) {
		got, err := parsePaths(`["packages/a","packages/b"]`)
		if err != nil {
			t.Fatalf("parsePaths error = %v", err)
		}
		if !reflect.DeepEqual(got, []string{"packages/a", "packages/b"}) {
			t.Fatalf("parsePaths = %v", got)
		}
	})

	for _, raw := range []string{"", "[]", `"packages/a"`, "[1]"} {
		t.Run("rejects "+raw, func(t *testing.T) {
			err := parsePaths(raw)
			if err == nil {
				t.Fatalf("parsePaths(%q) returned no error", raw)
			}
		})
	}
}

func TestAllPackages(t *testing.T) {
	t.Run("a manual dispatch lists exactly the public packages, sorted", func(t *testing.T) {
		root := t.TempDir()
		pkgs := map[string]string{
			"zeta":     `{"name":"@rigelbuild/zeta"}`,
			"alpha":    `{"name":"@rigelbuild/alpha","private":false}`,
			"internal": `{"name":"@rigelbuild/internal","private":true}`,
		}
		for dir, manifest := range pkgs {
			write(t, filepath.Join(root, "packages", dir, "package.json"), manifest)
		}
		write(t, filepath.Join(root, "packages", "README.md"), "not a package")
		got, err := allPackages(root)
		if err != nil {
			t.Fatalf("allPackages error = %v", err)
		}
		if !reflect.DeepEqual(got, []string{"packages/alpha", "packages/zeta"}) {
			t.Fatalf("allPackages = %v", got)
		}
	})
}

func TestIsPublished(t *testing.T) {
	t.Run("fails on a registry error instead of publishing", func(t *testing.T) {
		d := &fakeDeps{statuses: map[string]int{url("a"): 503}, published: []string{}, lines: []string{}, publishFn: nil}
		_, err := isPublished(t.Context(), packageManifest{Name: "@rigelbuild/a", Version: "1.0.0"}, d)
		if err == nil || !strings.Contains(err.Error(), "registry returned 503") {
			t.Fatalf("isPublished error = %v, want registry returned 503", err)
		}
	})
}

func TestPublishAll(t *testing.T) {
	t.Run("re-run skips versions already on npm and publishes the rest", func(t *testing.T) {
		d := &fakeDeps{statuses: map[string]int{url("gate"): 200}, published: []string{}, lines: []string{}, publishFn: nil}
		err := publishAll(t.Context(), []string{"packages/gate", "packages/list"}, d)
		if err != nil {
			t.Fatalf("publishAll error = %v", err)
		}
		if !reflect.DeepEqual(d.published, []string{"packages/list"}) {
			t.Fatalf("published = %v", d.published)
		}
		want := []string{
			"skip @rigelbuild/gate@1.0.0: already on npm",
			"published @rigelbuild/list@1.0.0",
		}
		if !reflect.DeepEqual(d.lines, want) {
			t.Fatalf("lines = %v, want %v", d.lines, want)
		}
	})
	t.Run("stops at the first failed publish", func(t *testing.T) {
		d := &fakeDeps{statuses: map[string]int{}, published: []string{}, lines: []string{}, publishFn: func(path string) error {
			return fmt.Errorf("%w: %s", errFakePublish, path)
		}}
		err := publishAll(t.Context(), []string{"packages/a", "packages/b"}, d)
		if !errors.Is(err, errFakePublish) || !strings.Contains(err.Error(), "packages/a") {
			t.Fatalf("publishAll error = %v, want fake publish error for packages/a", err)
		}
	})
}

func TestPublishReleased(t *testing.T) {
	t.Run("publishes only package paths from a mixed release", func(t *testing.T) {
		d := &fakeDeps{statuses: map[string]int{}, published: []string{}, lines: []string{}, publishFn: nil}
		err := publishReleased(t.Context(), []string{"go", "packages/length-gate", "docs"}, d)
		if err != nil {
			t.Fatalf("publishReleased error = %v", err)
		}
		if !reflect.DeepEqual(d.published, []string{"packages/length-gate"}) {
			t.Fatalf("published = %v", d.published)
		}
	})

	t.Run("skips when no npm package was released", func(t *testing.T) {
		d := &fakeDeps{statuses: map[string]int{}, published: []string{}, lines: []string{}, publishFn: nil}
		err := publishReleased(t.Context(), []string{"go"}, d)
		if err != nil {
			t.Fatalf("publishReleased error = %v", err)
		}
		if len(d.published) != 0 {
			t.Fatalf("published = %v, want none", d.published)
		}
		if !reflect.DeepEqual(d.lines, []string{"skip: no npm package released"}) {
			t.Fatalf("lines = %v", d.lines)
		}
	})
}

func write(t *testing.T, path, text string) {
	t.Helper()
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	err = os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
