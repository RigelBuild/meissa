// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113,wrapcheck // Preserve the TypeScript CLI's error text and HTTP failure context.
package dlclaim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// requestTimeout matches the TypeScript CLI's 30-second default.
const requestTimeout = 30 * time.Second

var (
	claimedDateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	claimedIDRE   = regexp.MustCompile(`^DL-\d{3,}$`)
	returnedIDRE  = regexp.MustCompile(`^DL-\d+$`)
)

//nolint:gochecknoglobals // Reuse one replacer for JavaScript-compatible URI encoding.
var jsURIEscaper = strings.NewReplacer(
	"+", "%20", "%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2A", "*",
)

// ClaimedID is one id the counter returned, with the claim date.
type ClaimedID struct {
	ID   string
	Date string
}

type claimedIDResponse struct {
	ID   json.RawMessage `json:"id"`
	Date json.RawMessage `json:"date"`
}

type serviceErrorResponse struct {
	Error string `json:"error"`
}

// ClaimRequest is the body of a POST to the counter's /claim endpoint.
type ClaimRequest struct {
	Repo    string `json:"repo"`
	Surface string `json:"surface"`
	Ref     string `json:"ref"`
	Lane    string `json:"lane"`
	Count   int    `json:"count"`
}

// BuildClaimBody builds the claim request for a parsed claim. ParseArgs has
// already checked the surface, so the body takes the surface from args.
func BuildClaimBody(cfg core.Config, a ClaimArgs) ClaimRequest {
	return ClaimRequest{
		Repo:    cfg.Counter.Partition,
		Surface: a.Surface.OrEmpty(),
		Ref:     a.Ref,
		Lane:    a.Lane,
		Count:   a.Count,
	}
}

// newClient refuses redirects, so the bearer token never follows one.
func newClient() *http.Client {
	return &http.Client{
		Transport: nil,
		Jar:       nil,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are refused")
		},
	}
}

func baseURL(cfg core.Config) string {
	return strings.TrimRight(cfg.Counter.URL, "/")
}

// postJSON sends a bearer-authenticated JSON POST and returns the response.
func postJSON(ctx context.Context, client *http.Client, endpoint, token string, payload any) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal claim request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build claim request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send claim request: %w", err)
	}
	return resp, nil
}

// errorName returns the error class the TypeScript CLI printed as error.name.
func errorName(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "TimeoutError"
	}
	return "Error"
}

// decodeResponse decodes one JSON object into its expected wire shape.
func decodeResponse[T any](body io.Reader) (T, error) {
	var zero T
	var response *T
	decoder := json.NewDecoder(body)
	err := decoder.Decode(&response)
	if err != nil {
		return zero, err
	}
	if response == nil {
		return zero, errors.New("response must be an object")
	}
	var trailing json.RawMessage
	err = decoder.Decode(&trailing)
	if err != io.EOF {
		if err == nil {
			return zero, errors.New("response contains multiple JSON values")
		}
		return zero, err
	}
	return *response, nil
}

// serviceErrorCode reads the optional error code from a failed service response.
func serviceErrorCode(body io.Reader) string {
	response, err := decodeResponse[serviceErrorResponse](body)
	if err != nil {
		return ""
	}
	return response.Error
}

// maybeMinted warns that ids may already be consumed, so the caller must not rerun.
func maybeMinted(cfg core.Config) string {
	statusURL := baseURL(cfg) + "/status?repo=" + encodeURIComponent(cfg.Counter.Partition)
	return " Ids may already be consumed; do not rerun. Find this lane's claims with `curl -H " +
		`"Authorization: Bearer $DL_CLAIM_TOKEN" ` + "'" + statusURL + "'`." //nolint:lll // Keep the exact TypeScript command text.
}

// encodeURIComponent matches JavaScript's encodeURIComponent for a query value.
func encodeURIComponent(value string) string {
	return jsURIEscaper.Replace(url.QueryEscape(value))
}

