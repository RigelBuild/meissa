// SPDX-License-Identifier: MIT OR Apache-2.0

package run

import (
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/ledgergate/claim"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
)

// Deps is the I/O the runner reads. The git and filesystem implementation
// lives outside this package. ListDesignFiles returns the files under the
// configured designs root, sorted.
type Deps interface {
	Root() string
	BaseRef() string
	ReadText(file string) (text string, found bool, err error)
	ListDesignFiles(ctx context.Context) ([]string, error)
	ReadMergeBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error)
	ReadBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error)
	Changed(ctx context.Context) (core.Changed, error)
	FetchClaims(ctx context.Context, lane string, ids []string) (map[string]claim.Verdict, error)
	PullRequestDetails(ctx context.Context) (mo.Option[core.PullRequestDetails], error)
}

// Once runs the design-ledger gate and returns the exit code: 0 pass,
// 1 findings, 2 usage, config or I/O error. The OK line goes to stdout; findings
// and errors go to stderr.
func Once(ctx context.Context, deps Deps, cfg core.Config, stdout, stderr io.Writer) int {
	changed, err := deps.Changed(ctx)
	if err != nil {
		sayf(stderr, "design-ledger-gate: %s\n", err)
		return 2
	}
	run := &gateRun{
		deps:         deps,
		root:         deps.Root(),
		cfg:          cfg,
		baseRef:      deps.BaseRef(),
		stdout:       stdout,
		stderr:       stderr,
		paths:        nil,
		contents:     map[string]string{},
		fenceSeen:    map[string]bool{},
		records:      nil,
		corpus:       core.DecisionCorpus{Rows: nil, Malformed: nil, Strays: nil},
		changed:      changed,
		details:      mo.None[core.PullRequestDetails](),
		violations:   nil,
		citations:    citationTally{seen: 0, checked: 0, unresolvable: 0, repoAmbiguous: 0},
		claimChecked: 0,
		claimLane:    "",
	}
	return run.execute(ctx)
}

// citationTally counts the citation leg's results for the OK line.
type citationTally struct {
	seen, checked, unresolvable, repoAmbiguous int
}

// gateRun holds one Once call's state. A step that hits an I/O error prints
// it and returns false; execute then exits 2.
type gateRun struct {
	deps    Deps
	root    string
	cfg     core.Config
	baseRef string
	stdout  io.Writer
	stderr  io.Writer

	paths        []string
	contents     map[string]string
	fenceSeen    map[string]bool
	records      []core.RecordHeader
	corpus       core.DecisionCorpus
	changed      core.Changed
	details      mo.Option[core.PullRequestDetails]
	violations   []core.Violation
	citations    citationTally
	claimChecked int
	claimLane    string
}

func (g *gateRun) execute(ctx context.Context) int {
	if !g.scanTree(ctx) || !g.readDetails(ctx) {
		return 2
	}
	if !g.evaluateCorpus() {
		return 2
	}
	if !g.checkCitations() || !g.checkRecordLinks() || !g.checkErrata() || !g.checkMainIDs(ctx) {
		return 2
	}
	g.claimChecked = 0
	g.claimLane = claim.Lane(g.changed, g.details)
	if g.cfg.Legs.ClaimCheck {
		checked, violations, err := claim.RunLeg(ctx, g.deps, g.cfg, g.baseRef, g.changed, g.details, g.corpus.Rows)
		if err != nil {
			sayln(g.stderr, err)
			return 2
		}
		g.claimChecked = checked
		g.violations = append(g.violations, violations...)
	}
	return g.report()
}

// scanTree lists the tree, reads the decision and record files, and builds the
// corpus. It reports unknown depth-one surfaces.
func (g *gateRun) scanTree(ctx context.Context) bool {
	paths, err := g.deps.ListDesignFiles(ctx)
	if err != nil {
		return g.failf("design-ledger-gate: cannot read the tree at %s\n%s", g.root, err)
	}
	// A wrong root or designsRoot lists nothing; never pass a gate that saw no files.
	if len(paths) == 0 {
		return g.failf("design-ledger-gate: no files under %s", g.cfg.DesignsRoot)
	}
	g.paths = paths
	var decisions, recordPaths []string
	var strays []core.StrayPath
	for _, file := range paths {
		kind := core.ClassifyDesignPath(file, g.cfg)
		if kind == core.PathDecision {
			decisions = append(decisions, file)
		}
		if kind == core.PathLegacyLedger || kind == core.PathMisplaced {
			strays = append(strays, core.StrayPath{Path: file, Kind: kind})
		}
		if core.TouchesRecord(file, g.cfg) {
			recordPaths = append(recordPaths, file)
		}
	}
	decisionSet := stringSet(decisions)
	recordSet := stringSet(recordPaths)
	var decisionFiles []core.File
	for _, file := range sortedUnion(decisions, recordPaths) {
		text, found, readErr := g.deps.ReadText(file)
		if readErr != nil {
			return g.failf("design-ledger-gate: cannot read the tree at %s\n%s", g.root, readErr)
		}
		if !found {
			continue
		}
		g.contents[file] = text
		if decisionSet[file] {
			decisionFiles = append(decisionFiles, core.File{Path: file, Text: text})
			g.violations = append(g.violations, core.ConflictMarkerViolations(file, text)...)
		}
		if recordSet[file] {
			g.records = append(g.records, core.ParseRecordHeader(file, text))
			g.violations = append(g.violations, core.ConflictMarkerViolations(file, text)...)
		}
	}
	g.corpus = core.BuildDecisionCorpus(decisionFiles, strays)
	g.checkSurfaces(paths)
	return true
}

