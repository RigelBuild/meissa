// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate

import (
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// docConfig mirrors the TypeScript DOC_CONFIG used by the doc ratchet tests.
var docConfig = model.Config{
	Extensions: []string{".ts"},
	Exclude:    nil,
	Docs: []model.DocClass{
		{
			Name: "spec", Include: []string{"docs/specs/**/*.md"}, Exclude: []string{},
			Target: 1500, MarkerAllowed: false,
		},
		{
			Name: "design record", Include: []string{"docs/designs/**/*.md"}, Exclude: []string{},
			Target: 2500, MarkerAllowed: true,
		},
		{
			Name: "repo doc", Include: []string{"docs/**/*.md"}, Exclude: []string{"docs/designs/**"},
			Target: 1500, MarkerAllowed: true,
		},
	},
}
