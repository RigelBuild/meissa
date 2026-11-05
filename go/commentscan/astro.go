// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// Segment is one language region in an Astro file.
type Segment struct {
	Lang      *LangConfig
	StartLine int
	Lines     []string
}

type astroWalk struct {
	segments  []Segment
	residues  []string
	ambiguous bool
}

var (
	scriptOpen    = regexp.MustCompile(`^` + jsSpace + `*<script(` + jsSpace + `[^>]*)?>` + jsSpace + `*$`)
	styleOpen     = regexp.MustCompile(`^` + jsSpace + `*<style(` + jsSpace + `[^>]*)?>` + jsSpace + `*$`)
	scriptClose   = regexp.MustCompile(`^` + jsSpace + `*</script>` + jsSpace + `*$`)
	styleClose    = regexp.MustCompile(`^` + jsSpace + `*</style>` + jsSpace + `*$`)
	islandMention = regexp.MustCompile(`<((script|style))\b`)
	markupExpr    = regexp.MustCompile(`\{[^}]*<[A-Za-z/]`)
	exprOpen      = regexp.MustCompile(`\{[^}]*$`)
	markupLine    = regexp.MustCompile(`<[A-Za-z/]`)
	fence         = regexp.MustCompile(`^---` + jsSpace + `*$`)
)

func markupRegion(lines []string, from int) ([]string, int, bool) {
	first := lines[from]
	if !markupExpr.MatchString(first) && !exprOpen.MatchString(first) {
		return nil, 0, false
	}
	depth, markup := 0, markupExpr.MatchString(first)
	var quote byte
	for k := from; k < len(lines); k++ {
		line := lines[k]
		if k > from && quote == 0 && markupLine.MatchString(line) {
			markup = true
		}
		end := -1
		for j := 0; j < len(line); j++ {
			ch := line[j]
			if !markup && quote == 0 && ch == '/' && j+1 < len(line) && line[j+1] == '/' {
				break
			}
			if quote != 0 {
				if ch == '\\' {
					j++
					continue
				}
				if ch == quote {
					quote = 0
				}
				continue
			}
			if ch == '\'' || ch == '"' || ch == '`' {
				quote = ch
				continue
			}
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth <= 0 && end < 0 {
					end = j + 1
				}
			}
		}
		if depth <= 0 && quote == 0 {
			if end < 0 {
				end = len(line)
			}
			return lines[from : k+1], end, markup
		}
	}
	return lines[from:], len(lines[len(lines)-1]), markup
}

func oneLineIsland(text string) bool {
	for i := range text {
		if text[i] != '<' {
			continue
		}
		name := ""
		for _, candidate := range []string{"script", "style"} {
			if strings.HasPrefix(text[i+1:], candidate) {
				name = candidate
				break
			}
		}
		if name == "" {
			continue
		}
		nameEnd := i + 1 + len(name)
		if nameEnd < len(text) && text[nameEnd] != '>' {
			r, _ := utf8.DecodeRuneInString(text[nameEnd:])
			if !jstext.IsSpace(r) {
				continue
			}
			for nameEnd < len(text) && text[nameEnd] != '>' {
				_, size := utf8.DecodeRuneInString(text[nameEnd:])
				nameEnd += size
			}
		}
		if nameEnd >= len(text) || text[nameEnd] != '>' {
			continue
		}
		bodyStart := nameEnd + 1
		bodyTag := strings.IndexByte(text[bodyStart:], '<')
		if bodyTag < 0 {
			continue
		}
		if strings.HasPrefix(text[bodyStart+bodyTag:], "</"+name+">") {
			return true
		}
	}
	return false
}

func scanMarkupRegion(region []string, end int) ([]string, bool) {
	joined := strings.Join(region, "\n")
	parsed := ScanJSX(joined)
	if !parsed.Ambiguous && len(parsed.Residues) == len(region) {
		return parsed.Residues, true
	}
	last := len(region) - 1
	tail := region[last][end:]
	if jstext.Trim(tail) == "" {
		return nil, false
	}
	body := append([]string(nil), region[:last]...)
	body = append(body, region[last][:end])
	parsed = ScanJSX(strings.Join(body, "\n"))
	if parsed.Ambiguous || len(parsed.Residues) != len(body) {
		return nil, false
	}
	tailScan := scanLine(langByID()[LanguageHTML], scanState{stack: []scanFrame{}}, tail)
	if len(tailScan.state.stack) > 0 || tailScan.underflow {
		return nil, false
	}
	parsed.Residues[last] = trimTrailing(parsed.Residues[last] + tailScan.residue)
	return parsed.Residues, true
}

func scanAstroLines(lang *LangConfig, lines []string) completeScan {
	result := completeScan{residues: make([]string, 0, len(lines)), underflow: false, unclosed: false, malformed: false}
	state := scanState{stack: []scanFrame{}}
	for _, line := range lines {
		scan := scanLine(lang, state, line)
		state = scan.state
		result.residues = append(result.residues, scan.residue)
		result.underflow = result.underflow || scan.underflow
		result.malformed = result.malformed || scan.malformed
	}
	for _, frame := range state.stack {
		if frame.kind != frameIndent {
			result.unclosed = true
			break
		}
	}
	return result
}

