// SPDX-License-Identifier: MIT OR Apache-2.0

package claim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/ledger"
)

// Claim client timing. These match the TypeScript claim client.
const (
	claimCheckTimeout      = 10 * time.Second
	claimCheckAttempts     = 3
	claimRateWaitMax       = 65 * time.Second
	claimDefaultRetryAfter = 60 * time.Second
	claimBatchSize         = 100
)

// claimBackoffAfter returns the wait after the given failed attempt (1 or 2).
func claimBackoffAfter(attempt int) time.Duration {
	if attempt == 1 {
		return time.Second
	}
	return 3 * time.Second
}

const (
	claimRateLimitedText = "design-ledger-gate: the DL claim service rate-limited this runner for 65 s." +
		"\nRe-run the pipeline in a minute."
	claimPromptSuffix = "\nRe-run the pipeline when it answers. Do not hand-mint: an unclaimed id fails."
)

var (
	errResponseMustContainClaims = errors.New("response must contain claims")
	errClaimsMustBeArray         = errors.New("claims must be an array")
	errClaimEntryMustBeObject    = errors.New("claim entry must be an object")
	errClaimEntryMissingFields   = errors.New("claim entry must contain id and state")
	errClaimEntryUnknownFields   = errors.New("claim entry has an unknown id or state")
	errDuplicateVerdict          = errors.New("duplicate claim verdict")
	errMissingVerdict            = errors.New("missing claim verdict")
	errClaimTokenRequired        = errors.New("DL_CLAIM_TOKEN is required")
	errClaimTokenInvalid         = errors.New("DL_CLAIM_TOKEN must contain printable ASCII characters without whitespace")
	errClaimNeedsHeadBranch      = errors.New("design-ledger-gate: the claim check needs the PR head branch")
)

// Verdict is the claim service's answer for one decision id on one lane.
type Verdict string

const (
	// VerdictLane means the id is claimed for the lane.
	VerdictLane Verdict = "lane"
	// VerdictNotLane means the id is not claimed for the lane.
	VerdictNotLane Verdict = "not-lane"
)

// ServiceError is a claim-service failure. Kind is "unreachable" or "rate_limited".
type ServiceError struct {
	Kind    string
	Message string
}

func (e *ServiceError) Error() string { return e.Message }

// Fetch returns the claim verdict for each id on one lane.
type Fetch func(ctx context.Context, lane string, ids []string) (map[string]Verdict, error)

// Clock supplies the claim client's waits. Tests inject a fake; they never sleep.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

// unreachableError wraps a claim-service failure with the TypeScript text.
func unreachableError(baseURL, reason string) *ServiceError {
	return &ServiceError{
		Kind: "unreachable",
		Message: fmt.Sprintf(
			"design-ledger-gate: cannot reach the DL claim service at %s (%s).%s",
			baseURL, reason, claimPromptSuffix),
	}
}

func rateLimitedError() *ServiceError {
	return &ServiceError{Kind: "rate_limited", Message: claimRateLimitedText}
}

// claimWait waits d on clock, or returns early when ctx ends.
func claimWait(ctx context.Context, clock Clock, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // The context error is the cancellation cause.
	case <-clock.After(d):
		return nil
	}
}

// NewClaimClient returns a Fetch for the configured counter and partition.
// It sends 100 ids per request. It retries network errors and 5xx responses up
// to three attempts, and it waits on 429 for Retry-After within one shared
// 65-second budget. Any other non-2xx response fails at once.
func NewClaimClient(cfg core.Config, httpClient *http.Client, clock Clock) Fetch {
	baseURL := strings.TrimRight(cfg.Counter.URL, "/")
	return func(ctx context.Context, lane string, ids []string) (map[string]Verdict, error) {
		result := make(map[string]Verdict, len(ids))
		waited := time.Duration(0)
		for start := 0; start < len(ids); start += claimBatchSize {
			batch := &claimBatch{
				baseURL:   baseURL,
				partition: cfg.Counter.Partition,
				lane:      lane,
				ids:       ids[start:min(start+claimBatchSize, len(ids))],
			}
			verdicts, err := fetchClaimBatch(ctx, httpClient, clock, batch, &waited)
			if err != nil {
				return nil, err
			}
			maps.Copy(result, verdicts)
		}
		return result, nil
	}
}

// claimBatch is one request's inputs.
type claimBatch struct {
	baseURL, partition, lane string
	ids                      []string
}

