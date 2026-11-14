// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113,wrapcheck // Preserve the CLI contract and its diagnostics.
package dlreconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

const usageLine = "Usage: dl-reconcile --config <path> [--check] [--stale-exit]"

// options are the parsed dl-reconcile flags.
type options struct {
	configPath string
	check      bool
	staleExit  bool
}

// Main runs dl-reconcile and returns its exit code: 0 success, 1 a reconcile
// failure or stale claims under --stale-exit, 2 a usage, config or token error.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--help" {
		err := writeOutput(stdout, usageLine)
		if err != nil {
			return reportOutputFailure(stderr, fmt.Errorf("cannot write output: %w", err))
		}
		return 0
	}
	opts, err := parseCLIArgs(args)
	if err != nil {
		reportError(stderr, "dl-reconcile: "+err.Error())
		return 2
	}
	root, err := repositoryRoot(ctx, env)
	if err != nil {
		reportError(stderr, "dl-reconcile: "+err.Error())
		return 2
	}
	cfg, body, err := prepare(ctx, opts.configPath, root)
	if err != nil {
		reportError(stderr, "dl-reconcile: "+err.Error())
		return 2
	}
	if opts.check {
		err = writeOutput(stdout, fmt.Sprintf("Design ledger parse check passed (%d decision files).", len(body.Landed)))
		if err != nil {
			return reportOutputFailure(stderr, fmt.Errorf("cannot write output: %w", err))
		}
		return 0
	}
	token, err := claim.ReadDLClaimToken(env)
	if err != nil {
		reportError(stderr, "dl-reconcile: "+err.Error())
		return 2
	}
	trimmed, err := claim.ValidateDLClaimToken(token)
	if err != nil {
		reportError(stderr, "dl-reconcile: "+err.Error())
		return 2
	}
	return reconcileAndReport(ctx, body, trimmed, cfg, opts, stdout, stderr)
}

// reconcileAndReport posts the frontier and prints the counter's answer.
func reconcileAndReport(
	ctx context.Context,
	body ReconcileRequest,
	token string,
	cfg core.Config,
	opts options,
	stdout, stderr io.Writer,
) int {
	response, err := Reconcile(ctx, NewClient(), body, token, cfg)
	if err != nil {
		reportError(stderr, "dl-reconcile: "+err.Error())
		return 1
	}
	err = writeOutput(stdout, FormatReconcileOutput(response))
	if err != nil {
		return reportOutputFailure(stderr, fmt.Errorf("cannot write output: %w", err))
	}
	if opts.staleExit && len(response.Stale) > 0 {
		return 1
	}
	return 0
}

// writeOutput writes one CLI result block and returns any writer failure.
func writeOutput(w io.Writer, value string) error {
	_, err := fmt.Fprintln(w, value)
	return err
}

// reportOutputFailure prints a best-effort diagnostic and returns exit code 1.
func reportOutputFailure(stderr io.Writer, err error) int {
	reportError(stderr, "dl-reconcile: "+err.Error())
	return 1
}

// reportError prints a best-effort diagnostic; a failed stderr write cannot be reported further.
func reportError(stderr io.Writer, message string) {
	_, _ = fmt.Fprintln(stderr, message)
}

// prepare loads the config, discovers the decision files and builds the request.
func prepare(ctx context.Context, configPath, root string) (core.Config, ReconcileRequest, error) {
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return core.Config{}, ReconcileRequest{}, err
	}
	files, err := discoverDecisionFiles(ctx, root, cfg)
	if err != nil {
		return core.Config{}, ReconcileRequest{}, err
	}
	body, err := AssertReconcilableLedgers(cfg, files)
	if err != nil {
		return core.Config{}, ReconcileRequest{}, err
	}
	return cfg, body, nil
}

// parseCLIArgs parses dl-reconcile flags. --config is required, and each flag may appear once.
func parseCLIArgs(args []string) (options, error) {
	var opts options
	configSeen := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--check":
			if opts.check {
				return options{}, errors.New("duplicate --check flag")
			}
			opts.check = true
		case arg == "--stale-exit":
			if opts.staleExit {
				return options{}, errors.New("duplicate --stale-exit flag")
			}
			opts.staleExit = true
		case arg == "--config" || strings.HasPrefix(arg, "--config="):
			if configSeen {
				return options{}, errors.New("duplicate --config flag")
			}
			configSeen = true
			value, next, err := configValue(args, index, arg)
			if err != nil {
				return options{}, err
			}
			opts.configPath = value
			index = next
		default:
			return options{}, fmt.Errorf("unknown argument: %s", arg)
		}
	}
	if !configSeen {
		return options{}, errors.New("--config is required")
	}
	return opts, nil
}

// configValue reads the --config path from its inline form or the next argument.
func configValue(args []string, index int, arg string) (string, int, error) {
	if inline, ok := strings.CutPrefix(arg, "--config="); ok {
		if inline == "" {
			return "", index, errors.New("--config requires a path")
		}
		return inline, index, nil
	}
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
		return "", index, errors.New("--config requires a path")
	}
	return args[index+1], index + 1, nil
}

// repositoryRoot returns GATE_ROOT when set, else the git top-level directory.
func repositoryRoot(ctx context.Context, env func(string) string) (string, error) {
	if gateRoot := env("GATE_ROOT"); gateRoot != "" {
		root, err := filepath.Abs(gateRoot)
		if err != nil {
			return "", fmt.Errorf("cannot find git root: %w", err)
		}
		return root, nil
	}

	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err == nil {
		return strings.TrimSpace(string(output)), nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr != "" {
			return "", fmt.Errorf("cannot find git root: %s", stderr)
		}
		return "", errors.New("cannot find git root")
	}
	return "", fmt.Errorf("cannot find git root: %w", err)
}

// discoverDecisionFiles walks designsRoot, rejects a misplaced decision path, and
// returns the decision files keyed by normalized path.
func discoverDecisionFiles(ctx context.Context, root string, cfg core.Config) (map[string]string, error) {
	fsys := os.DirFS(root)
	files := map[string]string{}
	walkErr := doublestar.GlobWalk(fsys, cfg.DesignsRoot+"/**", func(name string, _ fs.DirEntry) error {
		err := ctx.Err()
		if err != nil {
			return err
		}
		normalized := strings.ReplaceAll(name, "\\", "/")
		kind := core.ClassifyDesignPath(normalized, cfg)
		if kind == core.PathMisplaced {
			return fmt.Errorf("misplaced decision path %s", normalized)
		}
		if kind != core.PathDecision {
			return nil
		}
		text, err := fs.ReadFile(fsys, normalized)
		if err != nil {
			return err
		}
		files[normalized] = string(text)
		return nil
	}, doublestar.WithFilesOnly(), doublestar.WithNoHidden(), doublestar.WithFailOnIOErrors())
	return files, walkErr
}
