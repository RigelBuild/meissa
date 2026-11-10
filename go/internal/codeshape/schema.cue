#FunctionEntry: close({
	id: string & !=""
	value: int & >=0
	reason: string & =~"^\\S+(\\s+\\S+){2,}$"
})

#FunctionLimits: close({
	go: close({
		cognitive: int & >0
		lines: int & >0
		statements: int & >0
	})
	ts: close({
		cognitive: int & >0
		lines: int & >0
	})
})

#Metric: "go-cognitive" | "go-lines" | "go-statements" | "ts-cognitive" | "ts-lines"

#Allowlist: close({
	files?: [string]: #Entry
	dirs?: [string]: #Entry
	functions?: [#Metric]: [string]: [...#FunctionEntry]
})

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
	functions: close({
		go: close({cognitive: 20, lines: 60, statements: 40})
		ts: close({cognitive: 15, lines: 50})
	})
})

