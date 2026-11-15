// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:wrapcheck // The caller adds a stable scan diagnostic without changing the underlying text.
package dllist

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// Main runs dl-list in the working directory and returns its exit code: 0
// success, 1 a malformed file or an empty glob, 2 a usage, config or read error.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		reportError(stderr, "dl-list: "+err.Error())
		return 2
	}
	return run(ctx, args, root, env, stdout, stderr)
}

// run is Main with the repository root and the output writers injected.
func run(ctx context.Context, argv []string, root string, _ func(string) string, stdout, stderr io.Writer) int {
	args, err := ParseArgs(argv)
	if err != nil {
		reportError(stderr, err.Error()+"\n"+usage)
		return 2
	}
	if args.Help {
		err = writeOutput(stdout, usage)
		if err != nil {
			return reportOutputFailure(stderr, fmt.Errorf("cannot write output: %w", err))
		}
		return 0
	}
	configPath := args.ConfigPath
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(root, configPath)
	}
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		reportError(stderr, "dl-list: "+err.Error())
		return 2
	}
	if cfg.SurfaceDepth == 0 && args.Filters.Surface != "" && len(cfg.Surfaces) != 1 {
		reportError(stderr, "dl-list: --surface requires exactly one configured surface when surfaceDepth is 0")
		return 2
	}
	glob := args.Glob
	if glob == "" {
		glob = decisionGlob(cfg)
	}
	files, err := readDecisionFiles(ctx, root, glob)
	if err != nil {
		reportError(stderr, "dl-list: cannot scan or read decision files matching "+glob+": "+err.Error())
		return 2
	}
	if len(files) == 0 {
		reportError(stderr, fmt.Sprintf("no decision files match %s under %s", glob, root))
		return 1
	}
	lines, errs := ListDecisions(files, args.Filters, cfg)
	for _, line := range lines {
		err = writeOutput(stdout, line)
		if err != nil {
			return reportOutputFailure(stderr, fmt.Errorf("cannot write output: %w", err))
		}
	}
	for _, line := range errs {
		reportError(stderr, line)
	}
	if len(errs) > 0 {
		return 1
	}
	return 0
}

// writeOutput writes one CLI result line and returns any writer failure.
func writeOutput(w io.Writer, value string) error {
	_, err := fmt.Fprintln(w, value)
	return err
}

// reportOutputFailure prints a best-effort diagnostic and returns exit code 1.
func reportOutputFailure(stderr io.Writer, err error) int {
	reportError(stderr, "dl-list: "+err.Error())
	return 1
}

// reportError prints a best-effort diagnostic; a failed stderr write cannot be reported further.
func reportError(stderr io.Writer, message string) {
	_, _ = fmt.Fprintln(stderr, message)
}

// decisionGlob is the default decision-file glob for the configured depth.
func decisionGlob(cfg core.Config) string {
	if cfg.SurfaceDepth == 1 {
		return cfg.DesignsRoot + "/*/decisions/*/DL-*.md"
	}
	return cfg.DesignsRoot + "/decisions/*/DL-*.md"
}

// readDecisionFiles walks root with glob, skipping hidden paths as Bun's scan does,
// and reads each matching file.
func readDecisionFiles(ctx context.Context, root, glob string) ([]DecisionFile, error) {
	fsys := os.DirFS(root)
	var files []DecisionFile
	walkErr := doublestar.GlobWalk(fsys, glob, func(name string, _ fs.DirEntry) error {
		err := ctx.Err()
		if err != nil {
			return err
		}
		text, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		files = append(files, DecisionFile{Path: strings.ReplaceAll(name, "\\", "/"), Text: string(text)})
		return nil
	}, doublestar.WithFilesOnly(), doublestar.WithNoHidden(), doublestar.WithFailOnIOErrors())
	return files, walkErr
}