// isOK reports a 2xx status, the same test as the TypeScript response.ok.
func isOK(status int) bool {
	return status >= 200 && status <= 299
}

// formatServiceError builds the error for a non-2xx claim response.
func formatServiceError(cfg core.Config, status int, code string) error {
	codeText := ""
	if code != "" {
		codeText = " (" + code + ")"
	}
	return fmt.Errorf("claim failed with HTTP %d%s.%s", status, codeText, serviceHint(cfg, status, code))
}

func serviceHint(cfg core.Config, status int, code string) string {
	switch {
	case status == http.StatusUnauthorized:
		return " Token missing or wrong."
	case status == http.StatusTooManyRequests && code == "too_many_open_claims":
		return " Too many open claims; land them or remove abandoned ones with --release."
	case status == http.StatusTooManyRequests:
		return " Rate limited; retry later."
	case status == http.StatusServiceUnavailable && code == "not_hydrated":
		return " The reconcile process has not hydrated the " + cfg.Counter.Partition + " partition yet."
	case status == http.StatusBadRequest:
		return ""
	default:
		return maybeMinted(cfg)
	}
}

// readClaimedIDs checks a successful /claim body: it must hold count well-formed ids.
func readClaimedIDs(body io.Reader, count int, cfg core.Config) ([]ClaimedID, error) {
	var payload json.RawMessage
	decoder := json.NewDecoder(body)
	err := decoder.Decode(&payload)
	if err != nil {
		return nil, fmt.Errorf("claim response is not valid JSON.%s", maybeMinted(cfg))
	}
	var trailing json.RawMessage
	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("claim response is not valid JSON.%s", maybeMinted(cfg))
	}

	var response map[string]json.RawMessage
	err = json.Unmarshal(payload, &response)
	if err != nil || response == nil {
		return nil, fmt.Errorf("claim response must contain the requested number of ids.%s", maybeMinted(cfg))
	}
	var rawIDs *[]json.RawMessage
	if rawIDsJSON, ok := response["ids"]; !ok || json.Unmarshal(rawIDsJSON, &rawIDs) != nil {
		return nil, fmt.Errorf("claim response must contain the requested number of ids.%s", maybeMinted(cfg))
	}
	if rawIDs == nil || len(*rawIDs) != count {
		return nil, fmt.Errorf("claim response must contain the requested number of ids.%s", maybeMinted(cfg))
	}

	returned := make([]string, 0, len(*rawIDs))
	entries := make([]claimedIDResponse, 0, len(*rawIDs))
	for _, rawID := range *rawIDs {
		var entry claimedIDResponse
		err := json.Unmarshal(rawID, &entry)
		if err == nil {
			entries = append(entries, entry)
			if id, ok := decodeJSONString(entry.ID); ok && returnedIDRE.MatchString(id) {
				returned = append(returned, id)
			}
		} else {
			entries = append(entries, claimedIDResponse{ID: nil, Date: nil})
		}
	}
	seen := ""
	if len(returned) > 0 {
		seen = " Returned: " + strings.Join(returned, ", ") + "."
	}
	claimed := make([]ClaimedID, 0, len(entries))
	for _, entry := range entries {
		id, validID := decodeJSONString(entry.ID)
		date, validDate := decodeJSONString(entry.Date)
		if !validID || !validDate || !claimedIDRE.MatchString(id) || !claimedDateRE.MatchString(date) {
			return nil, fmt.Errorf("claim response contains a malformed id.%s%s", seen, maybeMinted(cfg))
		}
		claimed = append(claimed, ClaimedID{ID: id, Date: date})
	}
	return claimed, nil
}

func decodeJSONString(raw json.RawMessage) (string, bool) {
	var value *string
	err := json.Unmarshal(raw, &value)
	if err != nil || value == nil {
		return "", false
	}
	return *value, true
}
