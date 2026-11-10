#Entry: close({
	value: int & >=0
	reason: string & =~"^\\S+(\\s+\\S+){2,}$"
})

#FunctionEntry: close({
	id: string
	value: int & >=0
	reason: string & =~"^\\S+(\\s+\\S+){2,}$"
})

#Config: close({
	fileLimit: int & >0
	dirLimit: int & >0
	extensions: [...string]
	exclude: [...string]
	// When multiple globs match, the smallest limit wins.
	dirLimits?: [string]: {limit: int & >10, reason: string & =~"^\\S+(\\s+\\S+){2,}$"}
})

#Allowlist: close({
	files?: [string]: #Entry
	dirs?: [string]: #Entry
	functions?: [string]: [string]: [...#FunctionEntry]
})
