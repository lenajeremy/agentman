package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Railway rebuilds the relay only when a changed file matches one of
// railway.json's watch patterns. A package the relay imports that is missing
// from them ships in `am` but never reaches production: internal/tunnel, which
// the relay serves preview links through, was missing.
func TestRailwayRedeploysForEveryPackageTheRelayBuildsFrom(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "railway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Build struct {
			WatchPatterns []string `json:"watchPatterns"`
		} `json:"build"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	watched := map[string]bool{}
	for _, pattern := range config.Build.WatchPatterns {
		watched[pattern] = true
	}

	command := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/relay")
	command.Dir = root
	out, err := command.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const module = "github.com/lenajeremy/agentman/"
	for _, path := range strings.Fields(string(out)) {
		local, ok := strings.CutPrefix(path, module)
		if !ok {
			continue
		}
		if !watched[local+"/**"] {
			t.Errorf("the relay builds from %s, but railway.json does not watch %s/**", local, local)
		}
	}
}
