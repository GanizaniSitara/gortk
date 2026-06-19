// Package deps summarizes a project's declared dependencies from its lock files
// and manifests (Cargo.toml, package.json, requirements.txt, pyproject.toml,
// go.mod) into a compact, token-optimized report. Faithful port of rtk's
// src/cmds/system/deps.rs.
//
// Unlike most gortk commands this one does not wrap an external tool; it reads
// the manifest files directly and emits a summary, so it spawns no process and
// makes no network calls.
package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// maxDeps caps how many production dependencies we list per ecosystem.
const maxDeps = core.CapWarnings

// maxDevDeps caps dev dependencies — secondary to prod, so we show fewer.
var maxDevDeps = core.Reduced(core.CapWarnings, 5)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "deps",
		Summary: "Summarize project dependencies",
		Run:     Run,
	})
}

// Run summarizes dependencies for the project rooted at args[0] (default ".").
func Run(args []string, verbose int) (int, error) {
	path := "."
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			path = a
			break
		}
	}

	// If the path points at a file, scan its parent directory.
	dir := path
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		dir = filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Scanning dependencies in: %s\n", dir)
	}

	report, _ := Summarize(dir)
	fmt.Print(report)
	return 0, nil
}

// Summarize reads the recognized manifest files in dir and returns the compact
// dependency report plus the concatenated raw manifest text (the latter mirrors
// rtk's token-savings tracking input). Missing or unreadable files are skipped.
func Summarize(dir string) (report string, raw string) {
	var rtk strings.Builder
	var rawB strings.Builder
	found := false

	type manifest struct {
		file    string
		header  string
		summary func(content string) string
	}
	manifests := []manifest{
		{"Cargo.toml", "Rust (Cargo.toml):\n", summarizeCargo},
		{"package.json", "Node.js (package.json):\n", summarizePackageJSON},
		{"requirements.txt", "Python (requirements.txt):\n", summarizeRequirements},
		{"pyproject.toml", "Python (pyproject.toml):\n", summarizePyproject},
		{"go.mod", "Go (go.mod):\n", summarizeGoMod},
	}

	for _, m := range manifests {
		p := filepath.Join(dir, m.file)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		found = true
		content := core.NormalizeNewlines(string(data))
		rawB.WriteString(content)
		rtk.WriteString(m.header)
		rtk.WriteString(m.summary(content))
	}

	if !found {
		rtk.WriteString(fmt.Sprintf("No dependency files found in %s", dir))
	}

	return rtk.String(), rawB.String()
}

// lines mirrors Rust's str::lines(): split on '\n' and drop a single trailing
// empty element so a trailing newline does not yield a phantom blank line.
func lines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}
