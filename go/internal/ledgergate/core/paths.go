// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// DesignPathKind classifies one repository path under the designs root.
type DesignPathKind string

const (
	// PathDecision is a decision file in the configured decisions tree.
	PathDecision DesignPathKind = "decision"
	// PathDecisionsReadme is the README of the decisions tree.
	PathDecisionsReadme DesignPathKind = "decisions-readme"
	// PathLegacyLedger is a retired DECISIONS.md file.
	PathLegacyLedger DesignPathKind = "legacy-ledger"
	// PathMisplaced is a decision-looking file outside the configured layout.
	PathMisplaced DesignPathKind = "misplaced"
	// PathOther is any other design path.
	PathOther DesignPathKind = "other"
)

// StrayPath is a design file that breaks the decision layout.
// Kind is PathLegacyLedger or PathMisplaced.
type StrayPath struct {
	Path string
	Kind DesignPathKind
}

var (
	// JS `.` also stops at \r, U+2028 and U+2029; Go's stops only at \n.
	decisionFileRE   = regexp.MustCompile(`^DL-[^\r\n\x{2028}\x{2029}]*\.md$`)
	decisionNumberRE = regexp.MustCompile(`^DL-[0-9]+\.md$`)
)

// ClassifyDesignPath returns the layout kind of one repository path.
func ClassifyDesignPath(file string, cfg Config) DesignPathKind {
	root := cfg.DesignsRoot + "/"
	if !strings.HasPrefix(file, root) {
		return PathOther
	}
	base := path.Base(file)
	if base == "DECISIONS.md" {
		return PathLegacyLedger
	}
	relative := strings.Split(file[len(root):], "/")
	decisionIndex := 1
	if cfg.SurfaceDepth == 0 {
		decisionIndex = 0
	}
	if decisionIndex < len(relative) && relative[decisionIndex] == "decisions" {
		decisionPath := relative[decisionIndex+1:]
		if len(decisionPath) == 1 && decisionPath[0] == "README.md" {
			return PathDecisionsReadme
		}
		if len(decisionPath) == 2 && decisionPath[0] != "" && decisionFileRE.MatchString(base) {
			return PathDecision
		}
		return PathMisplaced
	}
	if cfg.SurfaceDepth == 1 && len(relative) >= 2 && relative[1] == "DECISIONS.md" {
		return PathLegacyLedger
	}
	if decisionNumberRE.MatchString(base) {
		return PathMisplaced
	}
	return PathOther
}

// TouchesRecord reports whether a changed path is a governed design record.
// The decision tree and a surface README are not records.
func TouchesRecord(file string, cfg Config) bool {
	if !strings.HasSuffix(file, ".md") {
		return false
	}
	root := cfg.DesignsRoot + "/"
	if !strings.HasPrefix(file, root) {
		return false
	}
	relative := strings.Split(file[len(root):], "/")
	if relative[0] == "decisions" {
		return false
	}
	if cfg.SurfaceDepth == 0 {
		return slices.Contains(cfg.GovernedRoots, relative[0])
	}
	isRecord := len(relative) >= 2 && relative[1] != "decisions"
	isSurfaceReadme := len(relative) == 2 && relative[1] == "README.md"
	return isRecord && !isSurfaceReadme
}

// resolveRecordRelative joins a Status pointer onto its record's directory.
// It reports false when the join climbs above the designs root.
func resolveRecordRelative(recordRelPath, pointer string) (string, bool) {
	joined := path.Join(path.Dir(recordRelPath), pointer)
	if strings.HasPrefix(joined, "..") {
		return "", false
	}
	return joined, true
}
