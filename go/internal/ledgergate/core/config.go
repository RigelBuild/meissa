// SPDX-License-Identifier: MIT OR Apache-2.0

// Package core ports the pure core of packages/design-ledger-gate.
package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// Legs enables the optional validation legs.
type Legs struct {
	Citations   bool
	Errata      bool
	RecordLinks bool
	MainIDs     bool
	ClaimCheck  bool
}

// Counter names the claim counter service and its partition.
type Counter struct {
	URL       string
	Partition string
}

// ExemptBranchRule exempts a branch prefix when the PR author matches.
type ExemptBranchRule struct {
	Prefix string
	Author string
}

// Config is the validated design-ledger configuration.
type Config struct {
	DesignsRoot   string
	SurfaceDepth  int // 0 or 1
	GovernedRoots []string
	Surfaces      []string
	// HistoricalChain nil permits any record to be Historical; non-nil restricts it to the listed paths.
	HistoricalChain []string
	// ExemptBranches nil selects the default renovate and trunk-merge rules; non-nil replaces them.
	ExemptBranches []ExemptBranchRule
	// RecordStatusScope is "all" (default) or "changed".
	RecordStatusScope      string
	CitationAmbiguousPaths []string
	Legs                   Legs
	Counter                Counter
	RemediationDoc         mo.Option[string]
	// RequireSubArea makes a flat decision file a failure. Default false.
	RequireSubArea bool
	// TitleScope is "off" (default), "changed" or "all". It sets which decisions must carry a title.
	TitleScope string
	// ArchiveRoots lists the archive roots a Record may resolve into. Default empty.
	ArchiveRoots []string
}

// contractError is a validation failure whose text is part of the TypeScript contract.
type contractError struct {
	msg   string
	cause error
}

func (e *contractError) Error() string { return e.msg }

func (e *contractError) Unwrap() error { return e.cause }

func contractf(format string, args ...any) error {
	return &contractError{msg: fmt.Sprintf(format, args...), cause: nil}
}

//nolint:gochecknoglobals // The keys are a fixed TypeScript-compatible allowlist.
var configKeys = []string{
	"designsRoot", "surfaceDepth", "governedRoots", "surfaces", "historicalChain",
	"exemptBranches", "citationAmbiguousPaths", "recordStatusScope", "legs",
	"counter", "remediationDoc", "requireSubArea", "titleScope", "archiveRoots",
}

// LoadConfig reads and validates one design-ledger config file. Error text is
// the TypeScript loader's text, except that JSON syntax errors come from
// encoding/json.
func LoadConfig(configPath string) (Config, error) {
	data, err := os.ReadFile(configPath) //nolint:gosec // The CLI reads the caller-supplied config path.
	if err != nil {
		return Config{}, &contractError{msg: fmt.Sprintf("Cannot load config \"%s\": %v", configPath, err), cause: err}
	}
	var parsed any
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		return Config{}, &contractError{msg: fmt.Sprintf("Cannot load config \"%s\": %v", configPath, err), cause: err}
	}
	raw, err := asRecord(parsed, "Config")
	if err != nil {
		return Config{}, err
	}
	return parseConfig(raw)
}

