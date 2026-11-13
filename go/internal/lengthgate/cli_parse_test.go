// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate

import (
	"slices"
	"testing"

	"github.com/samber/mo"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want cliArgs
	}{
		{
			name: "config alone",
			args: []string{"--config", "gate.json"},
			want: cliArgs{configPath: "gate.json"},
		},
		{
			name: "all and repeated skip-class",
			args: []string{"--config", "gate.json", "--all", "--skip-class", "spec", "--skip-class", "design record"},
			want: cliArgs{configPath: "gate.json", all: true, skipClasses: []string{"spec", "design record"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			if got.configPath != tc.want.configPath || got.all != tc.want.all ||
				!slices.Equal(got.skipClasses, tc.want.skipClasses) {
				t.Fatalf("parseArgs = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseArgsRejects pins the exact usage error for each malformed command line.
func TestParseArgsRejects(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing config", args: []string{"--all"}, wantErr: "--config is required"},
		{name: "config given twice", args: []string{"--config", "a", "--config", "b"}, wantErr: "--config may appear once"},
		{name: "config without a path", args: []string{"--config"}, wantErr: "--config requires a path"},
		{name: "config followed by another flag", args: []string{"--config", "--all"}, wantErr: "--config requires a path"},
		{name: "all given twice", args: []string{"--config", "a", "--all", "--all"}, wantErr: "--all may appear once"},
		{
			name: "skip-class without a value", args: []string{"--config", "a", "--skip-class"},
			wantErr: "--skip-class requires a class name",
		},
		{
			name: "skip-class with an empty value", args: []string{"--config", "a", "--skip-class", ""},
			wantErr: "--skip-class requires a class name",
		},
		{name: "unknown argument", args: []string{"--config", "a", "--bogus"}, wantErr: `unknown argument "--bogus"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArgs(tc.args)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// lookupFrom returns a lookup over env, reporting a key as set when it is present, even if empty.
func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}

func TestGateEnvFrom(t *testing.T) {
	t.Run("absent variables fall back to defaults", func(t *testing.T) {
		got := gateEnvFrom(lookupFrom(nil), cliArgs{})
		if got.Head != "HEAD" || got.Merge || got.Base.IsPresent() || got.Fetch.IsPresent() {
			t.Fatalf("gateEnvFrom = %+v, want HEAD, no merge, no base or fetch", got)
		}
	})

	t.Run("a set but empty head is kept, not replaced by HEAD", func(t *testing.T) {
		got := gateEnvFrom(lookupFrom(map[string]string{"LENGTH_GATE_HEAD": ""}), cliArgs{})
		if got.Head != "" {
			t.Fatalf("Head = %q, want empty", got.Head)
		}
	})

	t.Run("a set but empty base is Some of empty, not None", func(t *testing.T) {
		got := gateEnvFrom(lookupFrom(map[string]string{"LENGTH_GATE_BASE": ""}), cliArgs{})
		if got.Base != mo.Some("") {
			t.Fatalf("Base = %v, want Some(\"\")", got.Base)
		}
	})

	t.Run("merge is on only for exactly 1", func(t *testing.T) {
		on := gateEnvFrom(lookupFrom(map[string]string{"LENGTH_GATE_MERGE": "1"}), cliArgs{})
		off := gateEnvFrom(lookupFrom(map[string]string{"LENGTH_GATE_MERGE": "true"}), cliArgs{})
		if !on.Merge || off.Merge {
			t.Fatalf("Merge on=%v off=%v, want true then false", on.Merge, off.Merge)
		}
	})

	t.Run("flags flow into the env", func(t *testing.T) {
		got := gateEnvFrom(
			lookupFrom(map[string]string{"LENGTH_GATE_EVENT": "push"}),
			cliArgs{all: true, skipClasses: []string{"spec"}},
		)
		if got.Event != "push" || !got.All || !slices.Equal(got.SkipClasses, []string{"spec"}) {
			t.Fatalf("gateEnvFrom = %+v, want push, all, skip spec", got)
		}
	})
}
