const PULL_REQUEST_TEMPLATE = ".github/PULL_REQUEST_TEMPLATE.md";

export function isGatherable(path: string): boolean {
	const normalized = path.replaceAll("\\", "/").replace(/^\.\//, "");
	if (!/\.md$/i.test(normalized)) return false;
	if (normalized.split("/").includes("node_modules")) return false;
	if (normalized.split("/").includes("dist")) return false;
	if (normalized.split("/").includes("outputs")) return false;
	if (
		normalized.split("/").some((segment) => segment.startsWith(".")) &&
		normalized !== PULL_REQUEST_TEMPLATE
	)
		return false;
	if (
		normalized
			.split("/")
			.some(
				(segment, index, parts) =>
					segment === "designs" && parts[index - 1] === "docs",
			)
	)
		return false;
	if (normalized.split("/").at(-1)?.toLowerCase() === "changelog.md")
		return false;
	return true;
}
