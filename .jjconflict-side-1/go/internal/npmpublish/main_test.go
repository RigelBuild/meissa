// SPDX-License-Identifier: MIT OR Apache-2.0

package npmpublish

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMainRun(t *testing.T) {
	privateOnly := t.TempDir()
	write(
		t,
		filepath.Join(privateOnly, "packages", "internal", "package.json"),
		`{"name":"@rigelbuild/internal","private":true}`,
	)
	cwdPrivateOnly := t.TempDir()
	write(t, filepath.Join(cwdPrivateOnly, "packages", "x", "package.json"), `{"name":"@rigelbuild/x","private":true}`)

	tests := []struct {
		name       string
		cwd        string
		env        map[string]string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "publish all skips when every package is private",
			cwd:        "",
			env:        map[string]string{"GITHUB_WORKSPACE": privateOnly, "PUBLISH_ALL": "true"},
			wantCode:   0,
			wantStdout: "skip: no npm package released\n",
			wantStderr: "",
		},
		{
			name:       "publish all fails without a packages directory",
			cwd:        "",
			env:        map[string]string{"GITHUB_WORKSPACE": t.TempDir(), "PUBLISH_ALL": "true"},
			wantCode:   1,
			wantStdout: "",
			wantStderr: "npm-publish: list packages: ",
		},
		{
			name:       "release without a package skips without touching the registry",
			cwd:        "",
			env:        map[string]string{"GITHUB_WORKSPACE": t.TempDir(), "PATHS_RELEASED": `["go","docs"]`},
			wantCode:   0,
			wantStdout: "skip: no npm package released\n",
			wantStderr: "",
		},
		{
			name:       "malformed paths_released fails",
			cwd:        "",
			env:        map[string]string{"GITHUB_WORKSPACE": t.TempDir(), "PATHS_RELEASED": "[]"},
			wantCode:   1,
			wantStdout: "",
			wantStderr: "npm-publish: bad paths_released\n",
		},
		{
			name:       "unset workspace falls back to the working directory",
			cwd:        cwdPrivateOnly,
			env:        map[string]string{"PUBLISH_ALL": "true"},
			wantCode:   0,
			wantStdout: "skip: no npm package released\n",
			wantStderr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cwd != "" {
				t.Chdir(tt.cwd)
			}
			stdout := new(strings.Builder)
			stderr := new(strings.Builder)
			code := Main(t.Context(), nil, envOf(tt.env), stdout, stderr)
			if code != tt.wantCode {
				t.Fatalf("Main exit = %d, want %d", code, tt.wantCode)
			}
			if stdout.String() != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			switch {
			case tt.wantStderr == "" && stderr.Len() != 0:
				t.Fatalf("stderr = %q, want none", stderr.String())
			case tt.wantStderr != "" && !strings.HasPrefix(stderr.String(), tt.wantStderr):
				t.Fatalf("stderr = %q, want prefix %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}
