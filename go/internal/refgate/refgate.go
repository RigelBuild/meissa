// SPDX-License-Identifier: MIT OR Apache-2.0

// Package refgate scans tracked Git files for policy references.
//
//nolint:err113,staticcheck,exhaustruct // Preserve TypeScript contract text and zero-value references.
package refgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
	"github.com/samber/mo"
)

const (
	matchTimeout   = 2 * time.Second
	maxSafeInteger = 1<<53 - 1
)

// Pattern is a JavaScript regular expression and its flags.
type Pattern struct {
	Source string
	Flags  string
}

// Prefilter is a POSIX ERE passed directly to git grep.
type Prefilter struct {
	ERE        string
	IgnoreCase bool
}

// Config holds the complete reference gate policy.
type Config struct {
	Prefilter        Prefilter
	Patterns         []Pattern
	Ignore           []string
	CarveOutPaths    []string
	CarveOutPrefixes []string
	Allowlist        map[string]string
	RemediationDoc   mo.Option[string]
}

// Reference is a matching tracked source line.
type Reference struct {
	File string
	Line int
	Text string
}

type compiledPattern struct {
	pattern *regexp2.Regexp
	sticky  bool
}

type matchFailureError struct {
	patternIndex int
	file         string
	line         int
	timeout      bool
	cause        error
}

func (e matchFailureError) Unwrap() error { return e.cause }

func (e matchFailureError) Error() string {
	message := "hit the backtracking limit"
	if e.timeout {
		message = "timed out"
	}
	return fmt.Sprintf("ref-gate: patterns[%d] %s on %s:%d", e.patternIndex, message, e.file, e.line)
}

//nolint:err113 // Dynamic text is part of the TypeScript validation contract.
func asObject(value any, label string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return object, nil
}

//nolint:err113 // Unknown keys must retain the TypeScript error text.
func rejectUnknownKeys(object map[string]any, allowed []string, label string) error {
	for key := range object {
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("Unknown key %q in %s", key, label)
		}
	}
	return nil
}

//nolint:err113 // Dynamic text is part of the TypeScript validation contract.
func readStringArray(value any, label string) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", label)
	}
	result := make([]string, 0, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", label, index)
		}
		result = append(result, text)
	}
	return result, nil
}

