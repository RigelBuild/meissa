// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dlreconcile ports the dl-reconcile CLI. It reports the decision IDs in
// the design ledger to the claim counter and prints what the counter changed.
//
//nolint:err113,wrapcheck // Preserve TypeScript error text and add no redundant wrapper.
package dlreconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/ledger"
)

// requestTimeout matches the TypeScript CLI's 30-second default.
const requestTimeout = 30 * time.Second

// LandedDecision is one decision the ledger holds, sent to the counter.
type LandedDecision struct {
	ID      string `json:"id"`
	Surface string `json:"surface"`
	Ref     string `json:"ref"`
}

// ReconcileRequest is the body of a POST to the counter's /reconcile endpoint.
type ReconcileRequest struct {
	Repo   string           `json:"repo"`
	Landed []LandedDecision `json:"landed"`
}

// StaleClaim is a claim the counter still holds but the ledger no longer backs.
type StaleClaim struct {
	ID      string `json:"id"`
	Surface string `json:"surface"`
	Ref     string `json:"ref"`
	Lane    string `json:"lane"`
	Date    string `json:"date"`
}

// DuplicateClaim is an ID the counter holds on more than one surface.
type DuplicateClaim struct {
	ID       string   `json:"id"`
	Surfaces []string `json:"surfaces"`
}

// ReconcileResponse is the counter's answer to a reconcile request.
type ReconcileResponse struct {
	Updated    int              `json:"updated"`
	Inserted   int              `json:"inserted"`
	Stale      []StaleClaim     `json:"stale"`
	Duplicates []DuplicateClaim `json:"duplicates"`
}

// AssertReconcilableLedgers checks the configured surfaces and the decision files,
// then builds the request. It refuses an empty frontier and any malformed file.
// files maps each repository path to its text.
func AssertReconcilableLedgers(cfg core.Config, files map[string]string) (ReconcileRequest, error) {
	surfaces := cfg.Surfaces
	if len(surfaces) == 0 {
		return ReconcileRequest{}, errors.New("config.surfaces must contain at least one surface")
	}
	if cfg.SurfaceDepth == 0 && len(surfaces) != 1 {
		return ReconcileRequest{}, errors.New("config.surfaces must contain exactly one surface at depth 0")
	}
	decisions := decisionPaths(files, cfg)
	if len(decisions) == 0 {
		return ReconcileRequest{}, errors.New("decision file list is empty; refusing to post an empty frontier")
	}
	landed := make([]landedEntry, 0, len(decisions))
	for _, file := range decisions {
		entry, err := landedFor(file, files[file], cfg)
		if err != nil {
			return ReconcileRequest{}, err
		}
		landed = append(landed, entry)
	}
	sortLanded(landed)
	out := make([]LandedDecision, 0, len(landed))
	for _, entry := range landed {
		out = append(out, entry.decision)
	}
	return ReconcileRequest{Repo: cfg.Counter.Partition, Landed: out}, nil
}

// landedEntry pairs a landed decision with its path, for stable sorting.
type landedEntry struct {
	path     string
	decision LandedDecision
}

// decisionPaths returns the sorted paths that classify as decision files.
func decisionPaths(files map[string]string, cfg core.Config) []string {
	var decisions []string
	for file := range files {
		if core.ClassifyDesignPath(file, cfg) == core.PathDecision {
			decisions = append(decisions, file)
		}
	}
	sort.Strings(decisions)
	return decisions
}

// landedFor parses one decision file and names its surface. A parse error or an
// unknown surface is returned with the path that caused it.
func landedFor(file, text string, cfg core.Config) (landedEntry, error) {
	_, err := ledger.ParseDecisionFile(file, []byte(text))
	if err != nil {
		return landedEntry{}, fmt.Errorf("malformed decision file %s: line %s", file, lineOf(err))
	}
	relative := strings.Split(strings.TrimPrefix(file, cfg.DesignsRoot+"/"), "/")
	surface := relative[0]
	if cfg.SurfaceDepth == 0 {
		surface = cfg.Surfaces[0]
	}
	if !contains(cfg.Surfaces, surface) {
		return landedEntry{}, fmt.Errorf("unknown design surface %s in %s", surface, file)
	}
	return landedEntry{path: file, decision: LandedDecision{ID: basenameID(file), Surface: surface, Ref: "none"}}, nil
}

// lineOf returns the one-based line of a MalformedDecision, for the error text.
func lineOf(err error) string {
	if malformed, ok := errors.AsType[*ledger.MalformedDecision](err); ok {
		return strconv.Itoa(malformed.Line) + ": " + malformed.Reason
	}
	return err.Error()
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

// sortLanded orders entries by numeric id, then by path, as the TS does.
func sortLanded(entries []landedEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := idNumber(entries[i].decision.ID), idNumber(entries[j].decision.ID)
		if left != right {
			return left < right
		}
		return entries[i].path < entries[j].path
	})
}

// idNumber returns the number in a "DL-<n>" id, for sorting.
func idNumber(id string) int {
	number, err := strconv.Atoi(strings.TrimPrefix(id, "DL-"))
	if err != nil {
		return 0
	}
	return number
}

// basenameID returns the decision id from a file path such as "docs/DL-7.md".
func basenameID(file string) string {
	return strings.TrimSuffix(path.Base(file), ".md")
}

