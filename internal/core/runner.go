package core

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunOptions tunes how a wrapped command's output is captured and printed.
// Mirrors rtk's core::runner::RunOptions.
type RunOptions struct {
	// TeeLabel, when set, appends a "full output saved" hint on failure so an
	// agent can re-read the unfiltered output if the compressed form is
	// insufficient.
	TeeLabel string
	// FilterStdoutOnly filters only stdout; stderr is passed through verbatim.
	FilterStdoutOnly bool
	// SkipFilterOnFailure prints raw output (no filtering) when the command
	// exits non-zero.
	SkipFilterOnFailure bool
	// NoTrailingNewline suppresses the trailing newline after filtered output.
	NoTrailingNewline bool
	// InheritStdin forwards gortk's stdin to the child (needed for pipe-style
	// commands such as `cat file | gortk wc`).
	InheritStdin bool
}

// captureResult holds the outcome of running a child process.
type captureResult struct {
	stdout   string
	stderr   string
	raw      string // stdout followed by stderr, newline-normalized
	exitCode int
	startErr error
}

func capture(cmd *exec.Cmd, inheritStdin bool) captureResult {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if inheritStdin {
		cmd.Stdin = os.Stdin
	}

	err := cmd.Run()
	stdout := NormalizeNewlines(outBuf.String())
	stderr := NormalizeNewlines(errBuf.String())

	res := captureResult{
		stdout:   stdout,
		stderr:   stderr,
		raw:      stdout + stderr,
		exitCode: ExitCodeFromError(err),
	}
	if res.exitCode == 127 {
		res.startErr = err
	}
	return res
}

// RunFiltered executes cmd, captures its output, applies filter, prints the
// result, and records token savings. Returns the child's exit code.
func RunFiltered(cmd *exec.Cmd, tool, argsDisplay string, filter func(raw string) string, opts RunOptions) (int, error) {
	return RunFilteredWithExit(cmd, tool, argsDisplay, func(raw string, _ int) string {
		return filter(raw)
	}, opts)
}

// RunFilteredWithExit is RunFiltered but the filter also receives the exit code.
func RunFilteredWithExit(cmd *exec.Cmd, tool, argsDisplay string, filter func(raw string, exit int) string, opts RunOptions) (int, error) {
	timer := StartTimer()
	cmdLabel := strings.TrimSpace(tool + " " + argsDisplay)

	res := capture(cmd, opts.InheritStdin)
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run %s: %w", tool, res.startErr)
	}

	if opts.SkipFilterOnFailure && res.exitCode != 0 {
		if strings.TrimSpace(res.stdout) != "" {
			fmt.Print(res.stdout)
		}
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprint(os.Stderr, res.stderr)
		}
		timer.Track(cmdLabel, "gortk "+cmdLabel, res.raw, res.raw)
		return res.exitCode, nil
	}

	textToFilter := res.raw
	if opts.FilterStdoutOnly {
		textToFilter = res.stdout
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprint(os.Stderr, res.stderr)
		}
	}

	filtered := filter(textToFilter, res.exitCode)

	if opts.NoTrailingNewline {
		fmt.Print(filtered)
	} else {
		fmt.Println(filtered)
	}
	if hint := teeHint(res.raw, opts.TeeLabel, res.exitCode); hint != "" {
		fmt.Println(hint)
	}

	timer.Track(cmdLabel, "gortk "+cmdLabel, textToFilter, filtered)
	return res.exitCode, nil
}

// RunPassthrough executes a tool with no filtering, streaming stdio directly,
// while still recording that it ran.
func RunPassthrough(tool string, args []string, verbose int) (int, error) {
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "%s passthrough: %v\n", tool, args)
	}
	cmd := ResolvedCommand(tool, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	timer := StartTimer()
	err := cmd.Run()
	code := ExitCodeFromError(err)
	timer.TrackPassthrough(tool+" "+strings.Join(args, " "), fmt.Sprintf("gortk %s (passthrough)", tool))
	if code == 127 {
		return 127, fmt.Errorf("gortk: %w", err)
	}
	return code, nil
}

// teeHint writes the raw output to a tee file on failure and returns a hint
// line pointing at it, or "" when no tee is warranted. Best-effort.
func teeHint(raw, label string, exitCode int) string {
	if label == "" || exitCode == 0 || strings.TrimSpace(raw) == "" {
		return ""
	}
	path := teeFilePath(label)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		return ""
	}
	return fmt.Sprintf("[gortk: full output saved to %s — re-read if the filtered view is insufficient]", path)
}

func teeFilePath(label string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, label)
	if safe == "" {
		safe = "cmd"
	}
	return fmt.Sprintf("%s/tee-%s.txt", DataDir(), safe)
}
