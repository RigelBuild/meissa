// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:errchkjson,exhaustruct_v5 // Keep TS-derived cases direct and readable.
package dlreconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

const testPartition = "public-docs"

// depthZero is the single-surface config the TS tests use.
func depthZero() core.Config {
	return core.Config{
		DesignsRoot:   "docs/designs",
		SurfaceDepth:  0,
		GovernedRoots: []string{"ui"},
		Surfaces:      []string{"alpha"},
		Counter:       core.Counter{URL: "https://counter.example.test///", Partition: testPartition},
	}
}

// depthOne is the two-surface config the TS tests use.
func depthOne() core.Config {
	cfg := depthZero()
	cfg.SurfaceDepth = 1
	cfg.GovernedRoots = nil
	cfg.Surfaces = []string{"alpha", "beta"}
	return cfg
}

// decisionFile is a minimal valid decision file for id.
func decisionFile(id string) string {
	return strings.Join([]string{
		"---",
		"id: " + id,
		fmt.Sprintf(`decision: "Choose %s"`, id),
		`status: "Active (Reviewer, 2026-01-01)"`,
		"record: ../record.md",
		"---",
		"Decision body.",
	}, "\n")
}

// reconcileResponse builds the counter's JSON answer.
func reconcileResponse(stale []StaleClaim, duplicates []DuplicateClaim) *http.Response {
	body, _ := json.Marshal(ReconcileResponse{Updated: 1, Inserted: 2, Stale: stale, Duplicates: duplicates})
	return jsonResponse(http.StatusOK, string(body))
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// fixtureRoot writes files under a fresh directory with a config file, and returns both paths.
func fixtureRoot(t *testing.T, cfg core.Config, files map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	for path, text := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(full, []byte(text), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(map[string]any{
		"designsRoot":   cfg.DesignsRoot,
		"surfaceDepth":  cfg.SurfaceDepth,
		"governedRoots": cfg.GovernedRoots,
		"surfaces":      cfg.Surfaces,
		"counter":       map[string]string{"url": cfg.Counter.URL, "partition": cfg.Counter.Partition},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	err = os.WriteFile(configPath, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return root, configPath
}

// env returns a lookup over a fixed map, the way Main reads the environment.
func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// TestAssertReconcilableLedgers covers AssertReconcilableLedgers.
func TestAssertReconcilableLedgers(t *testing.T) {
	t.Run("assigns the configured surface at depth zero", func(t *testing.T) {
		path := "docs/designs/decisions/ui/DL-002.md"
		got, err := AssertReconcilableLedgers(depthZero(), map[string]string{path: decisionFile("DL-002")})
		if err != nil {
			t.Fatal(err)
		}
		want := ReconcileRequest{Repo: testPartition, Landed: []LandedDecision{{ID: "DL-002", Surface: "alpha", Ref: "none"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("takes the depth-one surface from the path and keeps duplicate IDs", func(t *testing.T) {
		files := map[string]string{
			"docs/designs/alpha/decisions/ui/DL-002.md":    decisionFile("DL-002"),
			"docs/designs/beta/decisions/server/DL-002.md": decisionFile("DL-002"),
		}
		got, err := AssertReconcilableLedgers(depthOne(), files)
		if err != nil {
			t.Fatal(err)
		}
		want := []LandedDecision{
			{ID: "DL-002", Surface: "alpha", Ref: "none"},
			{ID: "DL-002", Surface: "beta", Ref: "none"},
		}
		if !reflect.DeepEqual(got.Landed, want) {
			t.Fatalf("landed = %+v, want %+v", got.Landed, want)
		}
	})

	t.Run("refuses unknown surfaces, empty discovery, and malformed files", func(t *testing.T) {
		unknown := map[string]string{"docs/designs/gamma/decisions/ui/DL-001.md": decisionFile("DL-001")}
		_, err := AssertReconcilableLedgers(depthOne(), unknown)
		if err == nil || !strings.Contains(err.Error(), "unknown design surface") {
			t.Fatalf("unknown surface: err = %v", err)
		}
		_, err = AssertReconcilableLedgers(depthOne(), map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "refusing to post an empty frontier") {
			t.Fatalf("empty discovery: err = %v", err)
		}
		malformed := "docs/designs/alpha/decisions/ui/DL-001.md"
		_, err = AssertReconcilableLedgers(depthOne(), map[string]string{malformed: "not front matter"})
		if err == nil || !strings.Contains(err.Error(), malformed+": line 1") {
			t.Fatalf("malformed: err = %v, want %q", err, malformed+": line 1")
		}
	})
}

// TestReconcileService covers Reconcile against a fake counter.
func TestReconcileService(t *testing.T) {
	body := ReconcileRequest{Repo: testPartition, Landed: []LandedDecision{{ID: "DL-001", Surface: "alpha", Ref: "none"}}}

	t.Run("posts to the configured counter and parses stale and duplicate results", func(t *testing.T) {
		stale := []StaleClaim{{ID: "DL-010", Surface: "alpha", Ref: "RIG-2", Lane: "branch", Date: "2026-09-01"}}
		duplicates := []DuplicateClaim{{ID: "DL-010", Surfaces: []string{"alpha", "beta"}}}
		var gotURL, gotAuth string
		var gotBody ReconcileRequest
		client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			gotURL, gotAuth = r.URL.String(), r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			return reconcileResponse(stale, duplicates), nil
		})}
		got, err := Reconcile(context.Background(), client, body, " token ", depthZero())
		if err != nil {
			t.Fatal(err)
		}
		if gotURL != "https://counter.example.test/reconcile" || gotAuth != "Bearer token" {
			t.Fatalf("url=%q auth=%q", gotURL, gotAuth)
		}
		if !reflect.DeepEqual(gotBody, body) {
			t.Fatalf("posted body = %+v, want %+v", gotBody, body)
		}
		if !reflect.DeepEqual(got.Stale, stale) || !reflect.DeepEqual(got.Duplicates, duplicates) {
			t.Fatalf("stale=%+v duplicates=%+v", got.Stale, got.Duplicates)
		}
		want := []string{
			"updated: 1  inserted: 2",
			"stale: 1",
			"  DL-010 alpha branch RIG-2 2026-09-01",
			"duplicates: 1",
			"  DL-010 alpha beta",
		}
		if lines := strings.Split(FormatReconcileOutput(got), "\n"); !reflect.DeepEqual(lines, want) {
			t.Fatalf("output lines = %q, want %q", lines, want)
		}
	})

	t.Run("rejects blank credentials and invalid service responses", func(t *testing.T) {
		var called atomic.Bool
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return reconcileResponse(nil, nil), nil
		})}
		_, err := Reconcile(context.Background(), client, body, " ", depthZero())
		if err == nil || err.Error() != "DL_CLAIM_TOKEN is required" {
			t.Fatalf("blank token: err = %v", err)
		}
		if called.Load() {
			t.Fatal("request sent with a blank token")
		}
		bad := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"updated":1}`), nil
		})}
		_, err = Reconcile(context.Background(), bad, body, "token", depthZero())
		if err == nil || !strings.Contains(err.Error(), "invalid reconciliation response") {
			t.Fatalf("invalid response: err = %v", err)
		}
	})
	t.Run("matches TypeScript reconciliation response errors", func(t *testing.T) {
		for _, body := range []string{
			`null`,
			`{"updated":1,"inserted":2,"stale":[],"duplicates":"bad"}`,
			`{"updated":1,"inserted":2,"stale":[{}],"duplicates":[]}`,
			`{"updated":1,"inserted":2,"stale":[],"duplicates":[{"id":"DL-001","surfaces":[null]}]}`,
		} {
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, body), nil
			})}
			landed := []LandedDecision{{ID: "DL-001", Surface: "alpha", Ref: "none"}}
			request := ReconcileRequest{Repo: testPartition, Landed: landed}
			_, err := Reconcile(context.Background(), client, request, "token", depthZero())
			if err == nil || err.Error() != "invalid reconciliation response" {
				t.Fatalf("body %s: err=%v, want invalid reconciliation response", body, err)
			}
		}
	})

	t.Run("rejects non-ok service responses with their HTTP status", func(t *testing.T) {
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusServiceUnavailable, "failure"), nil
		})}
		_, err := Reconcile(context.Background(), client, body, "token", depthZero())
		if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("err = %v, want HTTP 503", err)
		}
	})

	t.Run("rejects an empty landed frontier before contacting the counter", func(t *testing.T) {
		var called atomic.Bool
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return reconcileResponse(nil, nil), nil
		})}
		empty := ReconcileRequest{Repo: testPartition, Landed: []LandedDecision{}}
		_, err := Reconcile(context.Background(), client, empty, "token", depthZero())
		if err == nil || !strings.Contains(err.Error(), "empty landed frontier") {
			t.Fatalf("err = %v", err)
		}
		if called.Load() {
			t.Fatal("request sent for an empty frontier")
		}
	})

	t.Run("shares the gate token reader and prefers non-empty file content", func(t *testing.T) {
		dir := t.TempDir()
		tokenPath := filepath.Join(dir, "token")
		err := os.WriteFile(tokenPath, []byte(" \n "), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		got, err := claim.ReadDLClaimToken(env(map[string]string{
			"DL_CLAIM_TOKEN":      "env-token",
			"DL_CLAIM_TOKEN_FILE": tokenPath,
		}))
		if err != nil || got != "env-token" {
			t.Fatalf("token = %q, err = %v, want env-token", got, err)
		}
	})
}

func TestRepositoryRootHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := repositoryRoot(ctx, func(string) string { return "" })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("repositoryRoot error = %v, want context.Canceled", err)
	}
}

// TestReconcileCLI covers Main end to end.
func TestReconcileCLI(t *testing.T) {
	gate := func(root string) map[string]string {
		return map[string]string{"GATE_ROOT": root, "DL_CLAIM_TOKEN": "token"}
	}

	t.Run("check mode performs no request and reports discovered count", func(t *testing.T) {
		path := "docs/designs/decisions/ui/DL-001.md"
		root, configPath := fixtureRoot(t, depthZero(), map[string]string{path: decisionFile("DL-001")})
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), []string{"--config", configPath, "--check"}, env(gate(root)), &stdout, &stderr)
		if code != 0 || strings.TrimSpace(stdout.String()) != "Design ledger parse check passed (1 decision files)." {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("returns usage errors for empty discovery and malformed files without posting", func(t *testing.T) {
		cases := []map[string]string{
			{},
			{"docs/designs/decisions/ui/DL-001.md": "broken"},
		}
		for _, files := range cases {
			root, configPath := fixtureRoot(t, depthZero(), files)
			var posted atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posted.Store(true) }))
			var stdout, stderr bytes.Buffer
			code := Main(context.Background(), []string{"--config", configPath}, env(gate(root)), &stdout, &stderr)
			srv.Close()
			if code != 2 || posted.Load() || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("files=%v code=%d posted=%v stderr=%q", files, code, posted.Load(), stderr.String())
			}
		}
	})

	t.Run("unreadable decisions directory exits 2 without posting", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read mode-000 directories")
		}
		var posted atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			posted.Store(true)
		}))
		t.Cleanup(srv.Close)
		cfg := depthZero()
		cfg.Counter.URL = srv.URL
		root, configPath := fixtureRoot(t, cfg, map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		directory := filepath.Join(root, "docs/designs/decisions")
		err := os.Chmod(directory, 0o000)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			err := os.Chmod(directory, 0o755)
			if err != nil {
				t.Errorf("restore decisions directory permissions: %v", err)
			}
		})
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), []string{"--config", configPath}, env(gate(root)), &stdout, &stderr)
		if code != 2 || posted.Load() {
			t.Fatalf(
				"code=%d posted=%v stdout=%q stderr=%q, want exit 2 without posting",
				code, posted.Load(), stdout.String(), stderr.String(),
			)
		}
		// Without fail-on-I/O the walk skips the directory and fails later as an empty list.
		if !strings.Contains(stderr.String(), "permission denied") ||
			strings.Contains(stderr.String(), "decision file list is empty") {
			t.Fatalf("stderr=%q, want the walk permission error", stderr.String())
		}
	})

	t.Run("refuses a misplaced decision-tree file before posting", func(t *testing.T) {
		root, configPath := fixtureRoot(t, depthZero(), map[string]string{
			"docs/designs/decisions/ui/archive/DL-009.md": decisionFile("DL-009"),
		})
		var stderr bytes.Buffer
		code := Main(context.Background(), []string{"--config", configPath}, env(gate(root)), io.Discard, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "misplaced decision path") {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
	})

	t.Run("returns one when the counter rejects the request", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "rejected")
		}))
		t.Cleanup(srv.Close)
		cfg := depthZero()
		cfg.Counter.URL = srv.URL
		root, configPath := fixtureRoot(t, cfg, map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		var stderr bytes.Buffer
		code := Main(context.Background(), []string{"--config", configPath}, env(gate(root)), io.Discard, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "HTTP 503") {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
	})

	t.Run("stale claims leave the default exit status successful", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(ReconcileResponse{Updated: 1, Inserted: 2, Stale: []StaleClaim{
				{ID: "DL-007", Surface: "alpha", Ref: "none", Lane: "branch", Date: "2026-09-01"},
			}, Duplicates: []DuplicateClaim{}})
		}))
		t.Cleanup(srv.Close)
		cfg := depthZero()
		cfg.Counter.URL = srv.URL
		root, configPath := fixtureRoot(t, cfg, map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), []string{"--config", configPath}, env(gate(root)), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code=%d stderr=%q, want 0 without --stale-exit", code, stderr.String())
		}
	})

	t.Run("stale-exit returns one when stale claims remain", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(ReconcileResponse{Updated: 1, Inserted: 2, Stale: []StaleClaim{
				{ID: "DL-007", Surface: "alpha", Ref: "none", Lane: "branch", Date: "2026-09-01"},
			}, Duplicates: []DuplicateClaim{}})
		}))
		t.Cleanup(srv.Close)
		cfg := depthZero()
		cfg.Counter.URL = srv.URL
		root, configPath := fixtureRoot(t, cfg, map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		var stdout, stderr bytes.Buffer
		code := Main(
			context.Background(),
			[]string{"--config", configPath, "--stale-exit"},
			env(gate(root)),
			&stdout,
			&stderr,
		)
		out := stdout.String()
		wrongExit := code != 1
		missingSummary := !strings.Contains(out, "updated: 1  inserted: 2")
		missingStaleClaim := !strings.Contains(out, "DL-007 alpha branch none 2026-09-01")
		if wrongExit || missingSummary || missingStaleClaim {
			t.Fatalf("code=%d stdout=%q", code, out)
		}
	})

	t.Run("rejects an interior-newline token without logging its value", func(t *testing.T) {
		root, configPath := fixtureRoot(t, depthZero(), map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		var stderr bytes.Buffer
		code := Main(context.Background(), []string{"--config", configPath}, env(map[string]string{
			"GATE_ROOT":      root,
			"DL_CLAIM_TOKEN": "safe\nsecret",
		}), io.Discard, &stderr)
		if code != 2 || strings.Contains(stderr.String(), "safe\nsecret") {
			t.Fatalf("code=%d stderr=%q, want exit 2 and no token echo", code, stderr.String())
		}
	})
	t.Run("prints missing and malformed token errors unchanged", func(t *testing.T) {
		root, configPath := fixtureRoot(t, depthZero(), map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		for _, tc := range []struct {
			name, token, want string
		}{
			{
				name: "required",
				want: "DL_CLAIM_TOKEN is required",
			},
			{
				name:  "invalid",
				token: "bad token",
				want: "DL_CLAIM_TOKEN must contain printable ASCII characters " +
					"without whitespace",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var stderr bytes.Buffer
				code := Main(context.Background(), []string{"--config", configPath}, env(map[string]string{
					"GATE_ROOT":      root,
					"DL_CLAIM_TOKEN": tc.token,
				}), io.Discard, &stderr)
				if code != 2 || !strings.Contains(stderr.String(), tc.want) {
					t.Fatalf("code=%d stderr=%q, want %q", code, stderr.String(), tc.want)
				}
			})
		}
	})

	t.Run("returns a failed exit when reconcile output cannot be written", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(ReconcileResponse{
				Updated: 1, Inserted: 2, Stale: []StaleClaim{}, Duplicates: []DuplicateClaim{},
			})
		}))
		t.Cleanup(srv.Close)
		cfg := depthZero()
		cfg.Counter.URL = srv.URL
		root, configPath := fixtureRoot(t, cfg, map[string]string{
			"docs/designs/decisions/ui/DL-001.md": decisionFile("DL-001"),
		})
		code := Main(context.Background(), []string{"--config", configPath}, env(gate(root)), errorWriter{}, io.Discard)
		if code != 1 {
			t.Fatalf("code=%d, want 1 for output failure", code)
		}
	})
}

// roundTrip adapts a function to http.RoundTripper so tests run without a network.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
