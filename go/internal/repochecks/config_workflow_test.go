// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRenovatePolicyScheduledWorkflow(t *testing.T) {
	t.Run("the scheduled workflow mints its token in the main environment and prepares devenv", func(t *testing.T) {
		root := filepath.Dir(moduleRoot(t))
		workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "renovate.yml"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(workflow)
		for _, fragment := range []string{
			`cron: "0 6 * * *"`, "workflow_dispatch:", "environment: main",
			"actions/create-github-app-token@", "client-id: ${{ vars.RENOVATE_APP_CLIENT_ID }}",
			"private-key: ${{ secrets.RENOVATE_APP_PRIVATE_KEY }}", "RENOVATE_TOKEN: ${{ steps.app-token.outputs.token }}",
			"bunx renovate@44.46.2",
		} {
			if !strings.Contains(text, fragment) {
				t.Errorf("workflow missing %q", fragment)
			}
		}
		var document yaml.Node
		parseErr := yaml.Unmarshal(workflow, &document)
		if parseErr != nil {
			t.Fatalf("parse renovate workflow: %v", parseErr)
		}
		steps := workflowSteps(&document)
		devenv := findWorkflowStep(steps, "Put pinned devenv CLI on PATH")
		devenvAssert := findWorkflowStep(steps, "Assert devenv is on PATH")
		node := findWorkflowStep(steps, "Put pinned Node.js on PATH")
		nodeAssert := findWorkflowStep(steps, "Assert pinned Node.js is on PATH")
		goToolchain := findWorkflowStep(steps, "Put pinned Go toolchain on PATH")
		goAssert := findWorkflowStep(steps, "Assert pinned Go toolchain is on PATH")
		run := findWorkflowStep(steps, "Run Renovate")
		for _, pair := range [][2]int{{devenv, devenvAssert}, {node, nodeAssert}, {goToolchain, goAssert}} {
			if pair[0] < 0 || pair[1] <= pair[0] || run < 0 || pair[1] >= run {
				t.Fatalf("PATH setup/assert order is invalid: %v", pair)
			}
		}
		assertRun(t, steps[devenv],
			"echo \"$(nix build --no-link --print-out-paths --inputs-from . nixpkgs#devenv)/bin\" >> \"$GITHUB_PATH\"")
		assertRun(t, steps[devenvAssert], "command -v devenv")
		assertRun(t, steps[node],
			"echo \"$(nix build --no-link --print-out-paths --inputs-from . nixpkgs#nodejs)/bin\" >> \"$GITHUB_PATH\"")
		assertRun(t, steps[nodeAssert], "node --version")
		assertRun(t, steps[goToolchain],
			"echo \"$(nix build --no-link --print-out-paths .#go)/bin\" >> \"$GITHUB_PATH\"")
		assertRun(t, steps[goAssert], "go version")
	})
}

type workflowStep struct {
	name string
	run  string
}

func workflowSteps(document *yaml.Node) []workflowStep {
	jobs := mappingValue(document.Content[0], "jobs")
	renovate := mappingValue(jobs, "renovate")
	steps := mappingValue(renovate, "steps")
	parsed := make([]workflowStep, 0, len(steps.Content))
	for _, step := range steps.Content {
		parsed = append(parsed, workflowStep{
			name: nodeValue(mappingValue(step, "name")),
			run:  nodeValue(mappingValue(step, "run")),
		})
	}
	return parsed
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func nodeValue(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}

func findWorkflowStep(steps []workflowStep, name string) int {
	for i, step := range steps {
		if step.name == name {
			return i
		}
	}
	return -1
}

func assertRun(t *testing.T, step workflowStep, want string) {
	t.Helper()
	if step.run != want {
		t.Errorf("step %q run = %q, want %q", step.name, step.run, want)
	}
}
