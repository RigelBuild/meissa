// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5 // Keep TS-derived cases direct and readable.
package dlclaim

import (
	"bytes"
	"context"
	"encoding/json"
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

const (
	testToken     = "token"
	testCounter   = "https://counter.example.test"
	testPartition = "public-docs"
)

// oneSurface is the single-surface config the TS tests call oneSurface.
func oneSurface() core.Config {
	return core.Config{
		Surfaces: []string{"alpha"},
		Counter:  core.Counter{URL: testCounter, Partition: testPartition},
	}
}

// severalSurfaces is the two-surface config the TS tests call severalSurfaces.
func severalSurfaces() core.Config {
	cfg := oneSurface()
	cfg.Surfaces = []string{"alpha", "beta"}
	return cfg
}

// claimBody is the ClaimRequest the claim-service tests post.
func claimBody() ClaimRequest {
	return ClaimRequest{Repo: testPartition, Surface: "alpha", Ref: "RIG-42", Lane: "feature/a", Count: 2}
}

// roundTrip adapts a function to http.RoundTripper so the tests never touch the network.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeClient returns a client whose transport runs handler.
func fakeClient(handler func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTrip(handler)}
}

// jsonResponse builds a response with a JSON body and status.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// env is a lookup over a fixed map, the way Main reads the environment.
func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// writeConfig writes a claim config pointing at url and returns its path.
func writeConfig(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := fmt.Sprintf(
		`{"designsRoot":"docs/designs","surfaceDepth":0,"governedRoots":["ui"],`+
			`"surfaces":["alpha"],"counter":{"url":%q,"partition":"public-docs"}}`,
		url,
	)
	err := os.WriteFile(path, []byte(cfg), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestClaimArgumentsAndRequest covers the describe "claim arguments and request".
func TestClaimArgumentsAndRequest(t *testing.T) {
	t.Run("defaults to the only configured surface", func(t *testing.T) {
		got, err := ParseArgs([]string{"--ref=none", "--lane=feature/b"}, oneSurface())
		if err != nil {
			t.Fatal(err)
		}
		claim, ok := got.(ClaimArgs)
		if !ok || claim.Ref != "none" || claim.Lane != "feature/b" || claim.Count != 1 || claim.Surface.OrEmpty() != "alpha" {
			t.Fatalf("ParseArgs = %#v", got)
		}
	})

	t.Run("requires a known explicit surface for a multi-surface config", func(t *testing.T) {
		_, err := ParseArgs([]string{"--ref", "none", "--lane", "x"}, severalSurfaces())
		if err == nil || !strings.Contains(err.Error(), "--surface is required") {
			t.Fatalf("err = %v", err)
		}
		_, err = ParseArgs([]string{"--ref", "none", "--lane", "x", "--surface", "gamma"}, severalSurfaces())
		if err == nil || !strings.Contains(err.Error(), "Unknown surface: gamma") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("requires an explicit surface when building a multi-surface body", func(t *testing.T) {
		// BuildClaimBody keeps the frozen signature and returns no error. The
		// surface check lives in ParseArgs, so a multi-surface claim without a
		// surface never reaches BuildClaimBody.
		_, err := ParseArgs([]string{"--ref", "RIG-42", "--lane", "x"}, severalSurfaces())
		if err == nil || !strings.Contains(err.Error(), "--surface is required") {
			t.Fatalf("err = %v, want the surface requirement", err)
		}
	})

	t.Run("validates refs, lane, counts, and unknown flags", func(t *testing.T) {
		argvs := [][]string{
			{"--lane", "x"},
			{"--ref", "rig-12", "--lane", "x"},
			{"--ref", "RIG-1", "--lane", " "},
			{"--ref", "RIG-1", "--lane", "x", "--count", "0"},
			{"--ref", "RIG-1", "--lane", "x", "--count", "11"},
			{"--ref", "RIG-1", "--lane", "x", "--count", "1.5"},
			{"--ref", "RIG-1", "--lane", "x", "--bogus"},
			{"--ref", "RIG-1", "--lane", "--count"},
		}
		for _, argv := range argvs {
			_, err := ParseArgs(argv, oneSurface())
			if err == nil {
				t.Fatalf("ParseArgs(%v) accepted, want an error", argv)
			}
		}
	})

	t.Run("accepts separated and equals values with an explicit surface", func(t *testing.T) {
		got, err := ParseArgs(
			[]string{"--ref", "RIG-42", "--lane", "feature/a", "--count", "2", "--surface=beta"},
			severalSurfaces(),
		)
		if err != nil {
			t.Fatal(err)
		}
		claim, ok := got.(ClaimArgs)
		if !ok {
			t.Fatalf("ParseArgs returned %T, want ClaimArgs", got)
		}
		if claim.Ref != "RIG-42" || claim.Lane != "feature/a" || claim.Count != 2 || claim.Surface.OrEmpty() != "beta" {
			t.Fatalf("ParseArgs = %#v", claim)
		}
	})
}

// TestClaimService covers the describe "claim service".
func TestClaimService(t *testing.T) {
	t.Run("posts the configured URL and partition with a trimmed bearer token", func(t *testing.T) {
		var gotURL, gotAuth string
		var gotBody ClaimRequest
		client := fakeClient(func(r *http.Request) (*http.Response, error) {
			gotURL, gotAuth = r.URL.String(), r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			return jsonResponse(200, `{"ids":[{"id":"DL-001","date":"2026-11-11"},{"id":"DL-002","date":"2026-11-11"}]}`), nil
		})
		_, err := Claim(context.Background(), client, claimBody(), " token ", oneSurface())
		if err != nil {
			t.Fatal(err)
		}
		if gotURL != testCounter+"/claim" || gotAuth != "Bearer token" {
			t.Fatalf("url=%q auth=%q", gotURL, gotAuth)
		}
		if gotBody.Repo != testPartition {
			t.Fatalf("posted repo = %q, want %q", gotBody.Repo, testPartition)
		}
	})

	t.Run("rejects blank credentials before posting", func(t *testing.T) {
		var called atomic.Bool
		client := fakeClient(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return jsonResponse(200, `{}`), nil
		})
		_, err := Claim(context.Background(), client, claimBody(), " ", oneSurface())
		if err == nil || err.Error() != "DL_CLAIM_TOKEN is required" {
			t.Fatalf("err = %v", err)
		}
		if called.Load() {
			t.Fatal("request sent with a blank token")
		}
	})

	t.Run("includes the configured service URL in an uncertain failure", func(t *testing.T) {
		client := fakeClient(func(*http.Request) (*http.Response, error) {
			return jsonResponse(502, "failure"), nil
		})
		_, err := Claim(context.Background(), client, claimBody(), testToken, oneSurface())
		if err == nil || !strings.Contains(err.Error(), testCounter+"/status?repo="+testPartition) {
			t.Fatalf("err = %v, want the status URL", err)
		}
	})

	t.Run("keeps status hints when service error bodies are unreadable", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			status int
			body   string
			want   string
		}{
			{
				name:   "empty unauthorized body",
				status: http.StatusUnauthorized,
				body:   "",
				want:   "Token missing or wrong.",
			},
			{
				name:   "HTML unauthorized body",
				status: http.StatusUnauthorized,
				body:   "<html>proxy error</html>",
				want:   "Token missing or wrong.",
			},
			{
				name:   "empty server error",
				status: http.StatusInternalServerError,
				body:   "",
				want:   "do not rerun",
			},
			{
				name:   "HTML server error",
				status: http.StatusInternalServerError,
				body:   "<html>proxy error</html>",
				want:   "do not rerun",
			},
			{
				name:   "empty gateway error",
				status: http.StatusBadGateway,
				body:   "",
				want:   "do not rerun",
			},
			{
				name:   "HTML gateway error",
				status: http.StatusBadGateway,
				body:   "<html>proxy error</html>",
				want:   "do not rerun",
			},
			{
				name:   "non-string error code",
				status: http.StatusTooManyRequests,
				body:   `{"error":1}`,
				want:   "Rate limited; retry later.",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				client := fakeClient(func(*http.Request) (*http.Response, error) {
					return jsonResponse(tc.status, tc.body), nil
				})
				_, err := Claim(context.Background(), client, claimBody(), testToken, oneSurface())
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", tc.status)) ||
					!strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Claim error = %v; want HTTP %d and %q", err, tc.status, tc.want)
				}
			})
		}
	})
	t.Run("classifies timeout errors without sleeping", func(t *testing.T) {
		if got := errorName(fmt.Errorf("request: %w", context.DeadlineExceeded)); got != "TimeoutError" {
			t.Fatalf("wrapped deadline name = %q, want TimeoutError", got)
		}
		if got := errorName(io.EOF); got != "Error" {
			t.Fatalf("plain error name = %q, want Error", got)
		}
		if client := newClient(); client.Timeout != requestTimeout {
			t.Fatalf("client timeout = %v, want %v", client.Timeout, requestTimeout)
		}
	})

	t.Run("rejects response shape mismatches", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body string
		}{
			{name: "claim array missing", body: `{}`},
			{name: "claim array wrong type", body: `{"ids":{}}`},
			{
				name: "claim entry missing id",
				body: `{"ids":[{"date":"2026-11-11"},` +
					`{"id":"DL-002","date":"2026-11-11"}]}`,
			},
			{
				name: "claim entry id wrong type",
				body: `{"ids":[{"id":1,"date":"2026-11-11"},` +
					`{"id":"DL-002","date":"2026-11-11"}]}`,
			},
			{name: "claim response non-object", body: `[]`},
			{name: "claim response multiple values", body: `{"ids":[]} {}`},
			{name: "maintenance array missing", body: `{}`},
			{name: "maintenance array wrong type", body: `{"ids":1}`},
			{name: "maintenance element wrong type", body: `{"ids":[1]}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				client := fakeClient(func(*http.Request) (*http.Response, error) {
					return jsonResponse(200, tc.body), nil
				})
				if strings.HasPrefix(tc.name, "claim") {
					_, err := Claim(context.Background(), client, claimBody(), testToken, oneSurface())
					if err == nil {
						t.Fatal("Claim accepted a response shape mismatch")
					}
					return
				}
				result, err := Release(
					context.Background(), client,
					ReleaseArgs{Lane: "lane", IDs: []string{"DL-001"}},
					testToken, oneSurface(),
				)
				if err == nil || result != nil {
					t.Fatalf("Release = %v, %v; want shape error", result, err)
				}
			})
		}
	})

	t.Run("includes valid returned ids in malformed claim diagnostics", func(t *testing.T) {
		client := fakeClient(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"ids":[{"id":1,"date":"2026-11-11"},{"id":"DL-002","date":"2026-11-11"}]}`), nil
		})
		_, err := Claim(context.Background(), client, claimBody(), testToken, oneSurface())
		if err == nil || !strings.Contains(err.Error(), "claim response contains a malformed id") ||
			!strings.Contains(err.Error(), "Returned: DL-002.") {
			t.Fatalf("Claim error = %v; want malformed id and returned DL-002", err)
		}
	})

	t.Run("preserves 409 refusals when conflict bodies are invalid", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body string
			want string
		}{
			{name: "empty", body: "", want: "some ids"},
			{name: "HTML", body: "<html>conflict</html>", want: "some ids"},
			{name: "non-string only", body: `{"ids":[1]}`, want: "some ids"},
			{name: "skip non-string and keep valid id", body: `{"ids":[1,"DL-005"]}`, want: "DL-005"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				client := fakeClient(func(*http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusConflict, tc.body), nil
				})
				_, err := Rebind(
					context.Background(),
					client,
					RebindArgs{From: "from", Lane: "to", IDs: []string{"DL-001"}},
					testToken,
					oneSurface(),
				)
				want := "rebind refused, nothing changed: " + tc.want + " not claimed for lane from."
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("Rebind error = %v; want refusal naming %q", err, tc.want)
				}
			})
		}
	})

	t.Run("rejects malformed and wrong-count responses", func(t *testing.T) {
		bodies := []string{
			`{"ids":[]}`,
			`{"ids":[{"id":"DL-001","date":"2026-11-11"}]}`,
			`{"ids":[{"id":1,"date":"2026-11-11"},{"id":"DL-002","date":"2026-11-11"}]}`,
			`{"ids":[{"id":"DL-001","date":"bad"},{"id":"DL-002","date":"2026-11-11"}]}`,
		}
		for _, body := range bodies {
			client := fakeClient(func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, body), nil
			})
			_, err := Claim(context.Background(), client, claimBody(), testToken, oneSurface())
			if err == nil || !strings.Contains(err.Error(), "claim response") {
				t.Fatalf("body %s: err = %v", body, err)
			}
		}
	})

	t.Run("warns about possible consumption only after the counter may advance", func(t *testing.T) {
		refused := fakeClient(func(*http.Request) (*http.Response, error) {
			return jsonResponse(400, `{}`), nil
		})
		_, err := Claim(context.Background(), refused, claimBody(), testToken, oneSurface())
		if err == nil || strings.Contains(err.Error(), "do not rerun") {
			t.Fatalf("400: err = %v, want no do-not-rerun warning", err)
		}
		uncertain := fakeClient(func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{}`), nil
		})
		_, err = Claim(context.Background(), uncertain, claimBody(), testToken, oneSurface())
		if err == nil || !strings.Contains(err.Error(), "do not rerun") {
			t.Fatalf("500: err = %v, want the do-not-rerun warning", err)
		}
	})
}

// TestFormatClaimed covers the top-level test "formats claimed IDs".
func TestFormatClaimed(t *testing.T) {
	got := FormatClaimed([]ClaimedID{{ID: "DL-377", Date: "2026-09-29"}})
	if got != "DL-377 (claimed 2026-09-29)" {
		t.Fatalf("FormatClaimed = %q", got)
	}
}

// TestClaimTokenFile covers the top-level test "uses a non-empty token file before the environment value".
func TestClaimTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	err := os.WriteFile(path, []byte("from-file\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	got, err := claim.ReadDLClaimToken(env(map[string]string{
		"DL_CLAIM_TOKEN_FILE": path,
		"DL_CLAIM_TOKEN":      "from-env",
	}))
	if err != nil || got != "from-file" {
		t.Fatalf("token = %q, err = %v, want from-file", got, err)
	}
}

// TestClaimInteriorNewline checks rejection without logging the token.
func TestClaimInteriorNewline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{"--config", writeConfig(t, srv.URL), "--ref", "none", "--lane", "x"},
		env(map[string]string{"DL_CLAIM_TOKEN": "safe\nsecret"}), &stdout, &stderr)
	if code != 2 || strings.Contains(stderr.String(), "safe\nsecret") {
		t.Fatalf("code=%d stderr=%q, want exit 2 and no token echo", code, stderr.String())
	}
}

func TestReadMaintenanceIDsErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "invalid JSON",
			body: `{"ids":[`,
			want: "claim maintenance response is not valid JSON",
		},
		{
			name: "malformed shape",
			body: `{"ids":1}`,
			want: "claim maintenance response is malformed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readMaintenanceIDs(strings.NewReader(tt.body))
			if err == nil || err.Error() != tt.want {
				t.Fatalf("readMaintenanceIDs error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestClaimMaintenance covers the describe "claim maintenance".
func TestClaimMaintenance(t *testing.T) {
	t.Run("parses rebind and release arguments", func(t *testing.T) {
		cases := []struct {
			name string
			argv []string
			want any
		}{
			{
				name: "--rebind --from old/head --lane new/head --ids DL-004,DL-005",
				argv: []string{"--rebind", "--from", "old/head", "--lane", "new/head", "--ids", "DL-004,DL-005"},
				want: RebindArgs{From: "old/head", Lane: "new/head", IDs: []string{"DL-004", "DL-005"}},
			},
			{
				name: "--release --lane=new/head --ids=DL-004,DL-005",
				argv: []string{"--release", "--lane=new/head", "--ids=DL-004,DL-005"},
				want: ReleaseArgs{Lane: "new/head", IDs: []string{"DL-004", "DL-005"}},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := ParseArgs(tc.argv, oneSurface())
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("ParseArgs = %#v, want %#v", got, tc.want)
				}
			})
		}
	})

	t.Run("rejects invalid maintenance arguments", func(t *testing.T) {
		cases := []struct {
			name string
			argv []string
		}{
			{"mixed modes", []string{"--rebind", "--release", "--from", "old", "--lane", "new", "--ids", "DL-004"}},
			{"mixed claim flags", []string{"--release", "--lane", "new", "--ids", "DL-004", "--ref", "none"}},
			{"missing old lane", []string{"--rebind", "--lane", "new", "--ids", "DL-004"}},
			{"invalid id", []string{"--release", "--lane", "new", "--ids", "nope"}},
			{"duplicate id", []string{"--release", "--lane", "new", "--ids", "DL-004,DL-4"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := ParseArgs(tc.argv, oneSurface())
				if err == nil {
					t.Fatalf("ParseArgs(%v) accepted, want an error", tc.argv)
				}
			})
		}
	})

	t.Run("posts rebind and release to the configured partition and reports ids", func(t *testing.T) {
		cases := []struct {
			mode string
			args any
			body map[string]any
		}{
			{
				mode: "rebind",
				args: RebindArgs{From: "old/head", Lane: "new/head", IDs: []string{"DL-004", "DL-005"}},
				body: map[string]any{"repo": "public-docs", "ids": []any{"DL-004", "DL-005"}, "from": "old/head", "to": "new/head"},
			},
			{
				mode: "release",
				args: ReleaseArgs{Lane: "new/head", IDs: []string{"DL-004", "DL-005"}},
				body: map[string]any{"repo": "public-docs", "ids": []any{"DL-004", "DL-005"}, "lane": "new/head"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.mode, func(t *testing.T) {
				var gotURL, gotMethod, gotAuth string
				var gotBody map[string]any
				client := fakeClient(func(r *http.Request) (*http.Response, error) {
					gotURL, gotMethod, gotAuth = r.URL.String(), r.Method, r.Header.Get("Authorization")
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
					return jsonResponse(200, `{"ids":["DL-004","DL-005"]}`), nil
				})
				var ids []string
				var err error
				switch a := tc.args.(type) {
				case RebindArgs:
					ids, err = Rebind(context.Background(), client, a, " token ", oneSurface())
				case ReleaseArgs:
					ids, err = Release(context.Background(), client, a, " token ", oneSurface())
				}
				if err != nil {
					t.Fatal(err)
				}
				if gotURL != testCounter+"/claims/"+tc.mode || gotMethod != http.MethodPost || gotAuth != "Bearer token" {
					t.Fatalf("url=%q method=%q auth=%q", gotURL, gotMethod, gotAuth)
				}
				if !reflect.DeepEqual(gotBody, tc.body) {
					t.Fatalf("body = %v, want %v", gotBody, tc.body)
				}
				if !reflect.DeepEqual(ids, []string{"DL-004", "DL-005"}) {
					t.Fatalf("ids = %v", ids)
				}
			})
		}
	})

	t.Run("a lane mismatch refuses atomically and names the affected IDs", func(t *testing.T) {
		client := fakeClient(func(*http.Request) (*http.Response, error) {
			return jsonResponse(409, `{"error":"lane_mismatch","ids":["DL-005"]}`), nil
		})
		_, err := Rebind(
			context.Background(), client,
			RebindArgs{From: "old/head", Lane: "new/head", IDs: []string{"DL-004", "DL-005"}},
			testToken, oneSurface(),
		)
		want := "rebind refused, nothing changed: DL-005 not claimed for lane old/head."
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("maintenance needs a token and CLI runs against configured endpoints", func(t *testing.T) {
		var called atomic.Bool
		client := fakeClient(func(*http.Request) (*http.Response, error) {
			called.Store(true)
			return jsonResponse(200, `{"ids":["DL-004"]}`), nil
		})
		_, err := Release(
			context.Background(), client,
			ReleaseArgs{Lane: "old/head", IDs: []string{"DL-004"}},
			" ", oneSurface(),
		)
		if err == nil || err.Error() != "DL_CLAIM_TOKEN is required" || called.Load() {
			t.Fatalf("blank token: err=%v called=%v", err, called.Load())
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ids":["DL-004"]}`))
		}))
		t.Cleanup(srv.Close)
		var stdout, stderr bytes.Buffer
		code := Main(
			context.Background(),
			[]string{"--config", writeConfig(t, srv.URL), "--release", "--lane", "old/head", "--ids", "DL-004"},
			env(map[string]string{"DL_CLAIM_TOKEN": "token"}), &stdout, &stderr,
		)
		if code != 0 || strings.TrimSpace(stdout.String()) != "DL-004" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}
