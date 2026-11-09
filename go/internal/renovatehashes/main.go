// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Main executes the hash refresh CLI contract without exiting the process.
func Main(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return mainWithRunner(ctx, args, getenv, stdout, stderr, execRunner{root: ""})
}

func mainWithRunner(
	ctx context.Context,
	_ []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	runner Runner,
) int {
	root, stderrText, exit, err := runner.Run(ctx, "git", "rev-parse", "--show-toplevel")
	if err != nil || exit != 0 {
		return reportFailure(stderr, failf("could not find Git root: %s", commandOutput(err, stderrText)))
	}
	root = strings.TrimSpace(root)
	if _, ok := runner.(execRunner); ok {
		runner = execRunner{root: root}
	}
	selected, err := refresh(ctx, root, defaultEntries(), runner, getenv)
	if err != nil {
		return reportFailure(stderr, err)
	}
	for _, entry := range selected {
		var message string
		if _, hasSource := entry.SourceAttr.Get(); hasSource {
			message = fmt.Sprintf("renovate-go-analysis: refreshed %s source and vendor hashes\n", entry.Name)
		} else {
			message = fmt.Sprintf("renovate-go-analysis: refreshed %s vendor hash\n", entry.Name)
		}
		_, writeErr := io.WriteString(stdout, message)
		if writeErr != nil {
			return reportFailure(stderr, withPrefix(writeErr))
		}
	}
	return 0
}

func reportFailure(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, withPrefix(err))
	return 1
}

func commandOutput(err error, stderr string) string {
	if err != nil {
		return err.Error()
	}
	return stderr
}
