package core

// Global truncation caps shared by every filter. Mirrors rtk's
// src/core/truncate.rs cap classes.
const (
	// CapErrors is for errors: most actionable, shown the most.
	CapErrors = 20
	// CapWarnings is for warnings and test failures: lower signal density.
	CapWarnings = 10
	// CapList is for flat lists (PRs, services, packages): one line per item.
	CapList = 20
	// CapInventory is for inventories (pip list, docker images): exhaustive.
	CapInventory = 50
)

// Reduced returns a cap reduced for a verbose data class. It falls back to cap
// when by >= cap so a deviation can never empty the list; 0 stays 0.
// Underflow-safe.
func Reduced(cap, by int) int {
	if by < cap {
		return cap - by
	}
	return cap
}

// NoiseDirs are directory names hidden from directory listings unless the user
// explicitly asks for them (e.g. ls -a). Mirrors rtk's NOISE_DIRS.
//
// Note: "env" (legacy Python virtualenv dir) is noise, but ".env" (dotenv) is
// intentionally absent — agents must still see dotenv files.
var NoiseDirs = []string{
	"node_modules", ".git", "target", "__pycache__", ".next", "dist", "build",
	".cache", ".turbo", ".vercel", ".pytest_cache", ".mypy_cache", ".tox",
	".venv", "venv", "env", "coverage", ".nyc_output", ".DS_Store", "Thumbs.db",
	".idea", ".vscode", ".vs", "*.egg-info", ".eggs",
}

// IsNoiseDir reports whether name is a noise directory.
func IsNoiseDir(name string) bool {
	for _, n := range NoiseDirs {
		if name == n {
			return true
		}
	}
	return false
}
