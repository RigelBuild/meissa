// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declaredBins returns the bin paths a package.json record declares, as a string or an object.
func declaredBins(t *testing.T, manifest json5Record) []string {
	t.Helper()
	switch bin := manifest["bin"].(type) {
	case nil:
		return nil
	case string:
		return []string{bin}
	case map[string]any:
		paths := make([]string, 0, len(bin))
		for _, value := range bin {
			path, ok := value.(string)
			if !ok {
				t.Fatalf("bin entry %v is not a string", value)
			}
			paths = append(paths, path)
		}
		return paths
	default:
		t.Fatalf("bin has unexpected type %T", bin)
		return nil
	}
}

// packageBin is one bin declared by a package under packages/.
type packageBin struct {
	pkg  string
	bin  string
	path string
}

// readPackageBins lists every bin declared by the packages under root/packages.
func readPackageBins(t *testing.T, root string) []packageBin {
	t.Helper()
	packages := filepath.Join(root, "packages")
	entries, err := os.ReadDir(packages)
	if err != nil {
		t.Fatal(err)
	}
	var bins []packageBin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest := readJSON5Record(t, filepath.Join(packages, entry.Name(), "package.json"))
		for _, bin := range declaredBins(t, manifest) {
			bins = append(bins, packageBin{
				pkg:  entry.Name(),
				bin:  bin,
				path: filepath.Join(packages, entry.Name(), bin),
			})
		}
	}
	return bins
}

func TestPackageBins(t *testing.T) {
	root := filepath.Dir(moduleRoot(t))
	bins := readPackageBins(t, root)

	t.Run("reads string, object, and absent bin declarations", func(t *testing.T) {
		if got := declaredBins(t, json5Record{"bin": "./cli.ts"}); len(got) != 1 || got[0] != "./cli.ts" {
			t.Fatalf("string bin = %q, want [./cli.ts]", got)
		}
		object := declaredBins(t, json5Record{"bin": json5Record{"a": "./a.ts", "b": "./b.ts"}})
		if len(object) != 2 || !containsString(object, "./a.ts") || !containsString(object, "./b.ts") {
			t.Fatalf("object bin = %q, want [./a.ts ./b.ts]", object)
		}
		if got := declaredBins(t, json5Record{}); len(got) != 0 {
			t.Fatalf("absent bin = %q, want none", got)
		}
	})

	t.Run("at least one package declares a bin", func(t *testing.T) {
		if len(bins) == 0 {
			t.Fatal("no package under packages/ declares a bin")
		}
	})

	t.Run("each bin starts with the bun shebang", func(t *testing.T) {
		for _, bin := range bins {
			content, err := os.ReadFile(bin.path)
			if err != nil {
				t.Fatal(err)
			}
			firstLine, _, _ := strings.Cut(string(content), "\n")
			if firstLine != "#!/usr/bin/env bun" {
				t.Errorf("%s bin %s first line = %q, want #!/usr/bin/env bun", bin.pkg, bin.bin, firstLine)
			}
		}
	})

	t.Run("each bin is npm-normalized without a leading ./", func(t *testing.T) {
		// npm publish rewrites a "./" bin path and warns; keep manifests in the form npm publishes.
		for _, bin := range bins {
			if strings.HasPrefix(bin.bin, "./") {
				t.Errorf("%s bin %s starts with ./", bin.pkg, bin.bin)
			}
		}
	})
}
