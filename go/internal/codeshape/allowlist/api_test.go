// SPDX-License-Identifier: MIT OR Apache-2.0

package allowlist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/codeshape/allowlist"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
	"github.com/RigelBuild/meissa/go/internal/codeshape/testsupport"
)

func TestLoadAllowsT1ConfigWithoutFunctionLimits(t *testing.T) {
	root := testsupport.NewFixtureRepo(t)
	testsupport.WriteConfigFiles(t, root, "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\"]\nexclude: []\n", "files: {}\n")
	cfg, _, err := allowlist.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FileLimit != 500 || cfg.Functions.Go.Lines != 0 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadBiomeConfigSupportsJSONCNameAndRootedRead(t *testing.T) {
	root := testsupport.NewFixtureRepo(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	repository, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if _, err := repository.ReadFile("escape"); err == nil {
		t.Fatal("repository root followed a symlink outside its boundary")
	}
	if model.MetricTSLines != "ts-lines" {
		t.Fatalf("metric = %q", model.MetricTSLines)
	}
}
