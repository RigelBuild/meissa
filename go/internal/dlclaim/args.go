// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dlclaim ports the dl-claim CLI. It claims, rebinds and releases
// design-ledger decision IDs against the claim counter.
package dlclaim

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

const usage = "Usage: dl-claim --config <path> --ref <RIG-n|none> --lane <branch> [--count 1..10] [--surface <s>]\n" +
	"       dl-claim --config <path> --rebind --from <lane> --lane <lane> --ids DL-a,DL-b\n" +
	"       dl-claim --config <path> --release --lane <lane> --ids DL-a,DL-b"

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the largest DL number the TypeScript CLI accepted.
const maxSafeInteger = 1<<53 - 1

var (
	refRE                   = regexp.MustCompile(`^(RIG-\d+|none)$`)
	countRE                 = regexp.MustCompile(`^\d+$`)
	dlIDRE                  = regexp.MustCompile(`^DL-(\d+)$`)
	errNoConfiguredSurfaces = errors.New("config.surfaces must contain at least one surface")
)

// Args is a parsed dl-claim command: a claim or a maintenance action.
type Args interface{ isArgs() }

// ClaimArgs asks the counter for new decision IDs.
type ClaimArgs struct {
	Ref     string
	Lane    string
	Count   int
	Surface mo.Option[string]
}

// RebindArgs moves claimed IDs from one lane to another.
type RebindArgs struct {
	From string
	Lane string
	IDs  []string
}

// ReleaseArgs frees IDs that a lane has claimed.
type ReleaseArgs struct {
	Lane string
	IDs  []string
}

func (ClaimArgs) isArgs()   {}
func (RebindArgs) isArgs()  {}
func (ReleaseArgs) isArgs() {}

// ParseArgs parses dl-claim arguments against a config. Every error starts with the usage text.
func ParseArgs(argv []string, cfg core.Config) (Args, error) {
	maintenance, isMaintenance, err := parseMaintenance(argv)
	if err != nil {
		return nil, err
	}
	if isMaintenance {
		return maintenance, nil
	}
	flags, err := collectClaimFlags(argv)
	if err != nil {
		return nil, err
	}
	claim, err := resolveClaim(cfg, flags)
	if err != nil {
		return nil, err
	}
	return claim, nil
}

func usageError(message string) error {
	return errors.New(usage + "\n" + message) //nolint:err113 // message mirrors the TS CLI
}

// readFlagValue returns a flag value, taken inline or from the next argument. It
// also returns the index of the last argument it consumed.
func readFlagValue(argv []string, index int, flag, inline string, hasInline bool) (string, int, error) {
	value, next := inline, index
	if !hasInline {
		if index+1 >= len(argv) {
			return "", index, usageError("Missing value for " + flag)
		}
		value, next = argv[index+1], index+1
	}
	if strings.HasPrefix(value, "--") {
		return "", index, usageError("Missing value for " + flag)
	}
	return value, next, nil
}

// parseMaintenance parses --rebind or --release. It reports false when argv names neither.
func parseMaintenance(argv []string) (Args, bool, error) {
	rebind := slices.Contains(argv, "--rebind")
	release := slices.Contains(argv, "--release")
	if !rebind && !release {
		return nil, false, nil
	}
	if rebind && release {
		return nil, true, usageError("Choose one maintenance mode")
	}
	mode, allowed := "release", []string{"--release", "--lane", "--ids"}
	if rebind {
		mode, allowed = "rebind", []string{"--rebind", "--from", "--lane", "--ids"}
	}
	values, err := maintenanceValues(argv, mode, allowed)
	if err != nil {
		return nil, true, err
	}
	lane, hasLane := values["--lane"]
	rawIDs, hasIDs := values["--ids"]
	if !hasLane || jstext.Trim(lane) == "" || !hasIDs {
		return nil, true, usageError("Maintenance mode requires --lane and --ids")
	}
	ids, err := parseDLIDs(rawIDs)
	if err != nil {
		return nil, true, err
	}
	if !rebind {
		return ReleaseArgs{Lane: lane, IDs: ids}, true, nil
	}
	from, hasFrom := values["--from"]
	if !hasFrom || jstext.Trim(from) == "" {
		return nil, true, usageError("--from must be non-empty")
	}
	return RebindArgs{From: from, Lane: lane, IDs: ids}, true, nil
}

