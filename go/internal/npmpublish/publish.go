<<<<<<< conflict 1 of 1
%%%%%%% diff from: mlzomnyq e661437d "fix(go): resolve M13 lint findings (RIG-8132)" (rebase destination)
\\\\\\\        to: uzmmuouy 4b30872a "feat(go): npm publish tool in Go (RIG-8133)" (parents of rebased revision)
+// SPDX-License-Identifier: MIT OR Apache-2.0
+
+// Package npmpublish publishes the npm packages that release-please released.
+package npmpublish
+
+import (
+	"context"
+	"encoding/json"
+	"errors"
+	"fmt"
+	"net/http"
+	"os"
+	"path/filepath"
+	"strings"
+)
+
+const registryBase = "https://registry.npmjs.org"
+
+var errBadPaths = errors.New("bad paths_released")
+
+// Manifest holds the package.json fields the publish flow reads.
+type Manifest struct {
+	Name    string `json:"name"`
+	Version string `json:"version"`
+}
+
+// Deps is the I/O the publish flow needs. Tests substitute fakes.
+type Deps interface {
+	Read(path string) (Manifest, error)
+	Status(ctx context.Context, url string) (int, error)
+	Publish(ctx context.Context, path string) error
+	Log(line string)
+}
+
+// ParsePaths decodes release-please's paths_released JSON array.
+// An empty list or a non-string item is an error.
+func ParsePaths(raw string) ([]string, error) {
+	var parsed []any
+	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
+		return nil, fmt.Errorf("%w: %w", errBadPaths, err)
+	}
+	if len(parsed) == 0 {
+		return nil, errBadPaths
+	}
+	paths := make([]string, 0, len(parsed))
+	for _, item := range parsed {
+		path, ok := item.(string)
+		if !ok {
+			return nil, errBadPaths
+		}
+		paths = append(paths, path)
+	}
+	return paths, nil
+}
+
+// IsPublished reports whether the registry already serves this name and version.
+func IsPublished(ctx context.Context, m Manifest, d Deps) (bool, error) {
+	url := registryBase + "/" + strings.Replace(m.Name, "/", "%2f", 1) + "/" + m.Version
+	status, err := d.Status(ctx, url)
+	if err != nil {
+		return false, fmt.Errorf("fetch %s: %w", url, err)
+	}
+	switch status {
+	case http.StatusOK:
+		return true, nil
+	case http.StatusNotFound:
+		return false, nil
+	default:
+		return false, fmt.Errorf("%s: registry returned %d", url, status)
+	}
+}
+
+// PublishAll publishes each path whose version is not on the registry yet, in order.
+// Skipping published versions lets a re-run finish a release that stopped partway.
+func PublishAll(ctx context.Context, paths []string, d Deps) error {
+	for _, path := range paths {
+		manifest, err := d.Read(path)
+		if err != nil {
+			return fmt.Errorf("read %s: %w", path, err)
+		}
+		id := manifest.Name + "@" + manifest.Version
+		published, err := IsPublished(ctx, manifest, d)
+		if err != nil {
+			return err
+		}
+		if published {
+			d.Log("skip " + id + ": already on npm")
+			continue
+		}
+		if err := d.Publish(ctx, path); err != nil {
+			return fmt.Errorf("publish %s: %w", path, err)
+		}
+		d.Log("published " + id)
+	}
+	return nil
+}
+
+// publishReleased keeps the packages/ paths and publishes them. A release with none is a no-op.
+func publishReleased(ctx context.Context, paths []string, d Deps) error {
+	var npmPaths []string
+	for _, path := range paths {
+		if strings.HasPrefix(path, "packages/") {
+			npmPaths = append(npmPaths, path)
+		}
+	}
+	if len(npmPaths) == 0 {
+		d.Log("skip: no npm package released")
+		return nil
+	}
+	return PublishAll(ctx, npmPaths, d)
+}
+
+// allPackages lists each public directory under root/packages. os.ReadDir sorts by name.
+func allPackages(root string) ([]string, error) {
+	entries, err := os.ReadDir(filepath.Join(root, "packages"))
+	if err != nil {
+		return nil, fmt.Errorf("list packages: %w", err)
+	}
+	var paths []string
+	for _, entry := range entries {
+		if !entry.IsDir() {
+			continue
+		}
+		path := "packages/" + entry.Name()
+		var manifest struct {
+			Private bool `json:"private"`
+		}
+		if err := readPackageJSON(root, path, &manifest); err != nil {
+			return nil, err
+		}
+		if !manifest.Private {
+			paths = append(paths, path)
+		}
+	}
+	return paths, nil
+}
+
+// readPackageJSON decodes root/path/package.json into v.
+func readPackageJSON(root, path string, v any) error {
+	file := filepath.Join(root, path, "package.json")
+	data, err := os.ReadFile(file) //nolint:gosec // The path is a package directory under the workspace root.
+	if err != nil {
+		return fmt.Errorf("read %s: %w", file, err)
+	}
+	if err := json.Unmarshal(data, v); err != nil {
+		return fmt.Errorf("parse %s: %w", file, err)
+	}
+	return nil
+}
+++++++ ssoulylw 0beea2e2 (rebased revision)
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package npmpublish publishes the npm packages that release-please released.
package npmpublish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const registryBase = "https://registry.npmjs.org"

