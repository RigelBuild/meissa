// SPDX-License-Identifier: MIT OR Apache-2.0

// Package model holds the length gate's data types: its config, document classes, findings and notes.
// It holds no logic, so the packages that read, judge and print findings can share one set of types.
package model

// DocClass is one document ratchet class: the files it covers and its prose-word target.
type DocClass struct {
	Name          string
	Include       []string
	Exclude       []string
	Target        float64
	MarkerAllowed bool
}

// Config is the length gate policy read from a config file.
type Config struct {
	Extensions []string
	Exclude    []string
	Docs       []DocClass
}

// Finding is one ratchet violation reported for a file.
type Finding interface{ isFinding() }

// CommentFinding reports a comment block that grew past its base length.
type CommentFinding struct {
	Path      string
	Line      int
	Lines     int
	BaseLines int
}

// DocFinding reports a document whose prose grew past its class target.
type DocFinding struct {
	Path      string
	DocClass  string
	Words     int
	BaseWords int
	Target    float64
}

// BadMarkerFinding reports a LENGTH marker that is malformed or not allowed.
type BadMarkerFinding struct {
	Path   string
	Line   int
	Reason string
}

// UnscannableFinding reports a file no scanner could place comments in.
type UnscannableFinding struct {
	Path string
}

func (CommentFinding) isFinding()     {}
func (DocFinding) isFinding()         {}
func (BadMarkerFinding) isFinding()   {}
func (UnscannableFinding) isFinding() {}

// Note reports a LENGTH marker that is not needed because its block fits the ceiling.
type Note struct {
	Path string
	Line int
}
