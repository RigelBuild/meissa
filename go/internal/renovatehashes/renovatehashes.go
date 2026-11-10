// SPDX-License-Identifier: MIT OR Apache-2.0

// Package renovatehashes refreshes Nix source and vendor hashes after Renovate updates Go pins.
package renovatehashes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/samber/mo"
)

const (
	pinFile = "tools/toolchain/go-analysis-pins.nix"
	fakeSRI = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

// Entry describes the source and vendor hash fields for one Nix derivation.
type Entry struct {
	Name       string
	File       string
	SourceAttr mo.Option[string]
	VendorAttr string
}

// Runner executes a command and returns its output, exit code, and spawn error.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, exit int, err error)
}

func defaultEntries() []Entry {
	return []Entry{
		{
			Name:       "nilaway",
			File:       pinFile,
			SourceAttr: mo.Some("src"),
			VendorAttr: "goModules",
		},
		{
			Name:       "golangci-lint",
			File:       pinFile,
			SourceAttr: mo.Some("src"),
			VendorAttr: "goModules",
		},
		{
			Name:       "meissa-tools",
			File:       "go/package.nix",
			SourceAttr: mo.None[string](),
			VendorAttr: "goModules",
		},
	}
}

type execRunner struct{ root string }

func (r execRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	return r.runWithEnv(ctx, nil, name, args...)
}

func (r execRunner) runWithEnv(
	ctx context.Context,
	extraEnv map[string]string,
	name string,
	args ...string,
) (string, string, int, error) {
	// #nosec G204 -- The command name and arguments are fixed by this internal refresher.
	command := exec.CommandContext(ctx, name, args...)
	if r.root != "" {
		command.Dir = r.root
	}
	if len(extraEnv) > 0 {
		command.Env = os.Environ()
		for key, value := range extraEnv {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	var stdout strings.Builder
	var stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if runErr != nil {
		if exitError, ok := errors.AsType[*exec.ExitError](runErr); ok {
			return stdout.String(), stderr.String(), exitError.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), -1, fmt.Errorf("run %s: %w", name, runErr)
	}
	return stdout.String(), stderr.String(), 0, nil
}

func withPrefix(err error) error {
	if err == nil || strings.HasPrefix(err.Error(), "renovate-go-analysis:") {
		return err
	}
	return fmt.Errorf("renovate-go-analysis: %w", err)
}

var (
	errRefreshFailure = errors.New("renovate hash refresh failure")
	// ErrRefreshSIGINT is the cancellation cause for an interrupt signal.
	ErrRefreshSIGINT = errors.New("SIGINT")
	// ErrRefreshSIGTERM is the cancellation cause for a termination signal.
	ErrRefreshSIGTERM = errors.New("SIGTERM")
)

type refreshFailureError struct {
	message string
}

func (e refreshFailureError) Error() string {
	return e.message
}

func (e refreshFailureError) Unwrap() error {
	return errRefreshFailure
}

type fileSnapshot struct {
	data []byte
	mode os.FileMode
}

func failf(format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	return withPrefix(refreshFailureError{message: message})
}