// FormatReconcileOutput prints the counts, then one line per stale claim and per
// duplicate.
func FormatReconcileOutput(r ReconcileResponse) string {
	lines := make([]string, 0, 2+len(r.Stale)+1+len(r.Duplicates))
	lines = append(lines, fmt.Sprintf("updated: %d  inserted: %d", r.Updated, r.Inserted))
	lines = append(lines, fmt.Sprintf("stale: %d", len(r.Stale)))
	for _, entry := range r.Stale {
		lines = append(lines, fmt.Sprintf("  %s %s %s %s %s", entry.ID, entry.Surface, entry.Lane, entry.Ref, entry.Date))
	}
	lines = append(lines, fmt.Sprintf("duplicates: %d", len(r.Duplicates)))
	for _, entry := range r.Duplicates {
		lines = append(lines, fmt.Sprintf("  %s %s", entry.ID, strings.Join(entry.Surfaces, " ")))
	}
	return strings.Join(lines, "\n")
}

// Reconcile posts the landed frontier to the counter and validates the response.
// The token is validated before any request is sent.
func Reconcile(
	ctx context.Context,
	client *http.Client,
	body ReconcileRequest,
	token string,
	cfg core.Config,
) (ReconcileResponse, error) {
	trimmed, err := claim.ValidateDLClaimToken(token)
	if err != nil {
		return ReconcileResponse{}, err
	}
	if len(body.Landed) == 0 {
		return ReconcileResponse{}, errors.New("refusing to reconcile an empty landed frontier")
	}
	endpoint := strings.TrimRight(cfg.Counter.URL, "/") + "/reconcile"
	resp, err := postJSON(ctx, client, endpoint, trimmed, body)
	if err != nil {
		return ReconcileResponse{}, fmt.Errorf("request to %s failed (%s)", endpoint, errorName(err))
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ReconcileResponse{}, fmt.Errorf("reconciliation failed with HTTP %d", resp.StatusCode)
	}
	return readReconcileResponse(resp.Body)
}

// postJSON sends a bearer-authenticated JSON POST that refuses redirects.
func postJSON(ctx context.Context, client *http.Client, endpoint, token string, payload any) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal reconcile request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build reconcile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send reconcile request: %w", err)
	}
	return resp, nil
}

// NewClient returns the client for a reconcile call: a 30-second timeout, and no
// redirects, so the bearer token never follows one.
func NewClient() *http.Client {
	return &http.Client{
		Transport: nil,
		Jar:       nil,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are refused")
		},
	}
}

// errorName returns the error class the TypeScript CLI printed as error.name.
func errorName(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "TimeoutError"
	}
	return "Error"
}

// closeBody closes a fully read response body. The close error cannot be acted on
// once the body is consumed, so it is dropped on purpose.
func closeBody(body io.Closer) {
	_ = body.Close()
}

var errInvalidResponse = errors.New("invalid reconciliation response")

// readReconcileResponse decodes and checks a successful reconcile body.
func readReconcileResponse(body io.Reader) (ReconcileResponse, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return ReconcileResponse{}, errInvalidResponse
	}
	var raw struct {
		Updated    *int              `json:"updated"`
		Inserted   *int              `json:"inserted"`
		Stale      []json.RawMessage `json:"stale"`
		Duplicates []json.RawMessage `json:"duplicates"`
	}
	err = json.Unmarshal(data, &raw)
	if err != nil {
		return ReconcileResponse{}, errInvalidResponse
	}
	if raw.Updated == nil || raw.Inserted == nil || raw.Stale == nil || raw.Duplicates == nil {
		return ReconcileResponse{}, errInvalidResponse
	}
	stale, err := decodeStale(raw.Stale)
	if err != nil {
		return ReconcileResponse{}, err
	}
	duplicates, err := decodeDuplicates(raw.Duplicates)
	if err != nil {
		return ReconcileResponse{}, err
	}
	return ReconcileResponse{Updated: *raw.Updated, Inserted: *raw.Inserted, Stale: stale, Duplicates: duplicates}, nil
}

// decodeStale checks each stale entry carries all five string fields.
func decodeStale(items []json.RawMessage) ([]StaleClaim, error) {
	out := make([]StaleClaim, 0, len(items))
	for _, item := range items {
		var entry struct {
			ID      *string `json:"id"`
			Surface *string `json:"surface"`
			Ref     *string `json:"ref"`
			Lane    *string `json:"lane"`
			Date    *string `json:"date"`
		}
		err := json.Unmarshal(item, &entry)
		if err != nil {
			return nil, errInvalidResponse
		}
		if entry.ID == nil || entry.Surface == nil || entry.Ref == nil || entry.Lane == nil || entry.Date == nil {
			return nil, errInvalidResponse
		}
		out = append(out, StaleClaim{
			ID:      *entry.ID,
			Surface: *entry.Surface,
			Ref:     *entry.Ref,
			Lane:    *entry.Lane,
			Date:    *entry.Date,
		})
	}
	return out, nil
}

// decodeDuplicates checks each duplicate entry has an id and string surfaces.
func decodeDuplicates(items []json.RawMessage) ([]DuplicateClaim, error) {
	out := make([]DuplicateClaim, 0, len(items))
	for _, item := range items {
		var entry struct {
			ID       *string    `json:"id"`
			Surfaces *[]*string `json:"surfaces"`
		}
		err := json.Unmarshal(item, &entry)
		if err != nil {
			return nil, errInvalidResponse
		}
		if entry.ID == nil || entry.Surfaces == nil {
			return nil, errInvalidResponse
		}
		surfaces := make([]string, 0, len(*entry.Surfaces))
		for _, surface := range *entry.Surfaces {
			if surface == nil {
				return nil, errInvalidResponse
			}
			surfaces = append(surfaces, *surface)
		}
		out = append(out, DuplicateClaim{ID: *entry.ID, Surfaces: surfaces})
	}
	return out, nil
}