// fetchClaimBatch runs one batch under its attempt budget. waited is the 429
// wait already spent, shared across batches.
func fetchClaimBatch(
	ctx context.Context,
	httpClient *http.Client,
	clock Clock,
	batch *claimBatch,
	waited *time.Duration,
) (map[string]Verdict, error) {
	target, err := claimURL(batch)
	if err != nil {
		return nil, unreachableError(batch.baseURL, err.Error())
	}
	attempts := 0
	for {
		reply, err := doClaimRequest(ctx, httpClient, target)
		if err != nil {
			attempts++
			if attempts >= claimCheckAttempts {
				return nil, unreachableError(batch.baseURL, err.Error())
			}
			waitErr := claimWait(ctx, clock, claimBackoffAfter(attempts))
			if waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		switch {
		case reply.status == http.StatusTooManyRequests:
			err := rateLimitWait(ctx, clock, reply.retryAfter, waited)
			if err != nil {
				return nil, err
			}
		case reply.status >= 500:
			attempts++
			if attempts >= claimCheckAttempts {
				return nil, unreachableError(batch.baseURL, fmt.Sprintf("HTTP %d", reply.status))
			}
			err := claimWait(ctx, clock, claimBackoffAfter(attempts))
			if err != nil {
				return nil, err
			}
		case reply.status < 200 || reply.status > 299:
			return nil, unreachableError(batch.baseURL, fmt.Sprintf("HTTP %d", reply.status))
		default:
			if reply.readErr != nil {
				return nil, unreachableError(batch.baseURL, reply.readErr.Error())
			}
			verdicts, err := decodeClaimResponse(reply.body, batch.ids)
			if err != nil {
				return nil, unreachableError(batch.baseURL, err.Error())
			}
			return verdicts, nil
		}
	}
}

// claimURL builds the GET URL for one batch.
func claimURL(batch *claimBatch) (string, error) {
	target, err := url.Parse(batch.baseURL + "/claims")
	if err != nil {
		return "", fmt.Errorf("invalid claim URL: %w", err)
	}
	query := target.Query()
	query.Set("repo", batch.partition)
	query.Set("lane", batch.lane)
	query.Set("ids", strings.Join(batch.ids, ","))
	target.RawQuery = query.Encode()
	return target.String(), nil
}

// claimReply is one HTTP response. The body is read inside the request timeout.
type claimReply struct {
	status     int
	retryAfter string
	body       []byte
	readErr    error
}

// doClaimRequest sends one GET under the 10-second timeout and reads the body.
// A transport error is returned as err; a body read error is kept in readErr.
func doClaimRequest(ctx context.Context, httpClient *http.Client, target string) (claimReply, error) {
	reqCtx, cancel := context.WithTimeout(ctx, claimCheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return claimReply{}, fmt.Errorf("build claim request: %w", err)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return claimReply{}, fmt.Errorf("claim request: %w", err)
	}
	// The body is fully read below; a close error after that changes nothing.
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(response.Body)
	return claimReply{
		status:     response.StatusCode,
		retryAfter: response.Header.Get("Retry-After"),
		body:       body,
		readErr:    readErr,
	}, nil
}

// rateLimitWait waits for Retry-After (default 60 s), capped by the shared
// 65-second budget. It returns the rate-limited error once the budget is spent.
func rateLimitWait(ctx context.Context, clock Clock, retryAfter string, waited *time.Duration) error {
	requested := claimDefaultRetryAfter
	seconds, err := strconv.ParseFloat(retryAfter, 64)
	if err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
		requested = time.Duration(min(seconds, claimRateWaitMax.Seconds()) * float64(time.Second))
	}
	remaining := claimRateWaitMax - *waited
	if remaining <= 0 {
		return rateLimitedError()
	}
	wait := min(requested, remaining)
	err = claimWait(ctx, clock, wait)
	if err != nil {
		return err
	}
	*waited += wait
	if *waited >= claimRateWaitMax {
		return rateLimitedError()
	}
	return nil
}

// decodeClaimResponse parses the JSON body and checks it against the requested ids.
func decodeClaimResponse(body []byte, ids []string) (map[string]Verdict, error) {
	var value any
	err := json.Unmarshal(body, &value)
	if err != nil {
		return nil, fmt.Errorf("malformed response body: %w", err)
	}
	return parseClaimResponse(value, ids)
}

