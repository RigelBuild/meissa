export type ConfigMode = "required" | "default";

export interface BundleSource {
	repo: string;
	mount: string;
	config: ConfigMode;
}

export interface FetchedSource extends BundleSource {
	root: string;
	sha: string;
}

export interface SectionRule {
	id: string;
	label: string;
	route: string;
	prefix?: string;
	files?: string[];
	pagefind?: boolean;
	collapsed?: boolean;
}

export interface PublishConfig {
	exclude: string[];
	sections: SectionRule[];
	packages?: { route: string; allow: string[]; readmeDepth: number };
}

export interface BundlePage {
	repo: string;
	sourcePath: string;
	route: string;
	section: string | null;
	pagefind: boolean;
}

export interface BrokenLink {
	repo: string;
	sourcePath: string;
	target: string;
}

export interface BundleManifest {
	docsRepo: { repo: string; sha: string };
	sources: { repo: string; ref: string; sha: string; pages: number }[];
}
