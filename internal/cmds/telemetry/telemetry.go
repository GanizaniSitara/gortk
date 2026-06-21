// Package telemetry implements gortk's `telemetry` command: the operator-facing
// controls for the optional, opt-in usage ping.
//
// gortk is offline by default. Telemetry is the ONE deliberate exception to the
// no-network rule, and it stays OFF and silent unless an enterprise explicitly
// enables it AND points it at their OWN sink endpoint. There is no compile-time
// URL and no vendor phone-home; the destination always comes from config.toml.
//
// Subcommands:
//
//   - status (default) : show whether telemetry is enabled, the configured
//     endpoint, and whether a token is set (masked).
//   - enable --endpoint <url> [--token <t>] : write enabled=true + endpoint
//     (+token) into config.toml, preserving any existing keys.
//   - disable : set enabled=false in config.toml.
//   - preview : print the EXACT aggregate JSON payload that WOULD be sent,
//     without sending it, so an enterprise can audit what they'd collect.
package telemetry

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "telemetry",
		Summary: "Manage gortk's optional, opt-in usage telemetry",
		Run:     Run,
	})
}

// Run dispatches the telemetry subcommands. With no args it shows status.
func Run(args []string, verbose int) (int, error) {
	sub := "status"
	rest := args
	if len(args) > 0 {
		sub = args[0]
		rest = args[1:]
	}

	switch sub {
	case "status", "--status":
		return runStatus()
	case "enable":
		return runEnable(rest)
	case "disable":
		return runDisable()
	case "preview":
		return runPreview()
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0, nil
	default:
		fmt.Fprintf(os.Stderr, "gortk telemetry: unknown subcommand %q\n\n%s", sub, usage)
		return 2, nil
	}
}

const usage = `usage: gortk telemetry <status|enable|disable|preview>

  status                          show enabled state, endpoint, token presence (default)
  enable --endpoint <url> [--token <t>]
                                  opt in and point telemetry at YOUR sink
  disable                         opt out (enabled=false)
  preview                         print the exact JSON payload that WOULD be sent

Telemetry is OFF by default and sends nothing unless enabled with an endpoint.
The env var GORTK_TELEMETRY_DISABLED=1 force-disables sending regardless of config.
`

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func runStatus() (int, error) {
	cfg := core.LoadConfig()
	t := cfg.Telemetry

	enabled := "no"
	if t.Enabled {
		enabled = "yes"
	}
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = "(none)"
	}
	token := "no"
	if t.Token != "" {
		token = "yes (" + maskToken(t.Token) + ")"
	}

	fmt.Println("gortk telemetry status:")
	fmt.Printf("  enabled:   %s\n", enabled)
	fmt.Printf("  endpoint:  %s\n", endpoint)
	fmt.Printf("  token set: %s\n", token)
	if os.Getenv("GORTK_TELEMETRY_DISABLED") == "1" {
		fmt.Println("  env:       GORTK_TELEMETRY_DISABLED=1 (sending force-disabled)")
	}
	fmt.Printf("  config:    %s\n", core.ConfigPath())
	fmt.Println()
	fmt.Println("Telemetry is off by default and sends only aggregate stats to YOUR")
	fmt.Println("configured sink. Run `gortk telemetry preview` to see the exact payload.")
	return 0, nil
}

// maskToken reveals at most the first and last two chars, masking the middle so
// status output never prints a usable credential.
func maskToken(tok string) string {
	switch n := len(tok); {
	case n == 0:
		return ""
	case n <= 4:
		return strings.Repeat("*", n)
	default:
		return tok[:2] + strings.Repeat("*", n-4) + tok[n-2:]
	}
}

// ---------------------------------------------------------------------------
// enable / disable
// ---------------------------------------------------------------------------

func runEnable(args []string) (int, error) {
	endpoint := ""
	token := ""
	haveToken := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--endpoint":
			if i+1 < len(args) {
				endpoint = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--endpoint="):
			endpoint = strings.TrimPrefix(a, "--endpoint=")
		case a == "--token":
			if i+1 < len(args) {
				token = args[i+1]
				haveToken = true
				i++
			}
		case strings.HasPrefix(a, "--token="):
			token = strings.TrimPrefix(a, "--token=")
			haveToken = true
		}
	}

	if strings.TrimSpace(endpoint) == "" {
		fmt.Fprintln(os.Stderr, "gortk telemetry enable: --endpoint <url> is required (no built-in endpoint).")
		return 2, nil
	}

	updates := map[string]string{
		"enabled":  "true",
		"endpoint": quoteTOML(endpoint),
	}
	if haveToken {
		updates["token"] = quoteTOML(token)
	}

	path := core.ConfigPath()
	if err := mergeTelemetry(path, updates); err != nil {
		return 1, fmt.Errorf("gortk telemetry enable: %w", err)
	}

	fmt.Printf("Telemetry enabled. Endpoint: %s\n", endpoint)
	if haveToken {
		fmt.Println("Bearer token stored in config.toml.")
	}
	fmt.Printf("Config: %s\n", path)
	fmt.Println("Disable anytime: gortk telemetry disable")
	return 0, nil
}

func runDisable() (int, error) {
	path := core.ConfigPath()
	if err := mergeTelemetry(path, map[string]string{"enabled": "false"}); err != nil {
		return 1, fmt.Errorf("gortk telemetry disable: %w", err)
	}
	fmt.Println("Telemetry disabled.")
	fmt.Printf("Config: %s\n", path)
	return 0, nil
}

// ---------------------------------------------------------------------------
// preview
// ---------------------------------------------------------------------------

func runPreview() (int, error) {
	body, err := core.BuildPayload()
	if err != nil {
		return 1, fmt.Errorf("gortk telemetry preview: %w", err)
	}
	fmt.Println("This is the exact aggregate-only payload gortk WOULD send.")
	fmt.Println("Nothing was sent. No command strings, paths, or secrets are included.")
	fmt.Println()
	fmt.Println(string(body))
	return 0, nil
}