// checkSurfaces reports each depth-one surface that the configured list omits.
func (g *gateRun) checkSurfaces(paths []string) {
	if g.cfg.SurfaceDepth != 1 || g.cfg.Surfaces == nil {
		return
	}
	known := stringSet(g.cfg.Surfaces)
	for _, surface := range discoveredSurfaces(paths, g.cfg.DesignsRoot) {
		if !known[surface] {
			g.add(g.cfg.DesignsRoot+"/"+surface, 0, "unknown design surface: "+surface)
		}
	}
}

// discoveredSurfaces returns the first path segment under the designs root for
// each file that has at least one segment below it, in listing order.
func discoveredSurfaces(paths []string, designsRoot string) []string {
	prefix := designsRoot + "/"
	seen := map[string]bool{}
	var surfaces []string
	for _, file := range paths {
		if !strings.HasPrefix(file, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(file, prefix), "/")
		if len(parts) >= 2 && parts[0] != "" && !seen[parts[0]] {
			seen[parts[0]] = true
			surfaces = append(surfaces, parts[0])
		}
	}
	return surfaces
}

// readDetails reads the PR details when a leg or the PR body needs them.
func (g *gateRun) readDetails(ctx context.Context) bool {
	_, hasBody := g.changed.Body.Get()
	if !g.cfg.Legs.MainIDs && !g.cfg.Legs.ClaimCheck && !hasBody {
		return true
	}
	details, err := g.deps.PullRequestDetails(ctx)
	if err != nil {
		return g.failf("design-ledger-gate: cannot read pull request details: %s", err)
	}
	g.details = details
	return true
}

// evaluateCorpus runs the invariant checks and records their findings.
func (g *gateRun) evaluateCorpus() bool {
	var readErr error
	readRecord := func(file string) (core.RecordContent, bool) {
		text, found, err := g.deps.ReadText(file)
		if err != nil {
			if readErr == nil {
				readErr = fmt.Errorf("cannot read %s: %w", file, err)
			}
			return core.RecordContent{Headings: nil, SizeBytes: 0}, false
		}
		if !found {
			return core.RecordContent{Headings: nil, SizeBytes: 0}, false
		}
		return core.RecordContentFromText(text), true
	}
	g.violations = append(g.violations,
		core.Evaluate(g.corpus, g.records, g.changed, readRecord, g.cfg, g.details)...)
	if readErr != nil {
		return g.failWith(readErr)
	}
	return true
}

// report prints the OK line, or every finding, and returns the exit code.
func (g *gateRun) report() int {
	if len(g.violations) == 0 {
		claims := "claim checks off"
		if g.cfg.Legs.ClaimCheck {
			claims = fmt.Sprintf("claims: %d new id(s) checked for %s", g.claimChecked, g.claimLane)
		}
		sayf(g.stdout, "design-ledger-gate: OK — %d decision file(s), %s; %s; %s; %s.\n",
			len(g.corpus.Rows), g.recordStatus(), g.prStatus(), g.citationStatus(), claims)
		return 0
	}
	if g.cfg.Legs.ClaimCheck && g.claimChecked > 0 {
		sayf(g.stdout, "claims: %d new id(s) checked for %s\n", g.claimChecked, g.claimLane)
	}
	sayln(g.stderr, "")
	sayf(g.stderr, "design-ledger-gate: %d violation(s):\n", len(g.violations))
	for _, v := range g.violations {
		if v.Line > 0 {
			sayf(g.stderr, "  %s:%d: %s\n", v.File, v.Line, v.Message)
		} else {
			sayf(g.stderr, "  %s: %s\n", v.File, v.Message)
		}
	}
	sayln(g.stderr, "")
	if doc, ok := g.cfg.RemediationDoc.Get(); ok && doc != "" {
		sayf(g.stderr, "See %s.\n", doc)
	}
	return 1
}

