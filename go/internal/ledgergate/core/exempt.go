// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"strings"

	"github.com/samber/mo"
)

// defaultExemptBranchRules apply when the config has no exemptBranches key.
func defaultExemptBranchRules() []ExemptBranchRule {
	return []ExemptBranchRule{
		{Prefix: "renovate/", Author: "rigelbuild-renovate[bot]"},
		{Prefix: "trunk-merge/", Author: "trunk-io[bot]"},
	}
}

// IsExemptBranch reports whether a configured bot branch may skip touch coupling.
// The author and repository status must be present, and the PR must be same-repository.
func IsExemptBranch(
	headBranch string,
	author mo.Option[string],
	crossRepository mo.Option[bool],
	rules []ExemptBranchRule,
) bool {
	name, hasAuthor := author.Get()
	cross, hasCross := crossRepository.Get()
	if !hasAuthor || !hasCross || cross {
		return false
	}
	normalized := NormalizeAuthor(name)
	for _, rule := range rules {
		if strings.HasPrefix(headBranch, rule.Prefix) && NormalizeAuthor(rule.Author) == normalized {
			return true
		}
	}
	return false
}

// NormalizeAuthor maps the GitHub "app/<slug>" form onto "<slug>[bot]".
func NormalizeAuthor(author string) string {
	if slug, found := strings.CutPrefix(author, "app/"); found {
		return slug + "[bot]"
	}
	return author
}

// IsExempt reports whether a configured bot branch may skip network checks.
func IsExempt(changed Changed, details mo.Option[PullRequestDetails], cfg Config) bool {
	return exemptFor(changed, details, cfg)
}

// exemptFor resolves PR details and config into the exemption verdict. A nil
// exemptBranches config selects the default rules.
func exemptFor(changed Changed, details mo.Option[PullRequestDetails], cfg Config) bool {
	author, cross := mo.None[string](), mo.None[bool]()
	if pr, ok := details.Get(); ok {
		author, cross = pr.Author, pr.IsCrossRepository
	}
	rules := cfg.ExemptBranches
	if rules == nil {
		rules = defaultExemptBranchRules()
	}
	return IsExemptBranch(changed.HeadBranch, author, cross, rules)
}