func maintenanceValues(argv []string, mode string, allowed []string) (map[string]string, error) {
	values := map[string]string{}
	for index := 0; index < len(argv); index++ {
		flag, inline, hasInline := strings.Cut(argv[index], "=")
		if !slices.Contains(allowed, flag) {
			return nil, usageError("Unknown or mixed flag: " + argv[index])
		}
		if flag == "--rebind" || flag == "--release" {
			if hasInline || flag != "--"+mode {
				return nil, usageError("Invalid maintenance mode")
			}
			continue
		}
		if _, dup := values[flag]; dup {
			return nil, usageError("Duplicate flag: " + flag)
		}
		value, next, err := readFlagValue(argv, index, flag, inline, hasInline)
		if err != nil {
			return nil, err
		}
		values[flag] = value
		index = next
	}
	return values, nil
}

// parseDLIDs splits a comma list of DL ids and pads each number to three digits.
func parseDLIDs(raw string) ([]string, error) {
	seen := map[string]bool{}
	ids := []string{}
	for rawID := range strings.SplitSeq(raw, ",") {
		match := dlIDRE.FindStringSubmatch(rawID)
		if match == nil {
			return nil, usageError("Invalid DL id: " + rawID)
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number > maxSafeInteger {
			return nil, usageError("Invalid DL id: " + rawID)
		}
		id := fmt.Sprintf("DL-%03d", number)
		if seen[id] {
			return nil, usageError("Duplicate DL id: " + id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

type claimFlags struct {
	ref     mo.Option[string]
	lane    mo.Option[string]
	surface mo.Option[string]
	count   int
}

func collectClaimFlags(argv []string) (claimFlags, error) {
	flags := claimFlags{
		ref:     mo.None[string](),
		lane:    mo.None[string](),
		surface: mo.None[string](),
		count:   1,
	}
	for index := 0; index < len(argv); index++ {
		flag, inline, hasInline := strings.Cut(argv[index], "=")
		if !slices.Contains([]string{"--ref", "--lane", "--count", "--surface"}, flag) {
			return claimFlags{}, usageError("Unknown flag: " + argv[index])
		}
		value, next, err := readFlagValue(argv, index, flag, inline, hasInline)
		if err != nil {
			return claimFlags{}, err
		}
		index = next
		err = setClaimFlag(&flags, flag, value)
		if err != nil {
			return claimFlags{}, err
		}
	}
	return flags, nil
}

func setClaimFlag(flags *claimFlags, flag, value string) error {
	switch flag {
	case "--ref":
		flags.ref = mo.Some(value)
	case "--lane":
		flags.lane = mo.Some(value)
	case "--surface":
		flags.surface = mo.Some(value)
	case "--count":
		return setCount(flags, value)
	}
	return nil
}

func setCount(flags *claimFlags, value string) error {
	if !countRE.MatchString(value) {
		return usageError("Invalid count: " + value)
	}
	// A value too large for int is rejected by resolveClaim's 1..10 range check.
	count, err := strconv.Atoi(value)
	if err != nil {
		if !errors.Is(err, strconv.ErrRange) {
			return usageError("Invalid count: " + value)
		}
		count = maxSafeInteger
	}
	flags.count = count
	return nil
}

func resolveClaim(cfg core.Config, flags claimFlags) (ClaimArgs, error) {
	ref, hasRef := flags.ref.Get()
	if !hasRef {
		return ClaimArgs{}, usageError("--ref is required")
	}
	if !refRE.MatchString(ref) {
		return ClaimArgs{}, usageError("Invalid ref: " + ref)
	}
	lane, hasLane := flags.lane.Get()
	if !hasLane || jstext.Trim(lane) == "" {
		return ClaimArgs{}, usageError("--lane must be non-empty")
	}
	if flags.count < 1 || flags.count > 10 {
		return ClaimArgs{}, usageError("Count must be an integer from 1 to 10")
	}
	if len(cfg.Surfaces) == 0 {
		return ClaimArgs{}, errNoConfiguredSurfaces
	}
	surface, err := claimSurface(cfg.Surfaces, flags.surface)
	if err != nil {
		return ClaimArgs{}, err
	}
	return ClaimArgs{Ref: ref, Lane: lane, Count: flags.count, Surface: mo.Some(surface)}, nil
}

// claimSurface returns the surface a claim names. Without --surface, the only
// configured surface is used; with several, --surface is required.
func claimSurface(surfaces []string, requested mo.Option[string]) (string, error) {
	if name, given := requested.Get(); given {
		if slices.Contains(surfaces, name) {
			return name, nil
		}
		return "", usageError("Unknown surface: " + name)
	}
	if len(surfaces) == 1 {
		return surfaces[0], nil
	}
	return "", usageError("--surface is required for multiple surfaces")
}
