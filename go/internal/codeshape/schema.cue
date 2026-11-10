#Entry: close({
	value: int & >=0
	reason: string & =~"^\\S+(\\s+\\S+){2,}$"
})

#Config: close({
	fileLimit: 500
	dirLimit: 10
	extensions: [...string]
	exclude: [...string]
	// When multiple globs match, the smallest limit wins.
	dirLimits?: [string]: {limit: int & >10, reason: string & =~"^\\S+(\\s+\\S+){2,}$"}
})

#Allowlist: close({
	files?: [string]: #Entry
	dirs?: [string]: #Entry
})
