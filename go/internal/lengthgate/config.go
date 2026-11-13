// SPDX-License-Identifier: MIT OR Apache-2.0

// Package lengthgate ratchets comment block and document prose length over a git diff.
//
//nolint:err113,staticcheck // Error text keeps the TypeScript contract wording.
package lengthgate

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/RigelBuild/meissa/go/commentscan"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
	"github.com/bmatcuk/doublestar/v4"
)

// LoadConfig reads and validates a length gate config file.
func LoadConfig(path string) (model.Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The CLI reads the caller-supplied config path.
	if err != nil {
		return model.Config{}, fmt.Errorf("Cannot load config %q: %w", path, err)
	}
	var parsed any
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		return model.Config{}, fmt.Errorf("Cannot load config %q: %w", path, err)
	}
	root, err := asRecord(parsed, "Config")
	if err != nil {
		return model.Config{}, err
	}
	err = rejectUnknownKeys(root, []string{"extensions", "exclude", "docs"}, "config")
	if err != nil {
		return model.Config{}, err
	}
	extensions, err := readStringArray(root["extensions"], "extensions")
	if err != nil {
		return model.Config{}, err
	}
	for _, extension := range extensions {
		_, ok := commentscan.ResidueScannerFor(extension)
		if !ok {
			return model.Config{}, fmt.Errorf("extensions includes %q but no scanner supports it", extension)
		}
	}
	docs, ok := root["docs"].([]any)
	if !ok {
		return model.Config{}, errors.New("docs must be an array of document classes")
	}
	exclude, err := readGlobArray(root["exclude"], "exclude")
	if err != nil {
		return model.Config{}, err
	}
	classes := make([]model.DocClass, 0, len(docs))
	for index, value := range docs {
		var class model.DocClass
		class, err = readDocClass(value, index)
		if err != nil {
			return model.Config{}, err
		}
		classes = append(classes, class)
	}
	return model.Config{Extensions: extensions, Exclude: exclude, Docs: classes}, nil
}

func asRecord(value any, label string) (map[string]any, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return record, nil
}

// rejectUnknownKeys fails on the first key, in sorted order, that is not allowed.
func rejectUnknownKeys(record map[string]any, allowed []string, label string) error {
	for _, key := range slices.Sorted(maps.Keys(record)) {
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("Unknown key %q in %s", key, label)
		}
	}
	return nil
}

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

// readGlobArray reads an array of glob patterns and rejects any pattern doublestar cannot parse, so
// matching later needs no error path.
func readGlobArray(value any, label string) ([]string, error) {
	globs, err := readStringArray(value, label)
	if err != nil {
		return nil, err
	}
	for index, glob := range globs {
		if !doublestar.ValidatePattern(glob) {
			return nil, fmt.Errorf("%s[%d] is not a valid glob: %q", label, index, glob)
		}
	}
	return globs, nil
}

func readDocClass(value any, index int) (model.DocClass, error) {
	label := fmt.Sprintf("docs[%d]", index)
	record, err := asRecord(value, label)
	if err != nil {
		return model.DocClass{}, err
	}
	err = rejectUnknownKeys(record, []string{"name", "include", "exclude", "target", "markerAllowed"}, label)
	if err != nil {
		return model.DocClass{}, err
	}
	name, ok := record["name"].(string)
	if !ok {
		return model.DocClass{}, fmt.Errorf("%s.name must be a string", label)
	}
	target, ok := record["target"].(float64)
	if !ok {
		return model.DocClass{}, fmt.Errorf("%s.target must be a finite number", label)
	}
	markerAllowed, ok := record["markerAllowed"].(bool)
	if !ok {
		return model.DocClass{}, fmt.Errorf("%s.markerAllowed must be a boolean", label)
	}
	include, err := readGlobArray(record["include"], label+".include")
	if err != nil {
		return model.DocClass{}, err
	}
	exclude, err := readGlobArray(record["exclude"], label+".exclude")
	if err != nil {
		return model.DocClass{}, err
	}
	return model.DocClass{
		Name:          name,
		Include:       include,
		Exclude:       exclude,
		Target:        target,
		MarkerAllowed: markerAllowed,
	}, nil
}
