// Package wget is gortk's compact wget wrapper. It runs the native `wget`
// tool, captures its output, strips the noisy progress bars, and prints a
// single-line result (or a compact head when piping to stdout). Faithful port
// of rtk's src/cmds/cloud/wget_cmd.rs.
//
// On Windows this resolves a `wget` / `wget.exe` on PATH (PATHEXT-aware via
// core.ResolvedCommand).
package wget

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "wget",
		Summary: "Download with compact output (strips progress bars)",
		Run:     Run,
	})
}

// Run parses the wget invocation. In rtk, clap splits this into a required
// `url`, an optional `-O/--output-document`, and trailing `args`. We mirror
// that dispatch here: the first non-flag token is the URL, `-O -` routes to the
// stdout path, and any `-O <file>` is threaded back through to wget.
func Run(args []string, verbose int) (int, error) {
	url, output, rest, ok := parseArgs(args)
	if !ok {
		fmt.Fprintln(os.Stderr, "gortk wget: missing URL")
		return 2, nil
	}

	if output == "-" {
		return runStdout(url, rest, verbose)
	}

	var allArgs []string
	if output != "" {
		allArgs = append(allArgs, "-O", output)
	}
	allArgs = append(allArgs, rest...)
	return runDownload(url, allArgs, verbose)
}

// parseArgs separates the URL, an explicit -O/--output-document value, and the
// remaining wget args from the raw argv. Returns ok=false when no URL is found.
func parseArgs(args []string) (url, output string, rest []string, ok bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-O" || a == "--output-document":
			if i+1 < len(args) {
				output = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "-O") && len(a) > 2:
			output = a[2:]
		case strings.HasPrefix(a, "--output-document="):
			output = strings.TrimPrefix(a, "--output-document=")
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		case url == "":
			url = a
		default:
			rest = append(rest, a)
		}
	}
	return url, output, rest, url != ""
}

// runDownload runs wget normally, capturing output to parse it, and prints a
// single compact result line.
func runDownload(url string, args []string, verbose int) (int, error) {
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "wget: %s\n", url)
	}

	cmdArgs := append(append([]string{}, args...), url)
	cmd := core.ResolvedCommand("wget", cmdArgs...)
	stdout, stderr, exit, startErr := capture(cmd)
	if startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run wget: %w", startErr)
	}

	if exit == 0 {
		filename := extractFilenameFromOutput(stderr, url, args)
		size := getFileSize(filename)
		fmt.Printf("%s ok | %s | %s\n", compactURL(url), filename, formatSize(size))
		return 0, nil
	}

	errMsg := parseError(stderr, stdout)
	fmt.Printf("%s FAILED: %s\n", compactURL(url), errMsg)
	return exit, nil
}

// runStdout runs wget with `-q -O -` and emits a compact head of the body so a
// large download piped to stdout does not flood the agent's context.
func runStdout(url string, args []string, verbose int) (int, error) {
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "wget: %s -> stdout\n", url)
	}

	cmdArgs := append([]string{"-q", "-O", "-"}, args...)
	cmdArgs = append(cmdArgs, url)
	cmd := core.ResolvedCommand("wget", cmdArgs...)
	stdout, stderr, exit, startErr := capture(cmd)
	if startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run wget: %w", startErr)
	}

	if exit == 0 {
		fmt.Print(compactStdout(url, stdout))
		return 0, nil
	}

	errMsg := parseError(stderr, "")
	fmt.Printf("%s FAILED: %s\n", compactURL(url), errMsg)
	return exit, nil
}

// capture runs cmd with stdout/stderr buffered (newline-normalized) and returns
// them plus the exit code. The only process spawned is the wrapped wget. We
// keep stdout and stderr split because wget writes progress/result lines to
// stderr (used for filename extraction) and body content to stdout.
func capture(cmd *exec.Cmd) (stdout, stderr string, exitCode int, startErr error) {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout = core.NormalizeNewlines(outBuf.String())
	stderr = core.NormalizeNewlines(errBuf.String())
	exitCode = core.ExitCodeFromError(err)
	if exitCode == 127 {
		startErr = err
	}
	return stdout, stderr, exitCode, startErr
}