func scanAstroWalk(text string) astroWalk {
	lines := strings.Split(text, "\n")
	walk := astroWalk{segments: make([]Segment, 0), residues: make([]string, 0, len(lines)), ambiguous: false}
	i := 0
	if len(lines) > 0 && fence.MatchString(lines[0]) {
		fenceEnd := -1
		for k := 1; k < len(lines); k++ {
			if fence.MatchString(lines[k]) {
				fenceEnd = k
				break
			}
		}
		if fenceEnd < 0 {
			return astroWalk{segments: nil, residues: nil, ambiguous: true}
		}
		walk.segments = append(walk.segments, Segment{
			Lang: langByID()[LanguageHTML], StartLine: 0, Lines: []string{lines[0]},
		})
		walk.residues = append(walk.residues, lines[0])
		body := lines[1:fenceEnd]
		scan := scanAstroLines(langByID()[LanguageTS], body)
		walk.segments = append(walk.segments, Segment{Lang: langByID()[LanguageTS], StartLine: 1, Lines: body})
		walk.residues = append(walk.residues, scan.residues...)
		if scan.underflow || scan.unclosed {
			walk.ambiguous = true
		}
		walk.segments = append(walk.segments, Segment{
			Lang: langByID()[LanguageHTML], StartLine: fenceEnd, Lines: []string{lines[fenceEnd]},
		})
		walk.residues = append(walk.residues, lines[fenceEnd])
		i = fenceEnd + 1
	}

	templateStart, template := i, make([]string, 0)
	state := scanState{stack: []scanFrame{}}
	closeTemplate := func() {
		if len(template) == 0 {
			return
		}
		walk.segments = append(walk.segments, Segment{
			Lang: langByID()[LanguageHTML], StartLine: templateStart, Lines: template,
		})
		template = nil
	}
	for i < len(lines) {
		line := lines[i]
		if len(state.stack) == 0 {
			region, end, markup := markupRegion(lines, i)
			if markup {
				parsed, ok := scanMarkupRegion(region, end)
				if !ok {
					return astroWalk{segments: nil, residues: nil, ambiguous: true}
				}
				template = append(template, region...)
				walk.residues = append(walk.residues, parsed...)
				i += len(region)
				continue
			}
		}
		scan := scanLine(langByID()[LanguageHTML], state, line)
		if scan.underflow {
			walk.ambiguous = true
		}
		residue := scan.residue
		isScript := len(state.stack) == 0 && scriptOpen.MatchString(residue)
		isStyle := len(state.stack) == 0 && styleOpen.MatchString(residue)
		if !isScript && !isStyle {
			if len(state.stack) == 0 && islandMention.MatchString(residue) && !oneLineIsland(residue) {
				return astroWalk{segments: nil, residues: nil, ambiguous: true}
			}
			state = scan.state
			template = append(template, line)
			walk.residues = append(walk.residues, residue)
			i++
			continue
		}
		closeRe := scriptClose
		lang := langByID()[LanguageTS]
		if isStyle {
			closeRe, lang = styleClose, langByID()[LanguageCSS]
		}
		scriptEnd := -1
		for k := i + 1; k < len(lines); k++ {
			if closeRe.MatchString(lines[k]) {
				scriptEnd = k
				break
			}
		}
		if scriptEnd < 0 {
			return astroWalk{segments: nil, residues: nil, ambiguous: true}
		}
		template = append(template, line)
		walk.residues = append(walk.residues, residue)
		closeTemplate()
		body := lines[i+1 : scriptEnd]
		bodyScan := scanAstroLines(lang, body)
		walk.segments = append(walk.segments, Segment{Lang: lang, StartLine: i + 1, Lines: body})
		walk.residues = append(walk.residues, bodyScan.residues...)
		if bodyScan.underflow || bodyScan.unclosed {
			walk.ambiguous = true
		}
		templateStart = scriptEnd
		template = append(template[:0], lines[scriptEnd])
		walk.residues = append(walk.residues, lines[scriptEnd])
		state = scanState{stack: []scanFrame{}}
		i = scriptEnd + 1
	}
	closeTemplate()
	if len(state.stack) > 0 {
		walk.ambiguous = true
	}
	return walk
}

// SegmentAstro returns the language regions in source order.
func SegmentAstro(text string) ([]Segment, bool) {
	walk := scanAstroWalk(text)
	if len(walk.segments) == 0 && walk.ambiguous {
		return nil, false
	}
	return walk.segments, true
}

// ScanAstro scans Astro frontmatter, template text and script/style islands.
func ScanAstro(text string) Residues {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	walk := scanAstroWalk(text)
	if walk.ambiguous {
		return Residues{Residues: strings.Split(text, "\n"), Ambiguous: true}
	}
	return Residues{Residues: walk.residues, Ambiguous: false}
}