// LoadConfig reads and validates a reference gate configuration file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The CLI reads the caller-supplied config path.
	if err != nil {
		return Config{}, fmt.Errorf("Cannot load config %q: %w", path, err)
	}
	var parsed any
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		return Config{}, fmt.Errorf("Cannot load config %q: %w", path, err)
	}
	object, err := asObject(parsed, "Config")
	if err != nil {
		return Config{}, err
	}
	allowedKeys := []string{
		"prefilter",
		"patterns",
		"ignore",
		"carveOutPaths",
		"carveOutPrefixes",
		"allowlist",
		"remediationDoc",
	}
	err = rejectUnknownKeys(object, allowedKeys, "config")
	if err != nil {
		return Config{}, err
	}

	prefilterObject, err := asObject(object["prefilter"], "prefilter")
	if err != nil {
		return Config{}, err
	}
	err = rejectUnknownKeys(prefilterObject, []string{"ere", "ignoreCase"}, "prefilter")
	if err != nil {
		return Config{}, err
	}
	prefilterERE, ok := prefilterObject["ere"].(string)
	if !ok {
		return Config{}, errors.New("prefilter.ere must be a string") //nolint:err113 // Preserve validation text.
	}
	ignoreCase, ok := prefilterObject["ignoreCase"].(bool)
	if !ok {
		return Config{}, errors.New("prefilter.ignoreCase must be a boolean") //nolint:err113 // Preserve validation text.
	}

	patternValues, ok := object["patterns"].([]any)
	if !ok || len(patternValues) == 0 {
		return Config{}, errors.New(
			"patterns must be a non-empty array of objects",
		) //nolint:err113 // Preserve validation text.
	}
	patterns := make([]Pattern, 0, len(patternValues))
	for index, value := range patternValues {
		label := fmt.Sprintf("patterns[%d]", index)
		patternObject, err := asObject(value, label)
		if err != nil {
			return Config{}, err
		}
		err = rejectUnknownKeys(patternObject, []string{"source", "flags"}, label)
		if err != nil {
			return Config{}, err
		}
		source, ok := patternObject["source"].(string)
		if !ok {
			return Config{}, fmt.Errorf(
				"patterns[%d].source must be a string",
				index,
			) //nolint:err113 // Preserve validation text.
		}
		flags, ok := patternObject["flags"].(string)
		if !ok {
			return Config{}, fmt.Errorf(
				"patterns[%d].flags must be a string",
				index,
			) //nolint:err113 // Preserve validation text.
		}
		_, _, err = compilePattern(source, flags)
		if err != nil {
			return Config{}, fmt.Errorf("Invalid regular expression in patterns[%d]: %w", index, err)
		}
		patterns = append(patterns, Pattern{Source: source, Flags: flags})
	}

	ignore, err := readStringArray(object["ignore"], "ignore")
	if err != nil {
		return Config{}, err
	}
	carveOutPaths, err := readStringArray(object["carveOutPaths"], "carveOutPaths")
	if err != nil {
		return Config{}, err
	}
	carveOutPrefixes, err := readStringArray(object["carveOutPrefixes"], "carveOutPrefixes")
	if err != nil {
		return Config{}, err
	}
	allowlistObject, err := asObject(object["allowlist"], "allowlist")
	if err != nil {
		return Config{}, err
	}
	allowlist := make(map[string]string, len(allowlistObject))
	for file, value := range allowlistObject {
		reason, ok := value.(string)
		if !ok {
			encoded, err := jsonString(file)
			if err != nil {
				return Config{}, err
			}
			return Config{}, fmt.Errorf("allowlist[%s] must be a string", encoded) //nolint:err113 // Preserve validation text.
		}
		allowlist[file] = reason
	}

	config := Config{
		Prefilter:        Prefilter{ERE: prefilterERE, IgnoreCase: ignoreCase},
		Patterns:         patterns,
		Ignore:           ignore,
		CarveOutPaths:    carveOutPaths,
		CarveOutPrefixes: carveOutPrefixes,
		Allowlist:        allowlist,
		RemediationDoc:   mo.None[string](),
	}
	if value, exists := object["remediationDoc"]; exists {
		remediationDoc, ok := value.(string)
		if !ok {
			return Config{}, errors.New("remediationDoc must be a string") //nolint:err113 // Preserve validation text.
		}
		config.RemediationDoc = mo.Some(remediationDoc)
	}
	return config, nil
}

func compilePattern(source, flags string) (*regexp2.Regexp, bool, error) {
	return compilePatternWithLimits(source, flags, matchTimeout, 100000)
}

