// SPDX-License-Identifier: BSD-3-Clause

// Package linediff aligns two line slices in the order jsdiff's diffArrays does.
//
// It ports jsdiff 9.0.0 (diff/base.js and diff/array.js), BSD-3-Clause,
// Copyright (c) 2009-2015 Kevin Decker. Ties break the way upstream breaks them,
// because length-gate pairs comment lines by this alignment.
package linediff

import (
	"math"
	"slices"
)

// Op is one run of an alignment: '=' for common lines, '+' for lines only in
// after, and '-' for lines only in before.
type Op struct {
	Kind  byte
	Count int
}

// component is one run on an edit path, linked to the run before it.
type component struct {
	count   int
	added   bool
	removed bool
	prev    *component
}

// edgePath is the furthest point reached on one diagonal of the edit graph.
type edgePath struct {
	oldPos int
	last   *component
}

// search is the state of one Myers search.
type search struct {
	oldTokens []string
	newTokens []string
	best      map[int]*edgePath
	minDiag   int
	maxDiag   int
}

// Lines returns the alignment of before and after as ordered runs.
func Lines(before, after []string) []Op {
	s := &search{
		oldTokens: before,
		newTokens: after,
		best:      map[int]*edgePath{},
		minDiag:   math.MinInt,
		maxDiag:   math.MaxInt,
	}
	seed := &edgePath{oldPos: -1, last: nil}
	newPos := s.extractCommon(seed, 0)
	if seed.oldPos+1 >= len(before) && newPos+1 >= len(after) {
		return build(seed.last)
	}
	s.best[0] = seed
	for editLength := 1; editLength <= len(before)+len(after); editLength++ {
		if ops, done := s.step(editLength); done {
			return ops
		}
	}
	// Myers reaches the end within len(before)+len(after) edits, so this is unreachable.
	return nil
}

// step checks every diagonal reachable in editLength edits. It reports the
// alignment once a path reaches the end of both sides.
func (s *search) step(editLength int) ([]Op, bool) {
	oldLen, newLen := len(s.oldTokens), len(s.newTokens)
	for diagonal := max(s.minDiag, -editLength); diagonal <= min(s.maxDiag, editLength); diagonal += 2 {
		removePath := s.best[diagonal-1]
		addPath := s.best[diagonal+1]
		delete(s.best, diagonal-1)
		canAdd := false
		if addPath != nil {
			addPathNewPos := addPath.oldPos - diagonal
			canAdd = addPathNewPos >= 0 && addPathNewPos < newLen
		}
		canRemove := removePath != nil && removePath.oldPos+1 < oldLen
		if !canAdd && !canRemove {
			delete(s.best, diagonal)
			continue
		}
		var base *edgePath
		if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
			base = addToPath(addPath, true, false, 0)
		} else {
			base = addToPath(removePath, false, true, 1)
		}
		newPos := s.extractCommon(base, diagonal)
		if base.oldPos+1 >= oldLen && newPos+1 >= newLen {
			return build(base.last), true
		}
		s.best[diagonal] = base
		if base.oldPos+1 >= oldLen {
			s.maxDiag = min(s.maxDiag, diagonal-1)
		}
		if newPos+1 >= newLen {
			s.minDiag = max(s.minDiag, diagonal+1)
		}
	}
	return nil, false
}

// extractCommon follows equal lines from p along diagonal and returns the new
// position in after.
func (s *search) extractCommon(p *edgePath, diagonal int) int {
	oldPos := p.oldPos
	newPos := oldPos - diagonal
	common := 0
	for newPos+1 < len(s.newTokens) && oldPos+1 < len(s.oldTokens) &&
		s.oldTokens[oldPos+1] == s.newTokens[newPos+1] {
		newPos++
		oldPos++
		common++
	}
	if common > 0 {
		p.last = &component{count: common, added: false, removed: false, prev: p.last}
	}
	p.oldPos = oldPos
	return newPos
}

// addToPath extends p by one run. A run of the same kind as the last one grows
// that run, as jsdiff's addToPath does.
func addToPath(p *edgePath, added, removed bool, oldPosInc int) *edgePath {
	last := p.last
	if last != nil && last.added == added && last.removed == removed {
		return &edgePath{
			oldPos: p.oldPos + oldPosInc,
			last:   &component{count: last.count + 1, added: added, removed: removed, prev: last.prev},
		}
	}
	return &edgePath{
		oldPos: p.oldPos + oldPosInc,
		last:   &component{count: 1, added: added, removed: removed, prev: last},
	}
}

// build turns a linked edit path into runs in source order.
func build(last *component) []Op {
	var ops []Op
	for c := last; c != nil; c = c.prev {
		ops = append(ops, Op{Kind: kindOf(c), Count: c.count})
	}
	slices.Reverse(ops)
	return ops
}

func kindOf(c *component) byte {
	if c.removed {
		return '-'
	}
	if c.added {
		return '+'
	}
	return '='
}
