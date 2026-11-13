// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/ledger"
)

// Front-matter line of each decision key. Findings point at these lines.
const (
	KeyLineID     = 2
	keyLineStatus = 4
	keyLineRecord = 5
)

// evaluator holds one Evaluate call's inputs and findings.
type evaluator struct {
	corpus     DecisionCorpus
	records    []RecordHeader
	changed    Changed
	readRecord func(path string) (RecordContent, bool)
	cfg        Config
	exempt     bool
	violations []Violation
}

// Evaluate runs every design-ledger invariant over one corpus and one PR.
// readRecord reports false when a repository path does not resolve to a Record.
func Evaluate(
	corpus DecisionCorpus,
	records []RecordHeader,
	changed Changed,
	readRecord func(path string) (RecordContent, bool),
	cfg Config,
	details mo.Option[PullRequestDetails],
) []Violation {
	e := &evaluator{
		corpus:     corpus,
		records:    records,
		changed:    changed,
		readRecord: readRecord,
		cfg:        cfg,
		exempt:     exemptFor(changed, details, cfg),
		violations: []Violation{},
	}
	e.reportStrays()
	byID, order := e.checkRows()
	e.checkCycles(byID, order)
	e.checkCompleteness()
	e.checkRecordStatuses(rowsByRecord(corpus.Rows))
	e.checkTouchCoupling()
	return e.violations
}

func (e *evaluator) add(file string, line int, message string) {
	e.violations = append(e.violations, Violation{File: file, Line: line, Message: message})
}

func (e *evaluator) reportStrays() {
	for _, stray := range e.corpus.Strays {
		message := "misplaced design file: decision files must follow the configured layout"
		if stray.Kind == PathLegacyLedger {
			message = "DECISIONS.md is retired: decision files belong in the configured decisions tree"
		}
		e.add(stray.Path, 0, message)
	}
	for _, malformed := range e.corpus.Malformed {
		e.add(malformed.Path, malformed.Line, "malformed decision file: "+malformed.Reason)
	}
}

// checkRows reports duplicate ids and per-row link and area faults. It returns
// the first row for each id and the ids in first-seen order.
func (e *evaluator) checkRows() (map[string]ledger.DecisionRow, []string) {
	rows := e.corpus.Rows
	if len(rows) == 0 && slices.ContainsFunc(e.records, e.isRecord) {
		e.add(e.cfg.DesignsRoot+"/decisions", 0, "no valid decision files were found")
	}
	byID := make(map[string]ledger.DecisionRow, len(rows))
	var order []string
	for _, row := range rows {
		first, seen := byID[row.ID]
		if !seen {
			byID[row.ID] = row
			order = append(order, row.ID)
			continue
		}
		e.add(row.Path, KeyLineID,
			fmt.Sprintf("%s: duplicate decision id (also defined in %s)", row.ID, first.Path))
	}
	for _, row := range rows {
		e.checkRow(row, byID)
	}
	return byID, order
}

func (e *evaluator) isRecord(header RecordHeader) bool {
	return TouchesRecord(header.Path, e.cfg)
}

func (e *evaluator) checkRow(row ledger.DecisionRow, byID map[string]ledger.DecisionRow) {
	root := e.cfg.DesignsRoot
	inRoot := strings.HasPrefix(row.RecordPath, root+"/")
	var recordParts []string
	if inRoot {
		recordParts = strings.Split(afterRoot(row.RecordPath, root), "/")
	}
	decisionArea := partAt(strings.Split(afterRoot(row.Path, root), "/"), 1)
	recordArea := partAt(recordParts, 0)
	areaMismatch := !inRoot ||
		(e.cfg.SurfaceDepth == 0 && (recordArea == "" || recordArea != decisionArea))
	if areaMismatch {
		e.add(row.Path, keyLineRecord,
			fmt.Sprintf("%s: decision area must match its Record path (%s)", row.ID, row.RecordPath))
	}
	e.checkRowRecordLink(row)
	e.checkRowSupersession(row, byID)
}

func (e *evaluator) checkRowRecordLink(row ledger.DecisionRow) {
	target, found := e.readRecord(row.RecordPath)
	switch {
	case !found:
		e.add(row.Path, keyLineRecord,
			fmt.Sprintf("%s: Record link path does not resolve: %s", row.ID, row.RecordRaw))
	case row.RecordAnchor != "" && !slices.Contains(target.Headings, row.RecordAnchor):
		before, _, _ := strings.Cut(row.RecordRaw, "#")
		e.add(row.Path, keyLineRecord,
			fmt.Sprintf("%s: Record link #anchor not found in %s: #%s", row.ID, before, row.RecordAnchor))
	case row.RecordAnchor == "" && target.SizeBytes > largeRecordBytes:
		e.add(row.Path, keyLineRecord,
			fmt.Sprintf("%s: Record link into a large record must carry a #anchor: %s", row.ID, row.RecordRaw))
	}
}

