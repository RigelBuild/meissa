// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5 // Keep TS-derived cases direct and readable.
package dlclaim

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errorWriter reports a closed output destination.
type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestClaimCLI covers command behavior that is distinct from service behavior.
func TestClaimCLI(t *testing.T) {
	t.Run("returns usage errors before posting when surface selection is invalid", func(t *testing.T) {
		var posted bool
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posted = true }))
		t.Cleanup(srv.Close)
		var stdout, stderr bytes.Buffer
		argv := []string{"--config", writeConfig(t, srv.URL), "--ref", "none", "--lane", "x", "--surface", "gamma"}
		code := Main(context.Background(), argv, env(map[string]string{"DL_CLAIM_TOKEN": "token"}), &stdout, &stderr)
		if code != 2 || posted || !strings.Contains(stderr.String(), "Unknown surface: gamma") {
			t.Fatalf("code=%d posted=%v stderr=%q", code, posted, stderr.String())
		}
	})

	t.Run("reports required and invalid token messages unchanged", func(t *testing.T) {
		for _, tc := range []struct {
			name, token, want string
		}{
			{name: "required", want: "DL_CLAIM_TOKEN is required"},
			{
				name:  "invalid",
				token: "bad token",
				want: "DL_CLAIM_TOKEN must contain printable ASCII characters " +
					"without whitespace",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				argv := []string{
					"--config", writeConfig(t, testCounter), "--ref", "none",
					"--lane", "feature/b",
				}
				code := Main(
					context.Background(), argv,
					env(map[string]string{"DL_CLAIM_TOKEN": tc.token}),
					&stdout, &stderr,
				)
				if code != 2 || !strings.Contains(stderr.String(), tc.want) {
					t.Fatalf("code=%d stderr=%q, want %q", code, stderr.String(), tc.want)
				}
			})
		}
	})

	t.Run("returns a failed exit when claimed IDs cannot be written", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ids":[{"id":"DL-377","date":"2026-09-29"}]}`))
		}))
		t.Cleanup(srv.Close)
		argv := []string{
			"--config", writeConfig(t, srv.URL), "--ref", "none",
			"--lane", "feature/b",
		}
		code := Main(
			context.Background(), argv,
			env(map[string]string{"DL_CLAIM_TOKEN": "token"}),
			errorWriter{}, io.Discard,
		)
		if code != 1 {
			t.Fatalf("code=%d, want 1 for output failure", code)
		}
	})
}
