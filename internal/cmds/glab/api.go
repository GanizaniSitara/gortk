package glab

// ── API subcommand ──────────────────────────────────────────────────────

// runAPI passes `glab api ...` through unchanged. It is an explicit/advanced
// command — the user knows what they asked for, and converting the JSON to a
// schema would destroy all values and force a re-fetch. Passthrough preserves
// the full response.
func runAPI(args []string, _ int) (int, error) {
	return runPassthrough("glab", "api", args)
}