func (e *evaluator) checkRowSupersession(row ledger.DecisionRow, byID map[string]ledger.DecisionRow) {
	targetID, superseded := supersessionTarget(row.Status)
	if !superseded {
		return
	}
	if targetID == row.ID {
		e.add(row.Path, keyLineStatus, row.ID+": superseded by itself")
		return
	}
	if _, known := byID[targetID]; !known {
		e.add(row.Path, keyLineStatus,
			fmt.Sprintf("%s: Superseded by %s, which is not a decision file", row.ID, targetID))
	}
}

// checkCycles walks each superseded row once per start and reports each cycle
// once, at its lowest id.
func (e *evaluator) checkCycles(byID map[string]ledger.DecisionRow, order []string) {
	reported := map[string]bool{}
	for _, startID := range order {
		start := byID[startID]
		if _, superseded := supersessionTarget(start.Status); !superseded {
			continue
		}
		e.walkSupersession(start, byID, reported)
	}
}

func (e *evaluator) walkSupersession(
	start ledger.DecisionRow,
	byID map[string]ledger.DecisionRow,
	reported map[string]bool,
) {
	var walk []ledger.DecisionRow
	current, present := start, true
	for present {
		node := current
		if index := slices.IndexFunc(walk, func(row ledger.DecisionRow) bool { return row.ID == node.ID }); index != -1 {
			e.reportCycle(walk[index:], node.ID, reported)
			return
		}
		walk = append(walk, node)
		nextID, superseded := supersessionTarget(node.Status)
		if !superseded {
			return
		}
		current, present = byID[nextID]
	}
}

func (e *evaluator) reportCycle(cycle []ledger.DecisionRow, nodeID string, reported map[string]bool) {
	if len(cycle) < 2 {
		return
	}
	ids := make([]string, 0, len(cycle))
	for _, row := range cycle {
		ids = append(ids, row.ID)
	}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	key := strings.Join(sorted, "|")
	if reported[key] {
		return
	}
	reported[key] = true
	anchor := cycle[0]
	for _, row := range cycle[1:] {
		if row.ID < anchor.ID {
			anchor = row
		}
	}
	chain := strings.Join(append(slices.Clone(ids), nodeID), " → ")
	e.add(anchor.Path, keyLineStatus, "supersession cycle: "+chain)
}

// checkCompleteness requires a valid decision for every area or surface that
// holds Records.
func (e *evaluator) checkCompleteness() {
	if e.cfg.SurfaceDepth == 0 {
		e.checkGovernedAreas()
		return
	}
	e.checkSurfaces()
}

func (e *evaluator) checkGovernedAreas() {
	root := e.cfg.DesignsRoot
	areas := e.decisionAreas()
	for _, area := range e.cfg.GovernedRoots {
		prefix := root + "/" + area + "/"
		hasRecords := slices.ContainsFunc(e.records, func(header RecordHeader) bool {
			return TouchesRecord(header.Path, e.cfg) && strings.HasPrefix(header.Path, prefix)
		})
		if hasRecords && !areas[area] {
			e.add(root+"/decisions/"+area, 0,
				fmt.Sprintf("%s/%s/ has records but no valid decision file in decisions/%s/", root, area, area))
		}
	}
}

// decisionAreas returns the area segment of every decision row's path.
func (e *evaluator) decisionAreas() map[string]bool {
	areas := map[string]bool{}
	for _, row := range e.corpus.Rows {
		parts := strings.Split(afterRoot(row.Path, e.cfg.DesignsRoot), "/")
		areas[partAt(parts, e.cfg.SurfaceDepth+1)] = true
	}
	return areas
}

func (e *evaluator) checkSurfaces() {
	root := e.cfg.DesignsRoot
	decisionSurfaces := map[string]bool{}
	for _, row := range e.corpus.Rows {
		decisionSurfaces[surfaceOf(row.Path, root)] = true
	}
	var recordSurfaces []string
	seen := map[string]bool{}
	for _, header := range e.records {
		if !TouchesRecord(header.Path, e.cfg) {
			continue
		}
		surface := surfaceOf(header.Path, root)
		if !seen[surface] {
			seen[surface] = true
			recordSurfaces = append(recordSurfaces, surface)
		}
	}
	for _, surface := range recordSurfaces {
		if !decisionSurfaces[surface] {
			e.add(fmt.Sprintf("%s/%s/decisions", root, surface), 0,
				surface+" has records but no valid decision file")
		}
	}
}

