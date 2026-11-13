// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct_v5,errchkjson,forcetypeassert // Keep TS-derived cases direct and readable.
package claim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/ledger"
)

// fakeClock records every requested wait and never sleeps.
type fakeClock struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

func (c *fakeClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

// claimConfig is the test config with a counter URL the test server answers.
func claimConfig(serverURL string) core.Config {
	return core.Config{
		DesignsRoot:   "docs/designs",
		SurfaceDepth:  0,
		GovernedRoots: []string{"ui"},
		Counter:       core.Counter{URL: serverURL + "/base/", Partition: "public-docs"},
	}
}

func claimDecision(id string) ledger.DecisionRow {
	return ledger.DecisionRow{Path: "docs/designs/decisions/ui/" + id + ".md", Line: 2, ID: id}
}

// lanePayload answers every requested id with state "lane".
func lanePayload(ids string) string {
	claims := make([]map[string]string, 0, strings.Count(ids, ",")+1)
	for id := range strings.SplitSeq(ids, ",") {
		claims = append(claims, map[string]string{"id": id, "state": "lane"})
	}
	data, _ := json.Marshal(map[string]any{"claims": claims})
	return string(data)
}

func TestClaimCheckIDsSelectsOnlyNewChangedIDs(t *testing.T) {
	rows := []ledger.DecisionRow{
		claimDecision("DL-004"),
		claimDecision("DL-002"),
		claimDecision("DL-003"),
	}
	changed := []string{rows[0].Path, rows[1].Path, rows[2].Path}
	checked := CheckIDs(rows, changed,
		map[string]struct{}{"DL-002": {}},
		map[string]struct{}{"DL-003": {}})
	if len(checked) != 1 || checked[0].ID != "DL-004" {
		t.Fatalf("CheckIDs = %v, want [DL-004]", checked)
	}
	if got := CheckIDs(rows, nil, nil, nil); len(got) != 0 {
		t.Fatalf("no changed files: got %v, want none", got)
	}
}

func TestCheckClaimsNotLaneAndMissingEachFailWithLaneGuidance(t *testing.T) {
	rows := []ledger.DecisionRow{claimDecision("DL-004"), claimDecision("DL-005")}
	violations := CheckClaims(rows, "feature/head", map[string]Verdict{"DL-004": VerdictNotLane})
	if len(violations) != 2 {
		t.Fatalf("got %d violations, want 2: %v", len(violations), violations)
	}
	joined := violations[0].Message + "\n" + violations[1].Message
	if !strings.Contains(joined, "dl-claim --lane feature/head") {
		t.Fatalf("missing dl-claim lane guidance in %q", joined)
	}
}

func TestClaimClientBatchesOneHundredIDsPerRequest(t *testing.T) {
	var mu sync.Mutex
	var requests []*url.URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL)
		mu.Unlock()
		_, _ = w.Write([]byte(lanePayload(r.URL.Query().Get("ids"))))
	}))
	defer server.Close()
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = fmt.Sprintf("DL-%03d", i+1)
	}
	fetch := NewClaimClient(claimConfig(server.URL), server.Client(), &fakeClock{})
	verdicts, err := fetch(context.Background(), "feature/head", ids)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	counts := make([]int, 0, len(requests))
	for _, request := range requests {
		counts = append(counts, len(strings.Split(request.Query().Get("ids"), ",")))
	}
	if !slices.Equal(counts, []int{100, 50}) {
		t.Fatalf("batch sizes = %v, want [100 50]", counts)
	}
	if requests[0].Path != "/base/claims" {
		t.Fatalf("path = %q, want /base/claims", requests[0].Path)
	}
	if got := requests[0].Query().Get("repo"); got != "public-docs" {
		t.Fatalf("repo = %q, want public-docs", got)
	}
	if got := requests[0].Query().Get("lane"); got != "feature/head" {
		t.Fatalf("lane = %q, want feature/head", got)
	}
	if len(verdicts) != 150 {
		t.Fatalf("got %d verdicts, want 150", len(verdicts))
	}
}

