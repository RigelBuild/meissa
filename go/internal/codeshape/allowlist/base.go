// SPDX-License-Identifier: MIT OR Apache-2.0

package allowlist

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"

	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
)

// Schema returns the embedded allowlist schema in a CUE context.
func Schema(ctx *cue.Context) (cue.Value, error) {
	return schemaValue(ctx)
}

// ParseAllowlist decodes an allowlist from CUE bytes.
func ParseAllowlist(ctx *cue.Context, schema cue.Value, data []byte) (model.Allowlist, error) {
	file, err := parser.ParseFile("base allowlist", data)
	if err != nil {
		return model.Allowlist{}, fmt.Errorf("parse base allowlist: %w", err)
	}
	for _, declaration := range file.Decls {
		if _, ok := declaration.(*ast.ImportDecl); ok {
			return model.Allowlist{}, fmt.Errorf("base allowlist: %w", errImportsForbidden)
		}
	}
	value := ctx.BuildFile(file)
	if err := value.Err(); err != nil {
		return model.Allowlist{}, fmt.Errorf("evaluate base allowlist: %w", err)
	}
	return decodeAllowlist(schema, value, "base allowlist")
}