func (g *gateRun) recordStatus() string {
	if _, hasBody := g.changed.Body.Get(); g.cfg.RecordStatusScope == "changed" && !hasBody {
		return "record Status skipped (no PR context)"
	}
	return fmt.Sprintf("%d record(s) status-checked", len(g.records))
}

func (g *gateRun) prStatus() string {
	if _, hasBody := g.changed.Body.Get(); !hasBody {
		return "PR checks skipped (no PR context)"
	}
	return "PR checks enabled"
}

func (g *gateRun) citationStatus() string {
	if !g.cfg.Legs.Citations {
		return "citation checks off"
	}
	return fmt.Sprintf("%d/%d citation(s) (%d unresolvable, %d repo-ambiguous)",
		g.citations.checked, g.citations.seen, g.citations.unresolvable, g.citations.repoAmbiguous)
}

// checkMainIDs reports a new decision id that the base branch tip already uses.
func (g *gateRun) checkMainIDs(ctx context.Context) bool {
	_, hasBody := g.changed.Body.Get()
	if !g.cfg.Legs.MainIDs || !hasBody || len(g.changed.Files) == 0 {
		return true
	}
	mergeBase, err := g.deps.ReadMergeBaseDecisionPaths(ctx, g.baseRef)
	if err != nil {
		return g.failf("design-ledger-gate: cannot read base decision listing: %s", err)
	}
	base, err := g.deps.ReadBaseDecisionPaths(ctx, g.baseRef)
	if err != nil {
		return g.failf("design-ledger-gate: cannot read base decision listing: %s", err)
	}
	mergeBaseIDs := decisionIDSet(mergeBase)
	baseTipIDs := decisionIDSet(base)
	for _, row := range g.corpus.Rows {
		if slices.Contains(g.changed.Files, row.Path) && !mergeBaseIDs[row.ID] && baseTipIDs[row.ID] {
			g.add(row.Path, core.KeyLineID, "decision ID already exists on base branch tip")
		}
	}
	return true
}

// designMarkdown lists the Markdown files under the designs root, in listing order.
func (g *gateRun) designMarkdown() []string {
	prefix := g.cfg.DesignsRoot + "/"
	var files []string
	for _, file := range g.paths {
		if strings.HasPrefix(file, prefix) && strings.HasSuffix(file, ".md") {
			files = append(files, file)
		}
	}
	return files
}

// text returns a file's text from the tree scan when it was read, else from the repository.
func (g *gateRun) text(file string) (string, bool, error) {
	if text, ok := g.contents[file]; ok {
		return text, true, nil
	}
	return g.deps.ReadText(file) //nolint:wrapcheck // Preserve the dependency's detailed read error.
}

// reportUnclosedFence adds one finding per file, however many legs read it.
func (g *gateRun) reportUnclosedFence(file, text string) {
	if g.fenceSeen[file] {
		return
	}
	g.fenceSeen[file] = true
	if line := core.ScanMarkdown(text).OpenFenceLine; line != 0 {
		g.add(file, line, "unclosed Markdown fence")
	}
}

func (g *gateRun) add(file string, line int, message string) {
	g.violations = append(g.violations, core.Violation{File: file, Line: line, Message: message})
}

// failf prints a usage, config or I/O error and returns false.
func (g *gateRun) failf(format string, args ...any) bool {
	sayf(g.stderr, format+"\n", args...)
	return false
}

// failWith prints an error that an unguarded read raised, with the CLI prefix.
func (g *gateRun) failWith(err error) bool {
	return g.failf("design-ledger-gate: %s", err)
}

// withinRepo reports whether a repository-relative path stays inside the repository.
func withinRepo(file string) bool {
	if path.IsAbs(file) {
		return false
	}
	clean := path.Clean(file)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// decisionIDSet maps each decision path to its id, the file name without ".md".
func decisionIDSet(paths []string) map[string]bool {
	ids := make(map[string]bool, len(paths))
	for _, file := range paths {
		ids[strings.TrimSuffix(path.Base(file), ".md")] = true
	}
	return ids
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// sortedUnion returns the distinct values of both lists, sorted.
func sortedUnion(first, second []string) []string {
	seen := make(map[string]bool, len(first)+len(second))
	var out []string
	for _, value := range slices.Concat(first, second) {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

// sayf and sayln write gate output. The exit code is the contract, so a failed
// write to stdout or stderr cannot change the outcome and is dropped.
func sayf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func sayln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