func parseConfig(raw map[string]any) (Config, error) {
	for _, key := range []string{"exemptBranchPrefixes", "exemptAuthors"} {
		if _, found := raw[key]; found {
			return Config{}, contractf("Config key \"%s\" is no longer supported; use exemptBranches", key)
		}
	}
	err := rejectUnknownKeys(raw, configKeys, "config")
	if err != nil {
		return Config{}, err
	}
	designsRoot, ok := raw["designsRoot"].(string)
	if !ok || !isRepoRelativePath(designsRoot) {
		return Config{}, contractf("designsRoot must be a non-empty repository-relative path without ..")
	}
	depth, ok := raw["surfaceDepth"].(float64)
	if !ok || (depth != 0 && depth != 1) {
		return Config{}, contractf("surfaceDepth must be 0 or 1")
	}
	cfg := Config{
		DesignsRoot: designsRoot, SurfaceDepth: int(depth), GovernedRoots: nil,
		Surfaces: nil, HistoricalChain: nil, ExemptBranches: nil, RecordStatusScope: "all",
		CitationAmbiguousPaths: []string{}, Legs: Legs{
			Citations: false, Errata: false, RecordLinks: false, MainIDs: false, ClaimCheck: false,
		},
		Counter:        Counter{URL: "", Partition: ""},
		RemediationDoc: mo.None[string](), RequireSubArea: false,
		TitleScope: "off", ArchiveRoots: []string{},
	}
	for _, step := range []func(map[string]any, *Config) error{
		parseRoots, parseSurfaces, parseHistoricalAndExempt, parseCitationsAndScope, parseLegsAndCounter,
		parseSubAreaTitleAndArchive,
	} {
		err = step(raw, &cfg)
		if err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// parseSubAreaTitleAndArchive parses N1's optional config fields.
func parseSubAreaTitleAndArchive(raw map[string]any, cfg *Config) error {
	if value, present := raw["requireSubArea"]; present {
		flag, ok := value.(bool)
		if !ok {
			return contractf("requireSubArea must be true or false")
		}
		cfg.RequireSubArea = flag
	}
	if value, present := raw["titleScope"]; present {
		scope, ok := value.(string)
		if !ok || (scope != "off" && scope != "changed" && scope != "all") {
			return contractf("titleScope must be \"off\", \"changed\" or \"all\"")
		}
		cfg.TitleScope = scope
	}
	if value, present := raw["archiveRoots"]; present {
		roots, err := readStringArray(value, "archiveRoots")
		if err != nil {
			return err
		}
		for _, root := range roots {
			if !isRepoRelativePath(root) || path.Clean(root) != root {
				return contractf("archiveRoots entries must be repository-relative normalized paths without ..")
			}
			if nestsUnder(root, cfg.DesignsRoot) || nestsUnder(cfg.DesignsRoot, root) {
				return contractf("archiveRoots entry %s must not contain or sit under designsRoot", root)
			}
		}
		for i, root := range roots {
			for _, other := range roots[i+1:] {
				if nestsUnder(root, other) || nestsUnder(other, root) {
					return contractf("archiveRoots entries %s and %s must not overlap", root, other)
				}
			}
		}
		cfg.ArchiveRoots = roots
	}
	return nil
}

// nestsUnder reports whether path equals root or sits under it. The match is on
// a path boundary, so docs/designs-old is not under docs/designs.
func nestsUnder(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func parseRoots(raw map[string]any, cfg *Config) error {
	if value, present := raw["governedRoots"]; present {
		roots, err := readStringArray(value, "governedRoots")
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			return contractf("governedRoots must not be empty")
		}
		for _, root := range roots {
			if !isRepoRelativePath(root) {
				return contractf("governedRoots entries must be repository-relative paths without ..")
			}
		}
		cfg.GovernedRoots = roots
	}
	if cfg.SurfaceDepth == 0 && cfg.GovernedRoots == nil {
		return contractf("governedRoots is required when surfaceDepth is 0")
	}
	if cfg.SurfaceDepth == 1 && cfg.GovernedRoots != nil {
		return contractf("governedRoots is only valid when surfaceDepth is 0")
	}
	return nil
}

func parseSurfaces(raw map[string]any, cfg *Config) error {
	value, present := raw["surfaces"]
	if !present {
		return nil
	}
	surfaces, err := readStringArray(value, "surfaces")
	if err != nil {
		return err
	}
	if len(surfaces) == 0 {
		return contractf("surfaces must not be empty")
	}
	for _, surface := range surfaces {
		if jstext.Trim(surface) == "" || surface == "." || surface == ".." || strings.ContainsAny(surface, `/\`) {
			return contractf("surfaces entries must be unique directory names")
		}
	}
	if hasDuplicate(surfaces) {
		return contractf("surfaces entries must be unique directory names")
	}
	if cfg.SurfaceDepth == 0 && len(surfaces) != 1 {
		return contractf("surfaces must have exactly one entry when surfaceDepth is 0")
	}
	cfg.Surfaces = surfaces
	return nil
}

func parseHistoricalAndExempt(raw map[string]any, cfg *Config) error {
	if value, present := raw["historicalChain"]; present {
		chain, err := readStringArray(value, "historicalChain")
		if err != nil {
			return err
		}
		cfg.HistoricalChain = chain
	}
	value, present := raw["exemptBranches"]
	if !present {
		return nil
	}
	entries, ok := value.([]any)
	if !ok {
		return contractf("exemptBranches must be an array of objects")
	}
	// Non-nil even when empty: an empty list replaces the default rules.
	rules := make([]ExemptBranchRule, 0, len(entries))
	for index, item := range entries {
		rule, err := parseExemptRule(item, index)
		if err != nil {
			return err
		}
		rules = append(rules, rule)
	}
	cfg.ExemptBranches = rules
	return nil
}

func parseExemptRule(item any, index int) (ExemptBranchRule, error) {
	label := fmt.Sprintf("exemptBranches[%d]", index)
	entry, err := asRecord(item, label)
	if err != nil {
		return ExemptBranchRule{}, err
	}
	err = rejectUnknownKeys(entry, []string{"prefix", "author"}, label)
	if err != nil {
		return ExemptBranchRule{}, err
	}
	prefix, ok := entry["prefix"].(string)
	if !ok || jstext.Trim(prefix) == "" {
		return ExemptBranchRule{}, contractf("%s.prefix must be a non-empty string", label)
	}
	author, ok := entry["author"].(string)
	if !ok || jstext.Trim(author) == "" {
		return ExemptBranchRule{}, contractf("%s.author must be a non-empty string", label)
	}
	return ExemptBranchRule{Prefix: prefix, Author: author}, nil
}

func parseCitationsAndScope(raw map[string]any, cfg *Config) error {
	cfg.CitationAmbiguousPaths = []string{}
	if value, present := raw["citationAmbiguousPaths"]; present {
		paths, err := readStringArray(value, "citationAmbiguousPaths")
		if err != nil {
			return err
		}
		for _, item := range paths {
			if !isNormalizedRepoPath(item) {
				return contractf("citationAmbiguousPaths entries must be normalized repository-relative paths")
			}
		}
		cfg.CitationAmbiguousPaths = paths
	}
	cfg.RecordStatusScope = "all"
	if value, present := raw["recordStatusScope"]; present {
		scope, ok := value.(string)
		if !ok || (scope != "all" && scope != "changed") {
			return contractf("recordStatusScope must be \"all\" or \"changed\"")
		}
		cfg.RecordStatusScope = scope
	}
	return nil
}

func parseLegsAndCounter(raw map[string]any, cfg *Config) error {
	if value, present := raw["legs"]; present {
		legs, err := parseLegs(value)
		if err != nil {
			return err
		}
		cfg.Legs = legs
	}
	counter, err := asRecord(raw["counter"], "counter")
	if err != nil {
		return err
	}
	err = rejectUnknownKeys(counter, []string{"url", "partition"}, "counter")
	if err != nil {
		return err
	}
	url, urlOK := counter["url"].(string)
	partition, partitionOK := counter["partition"].(string)
	if !urlOK || !partitionOK {
		return contractf("counter.url and counter.partition must be strings")
	}
	cfg.Counter = Counter{URL: url, Partition: partition}
	if value, present := raw["remediationDoc"]; present {
		doc, ok := value.(string)
		if !ok {
			return contractf("remediationDoc must be a string")
		}
		cfg.RemediationDoc = mo.Some(doc)
	}
	return nil
}

func parseLegs(value any) (Legs, error) {
	legConfig, err := asRecord(value, "legs")
	if err != nil {
		return Legs{}, err
	}
	err = rejectUnknownKeys(legConfig, []string{"citations", "errata", "recordLinks", "mainIds", "claimCheck"}, "legs")
	if err != nil {
		return Legs{}, err
	}
	for key, item := range legConfig {
		if _, ok := item.(bool); !ok {
			return Legs{}, contractf("legs.%s must be a boolean", key)
		}
	}
	flag := func(key string) bool {
		on, _ := legConfig[key].(bool)
		return on
	}
	return Legs{
		Citations:   flag("citations"),
		Errata:      flag("errata"),
		RecordLinks: flag("recordLinks"),
		MainIDs:     flag("mainIds"),
		ClaimCheck:  flag("claimCheck"),
	}, nil
}

func asRecord(value any, label string) (map[string]any, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, contractf("%s must be an object", label)
	}
	return record, nil
}

func rejectUnknownKeys(value map[string]any, allowed []string, label string) error {
	for key := range value {
		if !slices.Contains(allowed, key) {
			return contractf("Unknown key \"%s\" in %s", key, label)
		}
	}
	return nil
}

func readStringArray(value any, label string) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, contractf("%s must be an array of strings", label)
	}
	result := make([]string, 0, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, contractf("%s[%d] must be a string", label, index)
		}
		result = append(result, text)
	}
	return result, nil
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, dup := seen[value]; dup {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

// isRepoRelativePath mirrors the TypeScript helper: non-empty, not absolute,
// no drive letter, no backslash, and no "..".
func isRepoRelativePath(value string) bool {
	return value != "" &&
		!path.IsAbs(value) &&
		!hasDriveLetter(value) &&
		!strings.Contains(value, `\`) &&
		!strings.Contains(value, "..")
}

// hasDriveLetter matches the TypeScript pattern /^[A-Za-z]:[\\/]/.
func hasDriveLetter(value string) bool {
	if len(value) < 3 || value[1] != ':' {
		return false
	}
	letter := value[0]
	isLetter := (letter >= 'A' && letter <= 'Z') || (letter >= 'a' && letter <= 'z')
	return isLetter && (value[2] == '\\' || value[2] == '/')
}

// isNormalizedRepoPath mirrors the citationAmbiguousPaths check. A trailing
// slash survives normalization in JavaScript, so it is kept here too.
func isNormalizedRepoPath(item string) bool {
	if item == "" || item == "." || item == ".." || strings.Contains(item, `\`) || path.IsAbs(item) {
		return false
	}
	normalized := path.Clean(item)
	if strings.HasSuffix(item, "/") && normalized != "." {
		normalized += "/"
	}
	return normalized == item && !strings.HasPrefix(item, "../")
}
