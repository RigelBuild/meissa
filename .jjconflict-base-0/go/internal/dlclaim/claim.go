// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113,wrapcheck // Preserve the TypeScript CLI's validation and refusal text.
package dlclaim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

type rebindBody struct {
	Repo string   `json:"repo"`
	IDs  []string `json:"ids"`
	From string   `json:"from"`
	To   string   `json:"to"`
}

type releaseBody struct {
	Repo string   `json:"repo"`
	IDs  []string `json:"ids"`
	Lane string   `json:"lane"`
}

// Claim posts a claim and returns the ids the counter minted. The token is
// validated before any request is sent.
func Claim(
	ctx context.Context,
	client *http.Client,
	body ClaimRequest,
	token string,
	cfg core.Config,
) ([]ClaimedID, error) {
	trimmed, err := claim.ValidateDLClaimToken(token)
	if err != nil {
		return nil, err
	}
	endpoint := baseURL(cfg) + "/claim"
	resp, err := postJSON(ctx, client, endpoint, trimmed, body)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed (%s).%s", endpoint, errorName(err), maybeMinted(cfg))
	}
	defer closeBody(resp.Body)
	if !isOK(resp.StatusCode) {
		code := serviceErrorCode(resp.Body)
		return nil, formatServiceError(cfg, resp.StatusCode, code)
	}
	return readClaimedIDs(resp.Body, body.Count, cfg)
}

// Rebind moves claimed ids from one lane to another and returns the ids the counter moved.
func Rebind(ctx context.Context, client *http.Client, a RebindArgs, token string, cfg core.Config) ([]string, error) {
	body := rebindBody{Repo: cfg.Counter.Partition, IDs: a.IDs, From: a.From, To: a.Lane}
	return maintain(ctx, client, maintenance{
		mode:    "rebind",
		path:    "/claims/rebind",
		refusal: a.From,
		ids:     a.IDs,
		payload: body,
		token:   token,
		cfg:     cfg,
	})
}

// Release frees claimed ids for a lane and returns the ids the counter released.
func Release(ctx context.Context, client *http.Client, a ReleaseArgs, token string, cfg core.Config) ([]string, error) {
	body := releaseBody{Repo: cfg.Counter.Partition, IDs: a.IDs, Lane: a.Lane}
	return maintain(ctx, client, maintenance{
		mode:    "release",
		path:    "/claims/release",
		refusal: a.Lane,
		ids:     a.IDs,
		payload: body,
		token:   token,
		cfg:     cfg,
	})
}

// maintenance is one rebind or release request. refusal is the lane named in a 409.
type maintenance struct {
	mode    string
	path    string
	refusal string
	ids     []string
	payload any
	token   string
	cfg     core.Config
}

func maintain(ctx context.Context, client *http.Client, m maintenance) ([]string, error) {
	trimmed, err := claim.ValidateDLClaimToken(m.token)
	if err != nil {
		return nil, err
	}
	endpoint := baseURL(m.cfg) + m.path
	resp, err := postJSON(ctx, client, endpoint, trimmed, m.payload)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed (%s)", endpoint, errorName(err))
	}
	defer closeBody(resp.Body)
	if resp.StatusCode == http.StatusConflict {
		return nil, refusalError(m, resp.Body)
	}
	if !isOK(resp.StatusCode) {
		code := serviceErrorCode(resp.Body)
		return nil, formatServiceError(m.cfg, resp.StatusCode, code)
	}
	returned, err := readMaintenanceIDs(resp.Body)
	if err != nil {
		return nil, err
	}
	if !sameIDSet(returned, m.ids) {
		return nil, errors.New("claim maintenance response has the wrong ids")
	}
	return returned, nil
}

// refusalError reports a 409: the counter refused the whole request and changed nothing.
func refusalError(m maintenance, body io.Reader) error {
	ids := conflictIDs(body)
	named := strings.Join(ids, ", ")
	if named == "" {
		named = "some ids"
	}
	return fmt.Errorf( //nolint:staticcheck // Preserve the TypeScript error text.
		"%s refused, nothing changed: %s not claimed for lane %s.", //nolint:revive // TS error text.
		m.mode, named, m.refusal,
	)
}

// conflictIDs reads the ids a 409 names; malformed data does not hide the refusal.
func conflictIDs(body io.Reader) []string {
	var response map[string]json.RawMessage
	err := json.NewDecoder(body).Decode(&response)
	if err != nil || response == nil {
		return nil
	}
	rawIDs, ok := response["ids"]
	if !ok {
		return nil
	}
	var items []json.RawMessage
	err = json.Unmarshal(rawIDs, &items)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id, ok := decodeJSONString(item); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// readMaintenanceIDs checks that a maintenance body is an object whose ids are all strings.
func readMaintenanceIDs(body io.Reader) ([]string, error) {
	decoder := json.NewDecoder(body)
	var payload json.RawMessage
	err := decoder.Decode(&payload)
	if err != nil {
		return nil, errors.New("claim maintenance response is not valid JSON")
	}
	var trailing json.RawMessage
	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		return nil, errors.New("claim maintenance response is not valid JSON")
	}

	var response map[string]json.RawMessage
	err = json.Unmarshal(payload, &response)
	if err != nil || response == nil {
		return nil, errors.New("claim maintenance response is malformed")
	}
	var rawIDs []json.RawMessage
	err = json.Unmarshal(response["ids"], &rawIDs)
	if err != nil || rawIDs == nil {
		return nil, errors.New("claim maintenance response is malformed")
	}
	ids := make([]string, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		id, ok := decodeJSONString(rawID)
		if !ok {
			return nil, errors.New("claim maintenance response is malformed")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// sameIDSet reports whether returned holds exactly the requested ids, each once.
func sameIDSet(returned, requested []string) bool {
	if len(returned) != len(requested) {
		return false
	}
	seen := make(map[string]bool, len(returned))
	for _, id := range returned {
		seen[id] = true
	}
	if len(seen) != len(returned) {
		return false
	}
	return !slices.ContainsFunc(requested, func(id string) bool { return !seen[id] })
}

// closeBody closes a fully read response body. The close error cannot be acted on
// once the body is consumed, so it is dropped on purpose.
func closeBody(body io.Closer) {
	_ = body.Close()
}