func parseGrepHit(hit string) (Reference, bool) {
	var zeroReference Reference

	if nulPathEnd := strings.IndexByte(hit, 0); nulPathEnd >= 0 {
		lineEndRelative := strings.IndexByte(hit[nulPathEnd+1:], 0)
		if lineEndRelative < 0 {
			return zeroReference, false
		}
		lineEnd := nulPathEnd + 1 + lineEndRelative
		lineText := hit[nulPathEnd+1 : lineEnd]
		if !decimalDigits(lineText) {
			return zeroReference, false
		}
		line, err := strconv.ParseUint(lineText, 10, 53)
		if err != nil || line > uint64(maxSafeInteger) {
			return zeroReference, false
		}
		return Reference{File: hit[:nulPathEnd], Line: int(line), Text: hit[lineEnd+1:]}, true
	}

	for pathEnd := 0; pathEnd < len(hit); {
		pathDelimiter := strings.IndexByte(hit[pathEnd:], ':')
		if pathDelimiter < 0 {
			break
		}
		pathEnd += pathDelimiter
		nextColonRelative := strings.IndexByte(hit[pathEnd+1:], ':')
		if nextColonRelative < 0 {
			return zeroReference, false
		}
		nextColon := pathEnd + 1 + nextColonRelative
		lineText := hit[pathEnd+1 : nextColon]
		if decimalDigits(lineText) {
			line, err := strconv.ParseUint(lineText, 10, 53)
			if err != nil || line > uint64(maxSafeInteger) {
				return zeroReference, false
			}
			return Reference{File: hit[:pathEnd], Line: int(line), Text: hit[nextColon+1:]}, true
		}
		pathEnd++
	}
	return zeroReference, false
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// ParseGitGrepOutput parses path-NUL-line-NUL-text-LF records.
func ParseGitGrepOutput(output string) ([]string, error) {
	hits := make([]string, 0)
	for index := 0; index < len(output); {
		pathEndRelative := strings.IndexByte(output[index:], 0)
		pathEnd := -1
		if pathEndRelative >= 0 {
			pathEnd = index + pathEndRelative
		}
		lineEnd := -1
		if pathEnd >= 0 {
			lineEndRelative := strings.IndexByte(output[pathEnd+1:], 0)
			if lineEndRelative >= 0 {
				lineEnd = pathEnd + 1 + lineEndRelative
			}
		}
		line := ""
		if lineEnd >= 0 {
			line = output[pathEnd+1 : lineEnd]
		}
		if !decimalDigits(line) {
			message := fmt.Sprintf(
				"unparsable git grep output at offset %d",
				utf16Length(output[:index]),
			)
			return nil, errors.New(message) //nolint:err113 // Preserve parser text.
		}
		textStart := lineEnd + 1
		textEndRelative := strings.IndexByte(output[textStart:], '\n')
		end := len(output)
		if textEndRelative >= 0 {
			end = textStart + textEndRelative
		}
		hits = append(hits, output[index:pathEnd]+"\x00"+line+"\x00"+output[textStart:end])
		index = end + 1
	}
	return hits, nil
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func escapeRegExp(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, char := range value {
		if strings.ContainsRune(".*+?^${}()|[]\\", char) {
			result.WriteByte('\\')
		}
		result.WriteRune(char)
	}
	return result.String()
}

// FindViolations filters prefilter hits through the configured JavaScript patterns.
func FindViolations(config Config, grepHits []string) ([]Reference, error) {
	return findViolationsWithLimits(config, grepHits, matchTimeout, 100000)
}

func findViolationsWithLimits(
	config Config,
	grepHits []string,
	timeout time.Duration,
	stackLimit int,
) ([]Reference, error) {
	patterns := make([]compiledPattern, 0, len(config.Patterns))
	for _, configured := range config.Patterns {
		pattern, sticky, err := compilePatternWithLimits(configured.Source, configured.Flags, timeout, stackLimit)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, compiledPattern{pattern: pattern, sticky: sticky})
	}
	ignoredNames := make([]*regexp2.Regexp, 0, len(config.Ignore))
	for _, name := range config.Ignore {
		if name == "" {
			continue
		}
		ignored, err := regexp2.Compile(
			escapeRegExp(name),
			regexp2.ECMAScript|regexp2.IgnoreCase,
			regexp2.OptionMaxBacktrackingStackSize(stackLimit),
		)
		if err != nil {
			return nil, fmt.Errorf("compile ignored name: %w", err)
		}
		ignored.MatchTimeout = timeout
		ignoredNames = append(ignoredNames, ignored)
	}

	violations := make([]Reference, 0)
	for _, hit := range grepHits {
		line := strings.TrimRight(hit, "\r\n")
		if line == "" {
			continue
		}
		reference, ok := parseGrepHit(line)
		if !ok {
			encoded, err := jsonString(line)
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("unparsable grep hit: %s", encoded) //nolint:err113 // Preserve TypeScript's JSON error text.
		}
		if isCarvedOut(config, reference.File) {
			continue
		}
		matchText := reference.Text
		for _, ignored := range ignoredNames {
			var err error
			matchText, err = ignored.Replace(matchText, "", -1, -1)
			if err != nil {
				return nil, fmt.Errorf("remove ignored name: %w", err)
			}
		}
		for patternIndex, pattern := range patterns {
			matched, err := patternMatches(pattern, matchText)
			if err != nil {
				return nil, matchFailureError{
					patternIndex: patternIndex,
					file:         reference.File,
					line:         reference.Line,
					timeout:      isMatchTimeout(err),
					cause:        err,
				}
			}
			if matched {
				violations = append(violations, reference)
				break
			}
		}
	}
	return violations, nil
}

func isMatchTimeout(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func compilePatternWithLimits(
	source, flags string,
	timeout time.Duration,
	stackLimit int,
) (*regexp2.Regexp, bool, error) {
	options := regexp2.ECMAScript
	seen := make(map[rune]bool, len(flags))
	sticky := false
	for _, flag := range flags {
		if seen[flag] {
			return nil, false, fmt.Errorf("duplicate flag %q", flag) //nolint:err113 // Preserve JavaScript validation text.
		}
		seen[flag] = true
		switch flag {
		case 'i':
			options |= regexp2.IgnoreCase
		case 'm':
			options |= regexp2.Multiline
		case 's':
			options |= regexp2.Singleline
		case 'u':
			options |= regexp2.Unicode
		case 'g', 'd':
		case 'y':
			sticky = true
		default:
			return nil, false, fmt.Errorf("invalid flag %q", flag) //nolint:err113 // Preserve JavaScript validation text.
		}
	}
	pattern, err := regexp2.Compile(source, options, regexp2.OptionMaxBacktrackingStackSize(stackLimit))
	if err != nil {
		return nil, false, fmt.Errorf("compile JavaScript pattern: %w", err)
	}
	pattern.MatchTimeout = timeout
	return pattern, sticky, nil
}

func patternMatches(pattern compiledPattern, text string) (bool, error) {
	if pattern.sticky {
		match, err := pattern.pattern.FindStringMatchStartingAt(text, 0)
		if err != nil {
			return false, fmt.Errorf("match sticky pattern: %w", err)
		}
		return match != nil && match.RuneIndex == 0, nil
	}

	matched, err := pattern.pattern.MatchString(text)
	if err != nil {
		return false, fmt.Errorf("match pattern: %w", err)
	}
	return matched, nil
}

func isCarvedOut(config Config, path string) bool {
	if _, allowed := config.Allowlist[path]; allowed {
		return true
	}
	if slices.Contains(config.CarveOutPaths, path) {
		return true
	}
	for _, prefix := range config.CarveOutPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// jsonString encodes value like TypeScript's JSON.stringify of the decoded text:
// invalid UTF-8 becomes a raw U+FFFD, not a \ufffd escape.
func jsonString(value string) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(replaceInvalidUTF8([]byte(value)))
	if err != nil {
		return "", fmt.Errorf("encode JSON string: %w", err)
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

type grepFunc func(context.Context, string, Prefilter) ([]string, error)

func runOnce(ctx context.Context, root string, config Config, grep grepFunc, stdout, stderr io.Writer) int {
	return runOnceWithLimits(ctx, root, config, grep, stdout, stderr, matchTimeout, 100000)
}

func runOnceWithLimits(
	ctx context.Context,
	root string,
	config Config,
	grep grepFunc,
	stdout, stderr io.Writer,
	timeout time.Duration,
	stackLimit int,
) int {
	hits, err := grep(ctx, root, config.Prefilter)
	if err != nil {
		writeErr := writeOutput(stderr, "ref-gate: scan failed: %s\n", err)
		if !writeErr {
			return 2
		}
		return 2
	}
	violations, err := findViolationsWithLimits(config, hits, timeout, stackLimit)
	if err != nil {
		if failure, ok := errors.AsType[matchFailureError](err); ok {
			writeErr := writeOutput(stderr, "%s\n", failure.Error())
			if !writeErr {
				return 2
			}
			return 2
		}
		writeErr := writeOutput(stderr, "ref-gate: scan failed: %s\n", err)
		if !writeErr {
			return 2
		}
		return 2
	}
	if len(violations) == 0 {
		writeErr := writeOutput(stdout, "ref-gate: clean\n")
		if !writeErr {
			return 2
		}
		return 0
	}
	for _, violation := range violations {
		writeErr := writeOutput(
			stderr,
			"%s:%d: %s\n",
			violation.File,
			violation.Line,
			violation.Text,
		)
		if !writeErr {
			return 2
		}
	}
	if remediationDoc, present := config.RemediationDoc.Get(); present {
		writeErr := writeOutput(stderr, "See %s.\n", remediationDoc)
		if !writeErr {
			return 2
		}
	}
	return 1
}

func writeOutput(w io.Writer, format string, args ...any) bool {
	_, err := fmt.Fprintf(w, format, args...)
	return err == nil
}

func rootPath(ctx context.Context, configured string) (string, error) {
	if configured != "" {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve git root: %w", err)
		}
		return absolute, nil
	}
	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("cannot find git root: %s", commandStderr(err))
	}
	return strings.TrimSpace(string(output)), nil
}

// decodeGitGrepOutput decodes git grep's byte output as TextDecoder does, after stripping a leading BOM.
func decodeGitGrepOutput(output []byte) string {
	if bytes.HasPrefix(output, []byte{0xef, 0xbb, 0xbf}) {
		output = output[3:]
	}
	return replaceInvalidUTF8(output)
}

// replaceInvalidUTF8 applies WHATWG UTF-8 decoding: each maximal invalid subpart becomes one U+FFFD.
func replaceInvalidUTF8(output []byte) string {
	if utf8.Valid(output) {
		return string(output)
	}

	var decoded strings.Builder
	decoded.Grow(len(output))
	for index := 0; index < len(output); {
		first := output[index]
		if first < utf8.RuneSelf {
			decoded.WriteByte(first)
			index++
			continue
		}

		var continuationCount int
		switch {
		case first >= 0xc2 && first <= 0xdf:
			continuationCount = 1
		case first >= 0xe0 && first <= 0xef:
			continuationCount = 2
		case first >= 0xf0 && first <= 0xf4:
			continuationCount = 3
		default:
			decoded.WriteRune(utf8.RuneError)
			index++
			continue
		}

		invalid := false
		for consumed := range continuationCount {
			continuationIndex := index + 1 + consumed
			if continuationIndex == len(output) {
				decoded.WriteRune(utf8.RuneError)
				index = len(output)
				invalid = true
				break
			}

			continuation := output[continuationIndex]
			validContinuation := continuation >= 0x80 && continuation <= 0xbf
			if consumed == 0 {
				switch first {
				case 0xe0:
					validContinuation = continuation >= 0xa0 && continuation <= 0xbf
				case 0xed:
					validContinuation = continuation >= 0x80 && continuation <= 0x9f
				case 0xf0:
					validContinuation = continuation >= 0x90 && continuation <= 0xbf
				case 0xf4:
					validContinuation = continuation >= 0x80 && continuation <= 0x8f
				}
			}
			if !validContinuation {
				decoded.WriteRune(utf8.RuneError)
				index += 1 + consumed
				invalid = true
				break
			}
		}
		if invalid {
			continue
		}

		end := index + 1 + continuationCount
		decoded.Write(output[index:end])
		index = end
	}
	return decoded.String()
}

// GitGrep runs the POSIX ERE prefilter over every tracked file as text.
func GitGrep(ctx context.Context, root string, prefilter Prefilter) ([]string, error) {
	topCommand := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	topCommand.Dir = root
	topOutput, topErr := topCommand.Output()
	topLevel := strings.TrimSpace(string(topOutput))
	if topErr != nil {
		return nil, fmt.Errorf("%s is not the top level of a git repository", root) //nolint:err113 // Public error text.
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(root)
	canonicalTop, topPathErr := filepath.EvalSymlinks(topLevel)
	if rootErr != nil || topPathErr != nil || canonicalRoot != canonicalTop {
		return nil, fmt.Errorf("%s is not the top level of a git repository", root) //nolint:err113 // Public error text.
	}

	args := []string{"-C", root, "grep", "--no-color", "--null", "-n", "-a", "-E"}
	if prefilter.IgnoreCase {
		args = append(args, "-i")
	}
	args = append(args, "-e", prefilter.ERE)
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // Fixed executable and validated config argument.
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		exitError, isExitError := errors.AsType[*exec.ExitError](err)
		if isExitError && exitError.ExitCode() == 1 {
			return []string{}, nil
		}
		code := -1
		if isExitError {
			code = exitError.ExitCode()
		}
		return nil, fmt.Errorf(
			"git grep exited %d: %s",
			code,
			strings.TrimSpace(stderr.String()),
		) //nolint:err113 // Public error text.
	}
	hits, err := ParseGitGrepOutput(decodeGitGrepOutput(output))
	if err != nil {
		return nil, err
	}
	return hits, nil
}
func commandStderr(err error) string {
	exitError, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return ""
	}
	return strings.TrimSpace(string(exitError.Stderr))
}

// Main runs the ref-gate CLI contract without exiting the process.
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--config" || args[1] == "" {
		writeErr := writeOutput(stderr, "Usage: ref-gate --config <path>\n")
		if !writeErr {
			return 2
		}
		return 2
	}
	config, err := LoadConfig(args[1])
	if err != nil {
		if !writeOutput(stderr, "ref-gate: %s\n", err) {
			return 2
		}
		return 2
	}
	root, err := rootPath(ctx, env("GATE_ROOT"))
	if err != nil {
		if !writeOutput(stderr, "ref-gate: %s\n", err) {
			return 2
		}
		return 2
	}
	return runOnce(ctx, root, config, GitGrep, stdout, stderr)
}