func TestClaimClientRetriesNetworkAndServerErrorsWithBackoff(t *testing.T) {
	for _, failure := range []string{"network error", "server error"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls < 3 {
					if failure == "network error" {
						hijackAndClose(t, w)
						return
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte("busy"))
					return
				}
				_, _ = w.Write([]byte(`{"claims":[{"id":"DL-004","state":"lane"}]}`))
			}))
			defer server.Close()
			clock := &fakeClock{}
			fetch := NewClaimClient(claimConfig(server.URL), server.Client(), clock)
			verdicts, err := fetch(context.Background(), "feature/head", []string{"DL-004"})
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if verdicts["DL-004"] != VerdictLane {
				t.Fatalf("verdict = %v, want lane", verdicts)
			}
			if calls != 3 {
				t.Fatalf("calls = %d, want 3", calls)
			}
			if got := clock.recorded(); !reflect.DeepEqual(got, []time.Duration{time.Second, 3 * time.Second}) {
				t.Fatalf("waits = %v, want [1s 3s]", got)
			}
		})
	}
}

// hijackAndClose drops the connection without a response, which the client sees as a network error.
func hijackAndClose(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Fatalf("hijack: %v", err)
	}
	_ = conn.Close()
}

func TestClaimClient429RetriesAfterRetryAfterAndStopsAt65Seconds(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		retryAfter := "10"
		if calls == 1 {
			retryAfter = "60"
		}
		w.Header().Set("Retry-After", retryAfter)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	clock := &fakeClock{}
	fetch := NewClaimClient(claimConfig(server.URL), server.Client(), clock)
	_, err := fetch(context.Background(), "feature/head", []string{"DL-004"})
	assertRateLimited(t, err)
	if got := clock.recorded(); !reflect.DeepEqual(got, []time.Duration{60 * time.Second, 5 * time.Second}) {
		t.Fatalf("waits = %v, want [60s 5s]", got)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestClaimClientSharesOne429BudgetAcrossBatches(t *testing.T) {
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = fmt.Sprintf("DL-%d", i+1)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			_, _ = w.Write([]byte(lanePayload(r.URL.Query().Get("ids"))))
			return
		}
		w.Header().Set("Retry-After", "40")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	clock := &fakeClock{}
	fetch := NewClaimClient(claimConfig(server.URL), server.Client(), clock)
	_, err := fetch(context.Background(), "feature/head", ids)
	assertRateLimited(t, err)
	if got := clock.recorded(); !reflect.DeepEqual(got, []time.Duration{40 * time.Second, 25 * time.Second}) {
		t.Fatalf("waits = %v, want [40s 25s]", got)
	}
}

func TestClaimClientInfiniteRetryAfterUsesDefault(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1e400")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	clock := &fakeClock{}
	fetch := NewClaimClient(claimConfig(server.URL), server.Client(), clock)
	_, err := fetch(context.Background(), "feature/head", []string{"DL-004"})
	assertRateLimited(t, err)
	// A non-finite Retry-After falls back to the 60 s default, as in the TS client.
	if got := clock.recorded(); !reflect.DeepEqual(got, []time.Duration{60 * time.Second, 5 * time.Second}) {
		t.Fatalf("waits = %v, want [1m0s 5s]", got)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func assertRateLimited(t *testing.T, err error) {
	t.Helper()
	serviceErr, ok := errors.AsType[*ServiceError](err)
	if !ok || serviceErr.Kind != "rate_limited" {
		t.Fatalf("err = %v, want rate_limited ServiceError", err)
	}
}

func TestClaimClientBadRequestFailsOnceWithoutSleeping(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	clock := &fakeClock{}
	fetch := NewClaimClient(claimConfig(server.URL), server.Client(), clock)
	_, err := fetch(context.Background(), "feature/head", []string{"DL-004"})
	if _, ok := errors.AsType[*ServiceError](err); !ok {
		t.Fatalf("err = %v, want ServiceError", err)
	}
	if calls != 1 || len(clock.recorded()) != 0 {
		t.Fatalf("calls = %d, waits = %v; want 1 call and no wait", calls, clock.recorded())
	}
}

func TestClaimClientRejectsMalformedResponsesWithoutRetry(t *testing.T) {
	cases := map[string]string{
		"malformed body": "not-json",
		"missing id":     `{"claims":[]}`,
		"unknown state":  `{"claims":[{"id":"DL-004","state":"unknown"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			fetch := NewClaimClient(claimConfig(server.URL), server.Client(), &fakeClock{})
			_, err := fetch(context.Background(), "feature/head", []string{"DL-004"})
			if _, ok := errors.AsType[*ServiceError](err); !ok {
				t.Fatalf("err = %v, want ServiceError", err)
			}
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
		})
	}
}

func TestClaimClientAttachesRequestTimeout(t *testing.T) {
	var deadlineSet bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"claims":[{"id":"DL-004","state":"lane"}]}`))
	}))
	defer server.Close()
	httpClient := &http.Client{Transport: deadlineRecorder{base: server.Client().Transport, set: &deadlineSet}}
	fetch := NewClaimClient(claimConfig(server.URL), httpClient, &fakeClock{})
	_, err := fetch(context.Background(), "feature/head", []string{"DL-004"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !deadlineSet {
		t.Fatal("outbound claim request has no deadline; the 10-second timeout is missing")
	}
}

// deadlineRecorder records whether the outbound request carries a deadline, which is the client side of the timeout.
type deadlineRecorder struct {
	base http.RoundTripper
	set  *bool
}

func (d deadlineRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, ok := req.Context().Deadline(); ok {
		*d.set = true
	}
	base := d.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip claim request: %w", err)
	}
	return response, nil
}

