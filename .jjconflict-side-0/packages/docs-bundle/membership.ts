const PULL_REQUEST_TEMPLATE = ".github/PULL_REQUEST_TEMPLATE.md";

/** Whether a directory segment is excluded from gathering and recursive walks. */
export function isSkippedDirectory(name: string): boolean {
	return (
		name !== ".github" &&
		(name === "node_modules" ||
			name === "dist" ||
			name === "outputs" ||
			name.startsWith(".") ||
			name.startsWith("_"))
	);
}

/** Expects a repo-relative POSIX path; every segment is checked, so ancestors count. */
export function isGatherable(path: string): boolean {
	const normalized = path.replaceAll("\\", "/").replace(/^\.\//, "");
	if (!/\.md$/i.test(normalized)) return false;
	const segments = normalized.split("/");
	if (
		segments.some(
			(segment, index) =>
				(index < segments.length - 1 && isSkippedDirectory(segment)) ||
				((segment.startsWith(".") || segment.startsWith("_")) &&
					normalized !== PULL_REQUEST_TEMPLATE),
		)
	)
		return false;
	if (normalized.startsWith("docs/designs/")) return false;
	if (normalized.split("/").at(-1)?.toLowerCase() === "changelog.md")
		return false;
	return true;
}