// checkRecordStatuses validates the Status header of each Record in scope.
func (e *evaluator) checkRecordStatuses(rowByRecord map[string]ledger.DecisionRow) {
	changedFiles := map[string]bool{}
	for _, file := range e.changed.Files {
		changedFiles[file] = true
	}
	changedScope := e.cfg.RecordStatusScope == "changed"
	for _, header := range e.records {
		line, hasLine := header.StatusLine.Get()
		if !hasLine || (e.exempt && changedScope) {
			continue
		}
		if changedScope && (e.changed.Body.IsAbsent() || !changedFiles[header.Path]) {
			continue
		}
		e.checkStatus(header, line, rowByRecord)
	}
}

func (e *evaluator) checkStatus(header RecordHeader, line string, rowByRecord map[string]ledger.DecisionRow) {
	value, ok := ParseStatusValue(line)
	if !ok {
		e.add(header.Path, header.Line, "malformed or prohibited `Status:` header")
		return
	}
	switch status := value.(type) {
	case StatusHistorical:
		e.checkHistorical(header)
	case StatusSuperseded:
		e.checkSuperseded(header, status.Path, rowByRecord)
	}
}

func (e *evaluator) checkHistorical(header RecordHeader) {
	if e.cfg.HistoricalChain == nil || slices.Contains(e.cfg.HistoricalChain, header.Path) {
		return
	}
	e.add(header.Path, header.Line,
		"`Status: Historical` but the record is not in the configured historical chain")
}

func (e *evaluator) checkSuperseded(header RecordHeader, pointer string, rowByRecord map[string]ledger.DecisionRow) {
	root := e.cfg.DesignsRoot
	relative := header.Path
	if strings.HasPrefix(header.Path, root+"/") {
		relative = afterRoot(header.Path, root)
	}
	resolved, joined := resolveRecordRelative(relative, pointer)
	resolves := false
	if joined {
		_, resolves = e.readRecord(root + "/" + resolved)
	}
	if !resolves {
		e.add(header.Path, header.Line,
			"Status supersession does not resolve to a record: "+pointer)
	}
	linked, hasLink := rowByRecord[header.Path]
	if !hasLink {
		return
	}
	if _, superseded := supersessionTarget(linked.Status); !superseded {
		e.add(header.Path, header.Line,
			"record Status supersession disagrees with its decision status")
	}
}

// checkTouchCoupling requires a decision change or a Ledger-impact declaration
// when a governed record changes.
func (e *evaluator) checkTouchCoupling() {
	declared := hasLedgerImpact(e.changed.Body.OrEmpty())
	touchedRecord := !e.exempt && slices.ContainsFunc(e.changed.Files, func(file string) bool {
		return TouchesRecord(file, e.cfg)
	})
	touchedDecision := slices.ContainsFunc(e.changed.Files, func(file string) bool {
		return ClassifyDesignPath(file, e.cfg) == PathDecision
	})
	if touchedRecord && !touchedDecision && !declared {
		e.add("(pull request)", 0,
			"PR touches a governed design record without a changed decision file or Ledger-impact declaration")
	}
}

// supersessionTarget returns the DL id that a Superseded decision status names.
func supersessionTarget(status string) (string, bool) {
	match := supersededRE.FindStringSubmatch(status)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// rowsByRecord maps each Record path to the decision row that links it without
// an anchor. A later row for the same path replaces an earlier one.
func rowsByRecord(rows []ledger.DecisionRow) map[string]ledger.DecisionRow {
	byRecord := map[string]ledger.DecisionRow{}
	for _, row := range rows {
		if row.RecordAnchor == "" {
			byRecord[row.RecordPath] = row
		}
	}
	return byRecord
}

// afterRoot drops the designs root and its slash, as JavaScript's slice does.
func afterRoot(file, root string) string {
	start := len(root) + 1
	if start >= len(file) {
		return ""
	}
	return file[start:]
}

// surfaceOf returns the first path segment under the designs root.
func surfaceOf(file, root string) string {
	return partAt(strings.Split(afterRoot(file, root), "/"), 0)
}

func partAt(parts []string, index int) string {
	if index < len(parts) {
		return parts[index]
	}
	return ""
}
