// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate

import (
	"context"
	"io"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/gitdeps"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

const usageLine = "Usage: length-gate --config <path> [--all] [--skip-class <name>]..."

// cliArgs is the parsed command line.
type cliArgs struct {
	configPath  string
	all         bool
	skipClasses []string
}

// Main runs the length-gate CLI without exiting the process. It returns 2 for usage, config and Git failures.
// lookup reports whether a variable is set, so a set but empty value is not confused with an unset one.
func Main(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer) int {
	parsed, err := parseArgs(args)
	if err != nil {
		return failLines(stderr, "length-gate: "+err.Error(), usageLine)
	}
	config, err := LoadConfig(parsed.configPath)
	if err != nil {
		return failWith(stderr, err)
	}
	// An empty GATE_ROOT resolves to the working directory, as filepath.Abs("") does.
	gateRoot, _ := lookup("GATE_ROOT")
	root, err := gitdeps.GitTopLevel(ctx, gateRoot)
	if err != nil {
		return failWith(stderr, err)
	}
	gateEnv := gateEnvFrom(lookup, parsed)
	deps := gitdeps.NewGitDeps(root, gateEnv.Head)
	return RunOnce(ctx, deps, config, gateEnv, stdout, stderr)
}

// parseArgs accepts --config once (required), --all once, and --skip-class any number of times.
func parseArgs(args []string) (cliArgs, error) {
	var parsed cliArgs
	hasConfig := false
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; arg {
		case "--config":
			if hasConfig {
				return cliArgs{}, model.MessageError("--config may appear once")
			}
			value, ok := flagValue(args, index)
			if !ok {
				return cliArgs{}, model.MessageError("--config requires a path")
			}
			parsed.configPath = value
			hasConfig = true
			index++
		case "--all":
			if parsed.all {
				return cliArgs{}, model.MessageError("--all may appear once")
			}
			parsed.all = true
		case "--skip-class":
			value, ok := flagValue(args, index)
			if !ok {
				return cliArgs{}, model.MessageError("--skip-class requires a class name")
			}
			parsed.skipClasses = append(parsed.skipClasses, value)
			index++
		default:
			return cliArgs{}, model.Messagef("unknown argument %q", arg)
		}
	}
	if !hasConfig {
		return cliArgs{}, model.MessageError("--config is required")
	}
	return parsed, nil
}

// flagValue returns the operand after the flag at index. It must be present, non-empty and not another flag.
func flagValue(args []string, index int) (string, bool) {
	if index+1 >= len(args) {
		return "", false
	}
	value := args[index+1]
	if value == "" || strings.HasPrefix(value, "--") {
		return "", false
	}
	return value, true
}

// gateEnvFrom reads the LENGTH_GATE_* variables. A set variable counts as set even when empty,
// so only an absent variable falls back to its default.
func gateEnvFrom(lookup func(string) (string, bool), parsed cliArgs) GateEnv {
	head, ok := lookup("LENGTH_GATE_HEAD")
	if !ok {
		head = "HEAD"
	}
	merge, _ := lookup("LENGTH_GATE_MERGE")
	event, _ := lookup("LENGTH_GATE_EVENT")
	return GateEnv{
		Event:       event,
		Base:        optionalValue(lookup, "LENGTH_GATE_BASE"),
		Fetch:       optionalValue(lookup, "LENGTH_GATE_FETCH"),
		Head:        head,
		Merge:       merge == "1",
		All:         parsed.all,
		SkipClasses: parsed.skipClasses,
	}
}

// optionalValue returns the named variable when it is set, and None when it is absent.
func optionalValue(lookup func(string) (string, bool), name string) mo.Option[string] {
	value, ok := lookup(name)
	if !ok {
		return mo.None[string]()
	}
	return mo.Some(value)
}

// failWith prints one diagnostic line and returns exit code 2.
func failWith(stderr io.Writer, err error) int {
	return failLines(stderr, "length-gate: "+err.Error())
}

// failLines prints each line to stderr and returns exit code 2. The exit code already carries the
// failure, so a failed diagnostic write stops the remaining lines without changing it.
func failLines(stderr io.Writer, lines ...string) int {
	for _, line := range lines {
		err := writeLine(stderr, line)
		if err != nil {
			break
		}
	}
	return 2
}