// registryTimeout bounds each registry status check, so a stalled registry cannot hold the release job.
const registryTimeout = 30 * time.Second

var (
	errBadPaths                = errors.New("bad paths_released")
	errUnexpectedRegistryStatus = errors.New("unexpected registry status")
)

// packageManifest holds the package.json fields the publish flow reads.
type packageManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// publishDeps is the I/O the publish flow needs. Tests substitute fakes.
type publishDeps interface {
	Read(path string) (packageManifest, error)
	Status(ctx context.Context, url string) (int, error)
	Publish(ctx context.Context, path string) error
	Log(line string)
}

// parsePaths decodes release-please's paths_released JSON array.
// An empty list or a non-string item is an error.
func parsePaths(raw string) ([]string, error) {
	var parsed []any
	err := json.Unmarshal([]byte(raw), &parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadPaths, err)
	}
	if len(parsed) == 0 {
		return nil, errBadPaths
	}
	paths := make([]string, 0, len(parsed))
	for _, item := range parsed {
		path, ok := item.(string)
		if !ok {
			return nil, errBadPaths
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// isPublished reports whether the registry already serves this name and version.
func isPublished(ctx context.Context, m packageManifest, d publishDeps) (bool, error) {
	url := registryBase + "/" + strings.Replace(m.Name, "/", "%2f", 1) + "/" + m.Version
	status, err := d.Status(ctx, url)
	if err != nil {
		return false, fmt.Errorf("fetch %s: %w", url, err)
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("%s: registry returned %d: %w", url, status, errUnexpectedRegistryStatus)
	}
}

// publishAll publishes each path whose version is not on the registry yet, in order.
// Skipping published versions lets a re-run finish a release that stopped partway.
func publishAll(ctx context.Context, paths []string, d publishDeps) error {
	for _, path := range paths {
		manifest, err := d.Read(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		id := manifest.Name + "@" + manifest.Version
		published, err := isPublished(ctx, manifest, d)
		if err != nil {
			return err
		}
		if published {
			d.Log("skip " + id + ": already on npm")
			continue
		}
		err = d.Publish(ctx, path)
		if err != nil {
			return fmt.Errorf("publish %s: %w", path, err)
		}
		d.Log("published " + id)
	}
	return nil
}

// publishReleased keeps the packages/ paths and publishes them. A release with none is a no-op.
func publishReleased(ctx context.Context, paths []string, d publishDeps) error {
	var npmPaths []string
	for _, path := range paths {
		if strings.HasPrefix(path, "packages/") {
			npmPaths = append(npmPaths, path)
		}
	}
	if len(npmPaths) == 0 {
		d.Log("skip: no npm package released")
		return nil
	}
	return publishAll(ctx, npmPaths, d)
}

// allPackages lists each public directory under root/packages. os.ReadDir sorts by name.
func allPackages(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := "packages/" + entry.Name()
		var manifest struct {
			Private bool `json:"private"`
		}
		err := readPackageJSON(root, path, &manifest)
		if err != nil {
			return nil, err
		}
		if !manifest.Private {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// readPackageJSON decodes root/path/package.json into v.
func readPackageJSON(root, path string, v any) error {
	file := filepath.Join(root, path, "package.json")
	data, err := os.ReadFile(file) //nolint:gosec // The path is a package directory under the workspace root.
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	err = json.Unmarshal(data, v)
	if err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}
	return nil
}
>>>>>>> conflict 1 of 1 ends
