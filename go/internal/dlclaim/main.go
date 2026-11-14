// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:wrapcheck // The CLI preserves the original user-facing diagnostics.
package dlclaim

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// Main runs dl-claim and returns its exit code: 0 success, 1 service or request
// failure, 2 usage, config or token error.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--help" {
		err := writeOutput(stdout, usage)
		if err != nil {
			return fail(stderr, 1, fmt.Errorf("cannot write output: %w", err))
		}
		return 0
	}
	configPath, claimArgs, err := extractConfigPath(args)
	if err != nil {
		return fail(stderr, 2, err)
	}
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return fail(stderr, 2, err)
	}
	parsed, err := ParseArgs(claimArgs, cfg)
	if err != nil {
		return fail(stderr, 2, err)
	}
	token, err := claim.ReadDLClaimToken(env)
	if err != nil {
		return fail(stderr, 2, err)
	}
	trimmed, err := claim.ValidateDLClaimToken(token)
	if err != nil {
		return fail(stderr, 2, err)
	}
	return run(ctx, parsed, trimmed, cfg, stdout, stderr)
}

// run performs the parsed action. Its token is already read; it validates the token itself.
func run(ctx context.Context, parsed Args, token string, cfg core.Config, stdout, stderr io.Writer) int {
	client := newClient()
	switch a := parsed.(type) {
	case ClaimArgs:
		ids, err := Claim(ctx, client, BuildClaimBody(cfg, a), token, cfg)
		if err != nil {
			return fail(stderr, 1, err)
		}
		err = writeOutput(stdout, FormatClaimed(ids))
		if err != nil {
			return fail(stderr, 1, fmt.Errorf("cannot write output: %w", err))
		}
	case RebindArgs:
		ids, err := Rebind(ctx, client, a, token, cfg)
		if err != nil {
			return fail(stderr, 1, err)
		}
		err = writeOutput(stdout, strings.Join(ids, "\n"))
		if err != nil {
			return fail(stderr, 1, fmt.Errorf("cannot write output: %w", err))
		}
	case ReleaseArgs:
		ids, err := Release(ctx, client, a, token, cfg)
		if err != nil {
			return fail(stderr, 1, err)
		}
		err = writeOutput(stdout, strings.Join(ids, "\n"))
		if err != nil {
			return fail(stderr, 1, fmt.Errorf("cannot write output: %w", err))
		}
	}
	return 0
}

// extractConfigPath pulls the one --config flag out of argv and returns the rest.
func extractConfigPath(argv []string) (string, []string, error) {
	var configPath string
	seen := false
	var rest []string
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg != "--config" && !strings.HasPrefix(arg, "--config=") {
			rest = append(rest, arg)
			continue
		}
		if seen {
			return "", nil, usageError("--config may appear once")
		}
		seen = true
		value, next, err := readFlagValue(argv, index, "--config", strings.TrimPrefix(arg, "--config="), arg != "--config")
		if err != nil {
			return "", nil, err
		}
		if value == "" {
			return "", nil, usageError("--config requires a path")
		}
		configPath = value
		index = next
	}
	if !seen {
		return "", nil, usageError("--config is required")
	}
	return configPath, rest, nil
}

// FormatClaimed prints one line per id: "<id> (claimed <date>)".
func FormatClaimed(ids []ClaimedID) string {
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf("%s (claimed %s)", id.ID, id.Date))
	}
	return strings.Join(lines, "\n")
}

// writeOutput writes one CLI result line and returns any writer failure.
func writeOutput(w io.Writer, value string) error {
	_, err := fmt.Fprintln(w, value)
	return err
}
func fail(stderr io.Writer, code int, err error) int {
	reportError(stderr, "dl-claim: "+err.Error())
	return code
}

// reportError prints a best-effort diagnostic; a failed stderr write cannot be reported further.
func reportError(stderr io.Writer, message string) {
	_, _ = fmt.Fprintln(stderr, message)
}