func TestClaimTimeoutConstantsMatchTypeScript(t *testing.T) {
	if claimCheckTimeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", claimCheckTimeout)
	}
	if claimCheckAttempts != 3 {
		t.Fatalf("attempts = %d, want 3", claimCheckAttempts)
	}
	if claimRateWaitMax != 65*time.Second {
		t.Fatalf("rate wait max = %v, want 65s", claimRateWaitMax)
	}
}

func TestReadDLClaimTokenPrefersReadableFileThenEnv(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	token, err := ReadDLClaimToken(env(map[string]string{"DL_CLAIM_TOKEN_FILE": tokenPath, "DL_CLAIM_TOKEN": "env"}))
	if err != nil || token != "file-token" {
		t.Fatalf("file token = %q, %v; want file-token", token, err)
	}
	token, err = ReadDLClaimToken(
		env(map[string]string{"DL_CLAIM_TOKEN_FILE": filepath.Join(dir, "missing"), "DL_CLAIM_TOKEN": "env-token"}),
	)
	if err != nil || token != "env-token" {
		t.Fatalf("fallback token = %q, %v; want env-token", token, err)
	}
}

func TestReadDLClaimTokenFailsOnUnreadableTokenFile(t *testing.T) {
	dir := t.TempDir()
	env := func(key string) string {
		return map[string]string{"DL_CLAIM_TOKEN_FILE": dir, "DL_CLAIM_TOKEN": "env-token"}[key]
	}
	_, err := ReadDLClaimToken(env)
	if err == nil || !strings.Contains(err.Error(), "read DL_CLAIM_TOKEN_FILE") {
		t.Fatalf("directory token file err = %v, want read DL_CLAIM_TOKEN_FILE error", err)
	}
}

func TestValidateDLClaimTokenRules(t *testing.T) {
	_, err := ValidateDLClaimToken("   ")
	if err == nil || err.Error() != "DL_CLAIM_TOKEN is required" {
		t.Fatalf("empty token err = %v, want DL_CLAIM_TOKEN is required", err)
	}
	_, err = ValidateDLClaimToken("has space")
	if err == nil || err.Error() != "DL_CLAIM_TOKEN must contain printable ASCII characters without whitespace" {
		t.Fatalf("whitespace token err = %v", err)
	}
	token, err := ValidateDLClaimToken("  ok-token  ")
	if err != nil || token != "ok-token" {
		t.Fatalf("token = %q, %v; want ok-token", token, err)
	}
}

func TestClaimErrorTextKeepsTypeScriptWording(t *testing.T) {
	err := unreachableError("https://counter.example.test", "offline")
	want := "design-ledger-gate: cannot reach the DL claim service at https://counter.example.test (offline).\n" +
		"Re-run the pipeline when it answers. Do not hand-mint: an unclaimed id fails."
	if err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
	if strconv.Itoa(claimCheckAttempts) != "3" {
		t.Fatal("attempt count changed")
	}
}
