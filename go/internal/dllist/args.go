// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:err113 // Preserve the TypeScript CLI's validation text.
package dllist

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// usage is the dl-list usage line, printed for --help and after a usage error.
//
//nolint:gochecknoglobals // The static help output is shared by every CLI invocation.
var usage = "usage: dl-list [--config PATH] [--surface S] [--area A] [--status " +
	strings.Join(statuses, "|") + "] [--grep TEXT] [--glob GLOB]"

//nolint:gochecknoglobals // The static flag list is shared by every parse.
var valueFlags = []string{"--config", "--surface", "--area", "--status", "--grep", "--glob"}

// ParsedArgs is the parsed dl-list command line.
type ParsedArgs struct {
	Filters    Filters
	ConfigPath string // default "design-ledger.config.json"
	Glob       string // empty when no --glob was given
	Help       bool
}

// ParseArgs parses dl-list arguments. Unknown or misused flags return an error.
func ParseArgs(argv []string) (ParsedArgs, error) {
	parsed := ParsedArgs{
		Filters:    Filters{Surface: "", Area: "", Status: "", Grep: ""},
		ConfigPath: "design-ledger.config.json",
		Glob:       "",
		Help:       false,
	}

	configProvided := false
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg == "--help" || arg == "-h" {
			parsed.Help = true
			continue
		}
		flag, inline, hasInline := strings.Cut(arg, "=")
		if !slices.Contains(valueFlags, flag) {
			return ParsedArgs{}, fmt.Errorf("unknown argument %s", arg)
		}
		value, next, err := readArgument(argv, index, flag, inline, hasInline)
		if err != nil {
			return ParsedArgs{}, err
		}
		index = next
		if flag == "--config" {
			if configProvided {
				return ParsedArgs{}, errors.New("--config may appear once")
			}
			parsed.ConfigPath = value
			configProvided = true
			continue
		}
		err = setFilter(&parsed, flag, value)
		if err != nil {
			return ParsedArgs{}, err
		}
	}
	return parsed, nil
}

// setFilter stores the value of a filter flag. --status is lowercased and checked.
func setFilter(parsed *ParsedArgs, flag, value string) error {
	switch flag {
	case "--glob":
		parsed.Glob = value
	case "--status":
		status := jstext.ToLower(value)
		if !isStatus(status) {
			return fmt.Errorf("--status must be one of %s", strings.Join(statuses, ", "))
		}
		parsed.Filters.Status = status
	case "--surface":
		parsed.Filters.Surface = value
	case "--area":
		parsed.Filters.Area = value
	case "--grep":
		parsed.Filters.Grep = value
	}
	return nil
}

// readArgument returns a flag value, from its inline form or the next argument.
// It rejects an empty value and a value that looks like another flag.
func readArgument(argv []string, index int, flag, inline string, hasInline bool) (string, int, error) {
	value, next := inline, index
	if !hasInline {
		if index+1 >= len(argv) {
			return "", index, fmt.Errorf("%s needs a value", flag)
		}
		value, next = argv[index+1], index+1
	}
	if value == "" || (!hasInline && strings.HasPrefix(value, "--")) {
		return "", index, fmt.Errorf("%s needs a value", flag)
	}
	return value, next, nil
}
