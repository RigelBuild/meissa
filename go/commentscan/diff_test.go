package commentscan

import (
	"reflect"
	"strings"
	"testing"
)

func diff(path, hunk string) string {
	return strings.Join([]string{
		"diff --git a/" + path + " b/" + path,
		"--- a/" + path,
		"+++ b/" + path,
		"@@ -1,3 +1,3 @@",
		hunk,
	}, "\n")
}

func firstFile(t *testing.T, input string) FileDiff {
	t.Helper()
	files := ParseDiff(input)
	if len(files) != 1 {
		t.Fatalf("ParseDiff returned %d files, want 1", len(files))
	}
	return files[0]
}

func TestParseDiff(t *testing.T) {
	t.Run("removed SQL comment-looking lines are hunk deletions", func(t *testing.T) {
		file := firstFile(t, diff("queries/g.sql", " SELECT 1;\n--- a note\n SELECT 2;"))
		if file.Path != "queries/g.sql" {
			t.Errorf("path = %q, want %q", file.Path, "queries/g.sql")
		}
		got := make([][2]string, len(file.Hunks[0].Lines))
		for i, line := range file.Hunks[0].Lines {
			got[i] = [2]string{string(line.Kind), line.Text}
		}
		want := [][2]string{{" ", "SELECT 1;"}, {"-", "-- a note"}, {" ", "SELECT 2;"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("hunk lines = %#v, want %#v", got, want)
		}
	})

	t.Run("mixed hunk lines retain old and new line numbers", func(t *testing.T) {
		input := strings.Join([]string{
			"diff --git a/x.ts b/x.ts",
			"--- a/x.ts",
			"+++ b/x.ts",
			"@@ -7,3 +11,3 @@",
			" context",
			"-old",
			"+new",
			" context",
		}, "\n")
		lines := firstFile(t, input).Hunks[0].Lines
		got := make([][3]int, len(lines))
		for i, line := range lines {
			got[i] = [3]int{int(line.Kind), line.OldNo, line.NewNo}
		}
		want := [][3]int{{' ', 7, 11}, {'-', 8, 12}, {'+', 9, 12}, {' ', 9, 13}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("line kinds/numbers = %#v, want %#v", got, want)
		}
	})
	t.Run("added lines beginning with plus signs remain additions", func(t *testing.T) {
		file := firstFile(t, diff("x.md", " a\n+++ b\n c"))
		got := make([][2]string, len(file.Hunks[0].Lines))
		for i, line := range file.Hunks[0].Lines {
			got[i] = [2]string{string(line.Kind), line.Text}
		}
		want := [][2]string{{" ", "a"}, {"+", "++ b"}, {" ", "c"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("hunk lines = %#v, want %#v", got, want)
		}
	})

	t.Run("file headers before the first hunk set the path", func(t *testing.T) {
		input := strings.Join([]string{
			"diff --git a/old.ts b/new.ts",
			"--- a/old.ts",
			"+++ b/new.ts",
			"@@ -1 +1 @@",
			"-const n = 1;",
			"+const n = 2;",
		}, "\n")
		if got := firstFile(t, input).Path; got != "new.ts" {
			t.Errorf("path = %q, want %q", got, "new.ts")
		}
	})

	t.Run("deleted files retain their original path", func(t *testing.T) {
		input := strings.Join([]string{
			"diff --git a/gone.ts b/gone.ts",
			"--- a/gone.ts",
			"+++ /dev/null",
			"@@ -1 +0,0 @@",
			"-const n = 1;",
		}, "\n")
		file := firstFile(t, input)
		if !file.Deleted || file.Path != "gone.ts" {
			t.Errorf("deleted/path = %t/%q, want true/%q", file.Deleted, file.Path, "gone.ts")
		}
	})

	t.Run("rename and mode records set metadataOnly", func(t *testing.T) {
		rename := strings.Join([]string{
			"diff --git a/old.ts b/new.ts",
			"similarity index 100%",
			"rename from old.ts",
			"rename to new.ts",
		}, "\n")
		mode := strings.Join([]string{
			"diff --git a/script.sh b/script.sh",
			"old mode 100644",
			"new mode 100755",
		}, "\n")
		if !firstFile(t, rename).MetadataOnly || !firstFile(t, mode).MetadataOnly {
			t.Error("rename and mode-only diffs must be metadata-only")
		}
	})

	t.Run("a pure rename or copy keeps its source as oldPath", func(t *testing.T) {
		rename := firstFile(t, strings.Join([]string{
			"diff --git a/src/old.ts b/src/new.ts",
			"similarity index 100%",
			"rename from src/old.ts",
			"rename to src/new.ts",
		}, "\n"))
		if rename.OldPath != "src/old.ts" || rename.NewPath != "src/new.ts" {
			t.Errorf("rename paths = %q/%q, want %q/%q", rename.OldPath, rename.NewPath, "src/old.ts", "src/new.ts")
		}
		copied := firstFile(t, strings.Join([]string{
			"diff --git a/a.ts b/b.ts",
			"similarity index 100%",
			"copy from a.ts",
			"copy to b.ts",
		}, "\n"))
		if copied.OldPath != "a.ts" || copied.NewPath != "b.ts" {
			t.Errorf("copy paths = %q/%q, want %q/%q", copied.OldPath, copied.NewPath, "a.ts", "b.ts")
		}
		nested := firstFile(t, strings.Join([]string{
			"diff --git a/a/old.ts b/b/new.ts",
			"similarity index 100%",
			"rename from a/old.ts",
			"rename to b/new.ts",
		}, "\n"))
		if nested.OldPath != "a/old.ts" || nested.Path != "b/new.ts" {
			t.Errorf("nested rename paths = %q/%q, want %q/%q", nested.OldPath, nested.Path, "a/old.ts", "b/new.ts")
		}
	})

	t.Run("binary diff records are identified", func(t *testing.T) {
		input := strings.Join([]string{
			"diff --git a/image.png b/image.png",
			"index 1111111..2222222 100644",
			"Binary files a/image.png and b/image.png differ",
		}, "\n")
		if !firstFile(t, input).Binary {
			t.Error("binary diff was not marked binary")
		}
	})

	t.Run("header paths survive \" b/\" inside a name and git C-quoting", func(t *testing.T) {
		spaced := firstFile(t, strings.Join([]string{
			"diff --git a/x b/y.ts b/x b/y.ts",
			"new file mode 100644",
			"index 0000000..e69de29",
		}, "\n"))
		if spaced.Path != "x b/y.ts" {
			t.Errorf("spaced path = %q, want %q", spaced.Path, "x b/y.ts")
		}
		tab := firstFile(t, strings.Join([]string{
			`diff --git "a/t\tb.ts" "b/t\tb.ts"`,
			"index 1111111..2222222 100644",
			`Binary files "a/t\tb.ts" and "b/t\tb.ts" differ`,
		}, "\n"))
		if tab.Path != "t\tb.ts" {
			t.Errorf("tab path = %q, want %q", tab.Path, "t\tb.ts")
		}
		quoted := firstFile(t, strings.Join([]string{
			`diff --git "a/caf\303\251\nx.ts" "b/caf\303\251\nx.ts"`,
			`--- "a/caf\303\251\nx.ts"`,
			`+++ "b/caf\303\251\nx.ts"`,
			"@@ -1 +1 @@",
			"-a",
			"+b",
		}, "\n"))
		if quoted.OldPath != "café\nx.ts" || quoted.Path != "café\nx.ts" {
			t.Errorf("quoted paths = %q/%q, want %q/%q", quoted.OldPath, quoted.Path, "café\nx.ts", "café\nx.ts")
		}
	})

	t.Run("a CRLF-saved diff keeps clean header paths", func(t *testing.T) {
		input := strings.Join([]string{
			`diff --git "a/\303\251 x.ts" "b/\303\251 x.ts"`,
			"index 1111111..2222222 100644",
			`--- "a/\303\251 x.ts"`,
			`+++ "b/\303\251 x.ts"`,
			"@@ -1 +1 @@",
			"-a",
			"+b",
			"diff --git a/old.ts b/new.ts",
			"similarity index 100%",
			"rename from old.ts",
			"rename to new.ts",
			"",
		}, "\r\n")
		files := ParseDiff(input)
		if len(files) != 2 {
			t.Fatalf("ParseDiff returned %d files, want 2", len(files))
		}
		quoted, renamed := files[0], files[1]
		if quoted.OldPath != "é x.ts" || quoted.Path != "é x.ts" {
			t.Errorf("quoted paths = %q/%q, want %q/%q", quoted.OldPath, quoted.Path, "é x.ts", "é x.ts")
		}
		gotLines := []string{quoted.Hunks[0].Lines[0].Text, quoted.Hunks[0].Lines[1].Text}
		if !reflect.DeepEqual(gotLines, []string{"a\r", "b\r"}) {
			t.Errorf("hunk line text = %#v, want %#v", gotLines, []string{"a\r", "b\r"})
		}
		if renamed.OldPath != "old.ts" || renamed.Path != "new.ts" {
			t.Errorf("rename paths = %q/%q, want %q/%q", renamed.OldPath, renamed.Path, "old.ts", "new.ts")
		}
	})

	t.Run("quoted astral characters and trailing spaces in names survive", func(t *testing.T) {
		emoji := firstFile(t, strings.Join([]string{
			`diff --git "a/😀\tx.ts" "b/😀\tx.ts"`,
			"new file mode 100644",
			"index 0000000..e69de29",
		}, "\n"))
		if emoji.Path != "😀\tx.ts" {
			t.Errorf("emoji path = %q, want %q", emoji.Path, "😀\tx.ts")
		}
		trailing := firstFile(t, strings.Join([]string{
			"diff --git a/src/x.ts  b/src/x.ts ",
			"index 1111111..2222222 100644",
			"--- a/src/x.ts \t",
			"+++ b/src/x.ts \t",
			"@@ -1 +1 @@",
			"-a",
			"+b",
		}, "\n"))
		if trailing.OldPath != "src/x.ts " || trailing.Path != "src/x.ts " {
			t.Errorf("trailing-space paths = %q/%q, want %q/%q", trailing.OldPath, trailing.Path, "src/x.ts ", "src/x.ts ")
		}
		empty := firstFile(t, strings.Join([]string{
			"diff --git a/src/x.ts  b/src/x.ts ",
			"new file mode 100644",
			"index 0000000..e69de29",
		}, "\n"))
		if empty.Path != "src/x.ts " {
			t.Errorf("empty-file path = %q, want %q", empty.Path, "src/x.ts ")
		}
	})

	t.Run("empty and binary files added or deleted are flagged by their mode header", func(t *testing.T) {
		added := firstFile(t, strings.Join([]string{
			"diff --git a/pkg/.gitkeep b/pkg/.gitkeep",
			"new file mode 100644",
			"index 0000000..e69de29",
		}, "\n"))
		if !added.Added || added.Deleted || added.Path != "pkg/.gitkeep" {
			t.Errorf("added file state = %t/%t/%q, want true/false/%q", added.Added, added.Deleted, added.Path, "pkg/.gitkeep")
		}
		deleted := firstFile(t, strings.Join([]string{
			"diff --git a/img.png b/img.png",
			"deleted file mode 100644",
			"index 1111111..0000000",
			"Binary files a/img.png and /dev/null differ",
		}, "\n"))
		if deleted.Added || !deleted.Deleted || !deleted.Binary {
			t.Errorf("deleted file state = %t/%t/%t, want false/true/true", deleted.Added, deleted.Deleted, deleted.Binary)
		}
	})
}

func TestExtOf(t *testing.T) {
	t.Run("extOf finds a case-insensitive filename extension", func(t *testing.T) {
		if got := ExtOf("src/Component.TSX"); got != ".tsx" {
			t.Errorf("ExtOf() = %q, want %q", got, ".tsx")
		}
		if got := ExtOf("README"); got != "" {
			t.Errorf("ExtOf() for README = %q, want empty", got)
		}
	})
}