// parseClaimResponse checks the decoded value. It rejects a missing or non-array
// claims list, unknown ids or states, duplicate verdicts and missing verdicts.
func parseClaimResponse(value any, ids []string) (map[string]Verdict, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, errResponseMustContainClaims
	}
	claimsValue, present := record["claims"]
	if !present {
		return nil, errResponseMustContainClaims
	}
	claims, ok := claimsValue.([]any)
	if !ok {
		return nil, errClaimsMustBeArray
	}
	requested := make(map[string]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	verdicts := make(map[string]Verdict, len(claims))
	for _, claim := range claims {
		entry, ok := claim.(map[string]any)
		if !ok {
			return nil, errClaimEntryMustBeObject
		}
		id, hasID := entry["id"]
		state, hasState := entry["state"]
		if !hasID || !hasState {
			return nil, errClaimEntryMissingFields
		}
		idText, idOK := id.(string)
		stateText, stateOK := state.(string)
		if !idOK || !requested[idText] || !stateOK || !isVerdict(stateText) {
			return nil, errClaimEntryUnknownFields
		}
		if _, dup := verdicts[idText]; dup {
			return nil, fmt.Errorf("%w for %s", errDuplicateVerdict, idText)
		}
		verdicts[idText] = Verdict(stateText)
	}
	for _, id := range ids {
		if _, found := verdicts[id]; !found {
			return nil, fmt.Errorf("%w for %s", errMissingVerdict, id)
		}
	}
	return verdicts, nil
}

func isVerdict(value string) bool {
	return value == string(VerdictLane) || value == string(VerdictNotLane)
}

// CheckIDs selects the changed decision rows whose ids are new: absent
// from both the merge base and the base tip. Each id appears once, sorted by id.
func CheckIDs(
	rows []ledger.DecisionRow,
	changedFiles []string,
	mergeBaseIDs, baseTipIDs map[string]struct{},
) []ledger.DecisionRow {
	changed := make(map[string]bool, len(changedFiles))
	for _, file := range changedFiles {
		changed[file] = true
	}
	seen := make(map[string]bool, len(rows))
	selected := []ledger.DecisionRow{}
	for _, row := range rows {
		if !changed[row.Path] || seen[row.ID] {
			continue
		}
		if _, inMergeBase := mergeBaseIDs[row.ID]; inMergeBase {
			continue
		}
		if _, inBaseTip := baseTipIDs[row.ID]; inBaseTip {
			continue
		}
		seen[row.ID] = true
		selected = append(selected, row)
	}
	slices.SortFunc(selected, func(a, b ledger.DecisionRow) int { return strings.Compare(a.ID, b.ID) })
	return selected
}

// CheckClaims reports each row whose id is not claimed for the lane. The
// message names the dl-claim command to run.
func CheckClaims(rows []ledger.DecisionRow, lane string, verdicts map[string]Verdict) []core.Violation {
	violations := []core.Violation{}
	for _, row := range rows {
		if verdicts[row.ID] == VerdictLane {
			continue
		}
		violations = append(violations, core.Violation{
			File: row.Path,
			Line: row.Line,
			Message: fmt.Sprintf(
				"%s: not claimed for lane %s. If you claimed it under another lane, run\n"+
					"`dl-claim --rebind --from <old-lane> --lane %s --ids %s`. Otherwise run `dl-claim --lane %s`.",
				row.ID, lane, lane, row.ID, lane),
		})
	}
	return violations
}

// ReadDLClaimToken returns the claim token. DL_CLAIM_TOKEN_FILE wins when it is
// readable and non-empty after trimming; otherwise DL_CLAIM_TOKEN is returned.
func ReadDLClaimToken(env func(string) string) (string, error) {
	if rawPath := env("DL_CLAIM_TOKEN_FILE"); jstext.Trim(rawPath) != "" {
		data, err := os.ReadFile(rawPath) //nolint:gosec // The token path is set by the operator.
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read DL_CLAIM_TOKEN_FILE: %w", err)
		}
		if err == nil {
			if token := jstext.Trim(string(data)); token != "" {
				return token, nil
			}
		}
	}
	return env("DL_CLAIM_TOKEN"), nil
}

// ValidateDLClaimToken trims the token and checks that it is printable ASCII
// with no whitespace. It returns the trimmed token.
func ValidateDLClaimToken(token string) (string, error) {
	trimmed := jstext.Trim(token)
	if trimmed == "" {
		return "", errClaimTokenRequired
	}
	for _, r := range trimmed {
		if r < 0x21 || r > 0x7e {
			return "", errClaimTokenInvalid
		}
	}
	return trimmed, nil
}
