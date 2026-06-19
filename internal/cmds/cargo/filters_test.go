package cargo

import (
	"strings"
	"testing"
)

// These tests are ported verbatim from rtk's src/cmds/rust/cargo_cmd.rs
// #[cfg(test)] module. The sample cargo output pins "rtk" literals (they are
// real cargo log lines, e.g. "Compiling rtk v0.5.0"), so those are preserved as
// in the source — they are not user-facing strings gortk authors. The expected
// compacted output (e.g. "cargo build: ...") matches rtk exactly.
//
// rtk's streaming-handler tests (test_cargo_build_stream_*, test_cargo_test_stream_*)
// assert the same outputs as the pure-function tests, because rtk's
// BlockStreamFilter handler and the pure filter_cargo_build/_test functions are
// behaviorally equivalent. gortk has no nested streaming framework, so those
// stream tests are folded in here against the pure functions.

// ---- filter_cargo_build -----------------------------------------------------

func TestFilterCargoBuildSuccess(t *testing.T) {
	output := "" +
		"   Compiling libc v0.2.153\n" +
		"   Compiling cfg-if v1.0.0\n" +
		"   Compiling rtk v0.5.0\n" +
		"    Finished dev [unoptimized + debuginfo] target(s) in 15.23s\n"
	result := filterCargoBuild(output)
	if !strings.Contains(result, "cargo build") {
		t.Errorf("missing 'cargo build': %s", result)
	}
	if !strings.Contains(result, "3 crates compiled") {
		t.Errorf("missing '3 crates compiled': %s", result)
	}
	// Stream-equivalent assertions: Finished kept, Compiling stripped.
	if !strings.Contains(result, "Finished") {
		t.Errorf("missing 'Finished': %s", result)
	}
	if strings.Contains(result, "Compiling") {
		t.Errorf("should not contain 'Compiling': %s", result)
	}
}

func TestFilterCargoBuildErrors(t *testing.T) {
	output := "" +
		"   Compiling rtk v0.5.0\n" +
		"error[E0308]: mismatched types\n" +
		" --> src/main.rs:10:5\n" +
		"  |\n" +
		"10|     \"hello\"\n" +
		"  |     ^^^^^^^ expected `i32`, found `&str`\n" +
		"\n" +
		"error: aborting due to 1 previous error\n"
	result := filterCargoBuild(output)
	if !strings.Contains(result, "1 errors") {
		t.Errorf("missing '1 errors': %s", result)
	}
	if !strings.Contains(result, "E0308") {
		t.Errorf("missing 'E0308': %s", result)
	}
	if !strings.Contains(result, "mismatched types") {
		t.Errorf("missing 'mismatched types': %s", result)
	}
	if strings.Contains(result, "aborting") {
		t.Errorf("should not contain 'aborting': %s", result)
	}
}

// ---- filter_cargo_test ------------------------------------------------------

func TestFilterCargoTestAllPass(t *testing.T) {
	output := "" +
		"   Compiling rtk v0.5.0\n" +
		"    Finished test [unoptimized + debuginfo] target(s) in 2.53s\n" +
		"     Running target/debug/deps/rtk-abc123\n" +
		"\n" +
		"running 15 tests\n" +
		"test utils::tests::test_truncate_short_string ... ok\n" +
		"test utils::tests::test_truncate_long_string ... ok\n" +
		"test utils::tests::test_strip_ansi_simple ... ok\n" +
		"\n" +
		"test result: ok. 15 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "cargo test: 15 passed (1 suite, 0.01s)") {
		t.Errorf("expected compact format, got: %s", result)
	}
	if strings.Contains(result, "Compiling") {
		t.Errorf("should not contain 'Compiling': %s", result)
	}
	if strings.Contains(result, "test utils") {
		t.Errorf("should not contain 'test utils': %s", result)
	}
}

func TestFilterCargoTestFailures(t *testing.T) {
	output := "" +
		"running 5 tests\n" +
		"test foo::test_a ... ok\n" +
		"test foo::test_b ... FAILED\n" +
		"test foo::test_c ... ok\n" +
		"\n" +
		"failures:\n" +
		"\n" +
		"---- foo::test_b stdout ----\n" +
		"thread 'foo::test_b' panicked at 'assert_eq!(1, 2)'\n" +
		"\n" +
		"failures:\n" +
		"    foo::test_b\n" +
		"\n" +
		"test result: FAILED. 4 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "FAILURES") {
		t.Errorf("missing 'FAILURES': %s", result)
	}
	if !strings.Contains(result, "test_b") {
		t.Errorf("missing 'test_b': %s", result)
	}
	if !strings.Contains(result, "test result:") {
		t.Errorf("missing 'test result:': %s", result)
	}
	// Stream-equivalent assertion: panic detail preserved.
	if !strings.Contains(result, "panicked") {
		t.Errorf("missing 'panicked': %s", result)
	}
}

func TestFilterCargoTestMultiSuiteAllPass(t *testing.T) {
	output := "" +
		"   Compiling rtk v0.5.0\n" +
		"    Finished test [unoptimized + debuginfo] target(s) in 2.53s\n" +
		"     Running unittests src/lib.rs (target/debug/deps/rtk-abc123)\n" +
		"\n" +
		"running 50 tests\n" +
		"test result: ok. 50 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.45s\n" +
		"\n" +
		"     Running unittests src/main.rs (target/debug/deps/rtk-def456)\n" +
		"\n" +
		"running 30 tests\n" +
		"test result: ok. 30 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.30s\n" +
		"\n" +
		"     Running tests/integration.rs (target/debug/deps/integration-ghi789)\n" +
		"\n" +
		"running 25 tests\n" +
		"test result: ok. 25 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.25s\n" +
		"\n" +
		"   Doc-tests rtk\n" +
		"\n" +
		"running 32 tests\n" +
		"test result: ok. 32 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.45s\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "cargo test: 137 passed (4 suites, 1.45s)") {
		t.Errorf("expected aggregated format, got: %s", result)
	}
	if strings.Contains(result, "running") {
		t.Errorf("should not contain 'running': %s", result)
	}
}

func TestFilterCargoTestMultiSuiteWithFailures(t *testing.T) {
	output := "" +
		"     Running unittests src/lib.rs\n" +
		"\n" +
		"running 20 tests\n" +
		"test result: ok. 20 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.10s\n" +
		"\n" +
		"     Running unittests src/main.rs\n" +
		"\n" +
		"running 15 tests\n" +
		"test foo::test_bad ... FAILED\n" +
		"\n" +
		"failures:\n" +
		"\n" +
		"---- foo::test_bad stdout ----\n" +
		"thread panicked at 'assertion failed'\n" +
		"\n" +
		"test result: FAILED. 14 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.05s\n" +
		"\n" +
		"     Running tests/integration.rs\n" +
		"\n" +
		"running 10 tests\n" +
		"test result: ok. 10 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.02s\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "FAILURES") {
		t.Errorf("should not aggregate with failures, got: %s", result)
	}
	if !strings.Contains(result, "test_bad") {
		t.Errorf("missing 'test_bad': %s", result)
	}
	if !strings.Contains(result, "test result:") {
		t.Errorf("missing 'test result:': %s", result)
	}
	for _, want := range []string{"20 passed", "14 passed", "10 passed"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterCargoTestAllSuitesZeroTests(t *testing.T) {
	output := "" +
		"     Running unittests src/empty1.rs\n" +
		"\n" +
		"running 0 tests\n" +
		"\n" +
		"test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
		"\n" +
		"     Running unittests src/empty2.rs\n" +
		"\n" +
		"running 0 tests\n" +
		"\n" +
		"test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
		"\n" +
		"     Running tests/empty3.rs\n" +
		"\n" +
		"running 0 tests\n" +
		"\n" +
		"test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "cargo test: 0 passed (3 suites, 0.00s)") {
		t.Errorf("expected compact format for zero tests, got: %s", result)
	}
}

func TestFilterCargoTestWithIgnoredAndFiltered(t *testing.T) {
	output := "" +
		"     Running unittests src/lib.rs\n" +
		"\n" +
		"running 50 tests\n" +
		"test result: ok. 45 passed; 0 failed; 3 ignored; 0 measured; 2 filtered out; finished in 0.50s\n" +
		"\n" +
		"     Running tests/integration.rs\n" +
		"\n" +
		"running 20 tests\n" +
		"test result: ok. 18 passed; 0 failed; 2 ignored; 0 measured; 0 filtered out; finished in 0.20s\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "cargo test: 63 passed, 5 ignored, 2 filtered out (2 suites, 0.70s)") {
		t.Errorf("expected compact format with ignored and filtered, got: %s", result)
	}
}

func TestFilterCargoTestSingleSuiteCompact(t *testing.T) {
	output := "" +
		"     Running unittests src/main.rs\n" +
		"\n" +
		"running 15 tests\n" +
		"test result: ok. 15 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "cargo test: 15 passed (1 suite, 0.01s)") {
		t.Errorf("expected singular 'suite', got: %s", result)
	}
}

func TestFilterCargoTestRegexFallback(t *testing.T) {
	output := "" +
		"     Running unittests src/main.rs\n" +
		"\n" +
		"running 15 tests\n" +
		"test result: MALFORMED LINE WITHOUT PROPER FORMAT\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "test result: MALFORMED") {
		t.Errorf("expected fallback format, got: %s", result)
	}
}

func TestFilterCargoTestCompileErrorPreservesErrorHeader(t *testing.T) {
	output := "" +
		"   Compiling rtk v0.31.0 (/workspace/projects/rtk)\n" +
		"error[E0425]: cannot find value `missing_symbol` in this scope\n" +
		" --> tests/repro_compile_fail.rs:3:13\n" +
		"  |\n" +
		"3 |     let _ = missing_symbol;\n" +
		"  |             ^^^^^^^^^^^^^^ not found in this scope\n" +
		"\n" +
		"For more information about this error, try `rustc --explain E0425`.\n" +
		"error: could not compile `rtk` (test \"repro_compile_fail\") due to 1 previous error\n"
	result := filterCargoTest(output)
	if !strings.Contains(result, "cargo test: 1 errors, 0 warnings (1 crates)") {
		t.Errorf("missing compile-error summary: %s", result)
	}
	if !strings.Contains(result, "error[E0425]") {
		t.Errorf("missing 'error[E0425]': %s", result)
	}
	if !strings.Contains(result, "--> tests/repro_compile_fail.rs:3:13") {
		t.Errorf("missing location: %s", result)
	}
	if strings.HasPrefix(result, "|") {
		t.Errorf("should not start with '|': %s", result)
	}
}

// ---- filter_cargo_clippy ----------------------------------------------------

func TestFilterCargoClippyClean(t *testing.T) {
	output := "" +
		"    Checking rtk v0.5.0\n" +
		"    Finished dev [unoptimized + debuginfo] target(s) in 1.53s\n"
	result := filterCargoClippy(output)
	if !strings.Contains(result, "cargo clippy: No issues found") {
		t.Errorf("missing clean message: %s", result)
	}
}

func TestFilterCargoClippyWarnings(t *testing.T) {
	output := "" +
		"    Checking rtk v0.5.0\n" +
		"warning: unused variable: `x` [unused_variables]\n" +
		" --> src/main.rs:10:9\n" +
		"  |\n" +
		"10|     let x = 5;\n" +
		"  |         ^ help: if this is intentional, prefix it with an underscore: `_x`\n" +
		"\n" +
		"warning: this function has too many arguments [clippy::too_many_arguments]\n" +
		" --> src/git.rs:16:1\n" +
		"  |\n" +
		"16| pub fn run(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32) {}\n" +
		"  |\n" +
		"\n" +
		"warning: `rtk` (bin) generated 2 warnings\n" +
		"    Finished dev [unoptimized + debuginfo] target(s) in 1.53s\n"
	result := filterCargoClippy(output)
	if !strings.Contains(result, "0 errors, 2 warnings") {
		t.Errorf("missing count: %s", result)
	}
	if !strings.Contains(result, "unused_variables") {
		t.Errorf("missing 'unused_variables': %s", result)
	}
	if !strings.Contains(result, "clippy::too_many_arguments") {
		t.Errorf("missing 'clippy::too_many_arguments': %s", result)
	}
}

func TestFilterCargoClippyIncludesErrorDetails(t *testing.T) {
	output := "" +
		"    Checking rtk v0.5.0\n" +
		"error: struct literals are not allowed here\n" +
		"warning: unused variable: `x` [unused_variables]\n" +
		"    Finished dev [unoptimized + debuginfo] target(s) in 1.53s\n"
	result := filterCargoClippy(output)
	if !strings.Contains(result, "cargo clippy: 1 errors, 1 warnings") {
		t.Errorf("missing count: %s", result)
	}
	if !strings.Contains(result, "Errors:") {
		t.Errorf("missing 'Errors:': %s", result)
	}
	if !strings.Contains(result, "struct literals are not allowed here") {
		t.Errorf("missing error detail: %s", result)
	}
}

func TestFilterCargoClippyShowsFullErrorBlock(t *testing.T) {
	output := "" +
		"    Checking rtk v0.5.0\n" +
		"error[E0308]: mismatched types\n" +
		" --> src/main.rs:10:5\n" +
		"  |\n" +
		"9 |     fn foo() -> i32 {\n" +
		"  |                 --- expected `i32` because of return type\n" +
		"10|     \"hello\"\n" +
		"  |     ^^^^^^^ expected `i32`, found `&str`\n" +
		"\n" +
		"error: aborting due to 1 previous error\n"
	result := filterCargoClippy(output)
	if !strings.Contains(result, "cargo clippy: 1 errors, 0 warnings") {
		t.Errorf("missing count: %s", result)
	}
	if !strings.Contains(result, "error[E0308]: mismatched types") {
		t.Errorf("missing error headline: %s", result)
	}
	if !strings.Contains(result, "src/main.rs:10:5") {
		t.Errorf("missing location: %s", result)
	}
	if !strings.Contains(result, "expected `i32`, found `&str`") {
		t.Errorf("missing context: %s", result)
	}
}

func TestFilterCargoClippyMultipleErrorsShowAllBlocks(t *testing.T) {
	output := "" +
		"error[E0308]: mismatched types\n" +
		" --> src/foo.rs:5:3\n" +
		"\n" +
		"error[E0425]: cannot find value `x`\n" +
		" --> src/bar.rs:12:9\n" +
		"\n" +
		"error: aborting due to 2 previous errors\n"
	result := filterCargoClippy(output)
	if !strings.Contains(result, "2 errors") {
		t.Errorf("missing '2 errors': %s", result)
	}
	if !strings.Contains(result, "src/foo.rs:5:3") {
		t.Errorf("missing first location: %s", result)
	}
	if !strings.Contains(result, "src/bar.rs:12:9") {
		t.Errorf("missing second location: %s", result)
	}
}

// ---- filter_cargo_install ---------------------------------------------------

func TestFilterCargoInstallSuccess(t *testing.T) {
	output := "" +
		"  Installing rtk v0.11.0\n" +
		"  Downloading crates ...\n" +
		"  Downloaded anyhow v1.0.80\n" +
		"  Downloaded clap v4.5.0\n" +
		"   Compiling libc v0.2.153\n" +
		"   Compiling cfg-if v1.0.0\n" +
		"   Compiling anyhow v1.0.80\n" +
		"   Compiling clap v4.5.0\n" +
		"   Compiling rtk v0.11.0\n" +
		"    Finished `release` profile [optimized] target(s) in 45.23s\n" +
		"  Replacing /Users/user/.cargo/bin/rtk\n" +
		"   Replaced package `rtk v0.9.4` with `rtk v0.11.0` (/Users/user/.cargo/bin/rtk)\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "cargo install") {
		t.Errorf("missing 'cargo install': %s", result)
	}
	if !strings.Contains(result, "rtk v0.11.0") {
		t.Errorf("missing crate info: %s", result)
	}
	if !strings.Contains(result, "5 deps compiled") {
		t.Errorf("missing dep count: %s", result)
	}
	if !strings.Contains(result, "Replaced") {
		t.Errorf("missing 'Replaced': %s", result)
	}
	if strings.Contains(result, "Compiling") {
		t.Errorf("should not contain 'Compiling': %s", result)
	}
	if strings.Contains(result, "Downloading") {
		t.Errorf("should not contain 'Downloading': %s", result)
	}
}

func TestFilterCargoInstallReplace(t *testing.T) {
	output := "" +
		"  Installing rtk v0.11.0\n" +
		"   Compiling rtk v0.11.0\n" +
		"    Finished `release` profile [optimized] target(s) in 10.0s\n" +
		"  Replacing /Users/user/.cargo/bin/rtk\n" +
		"   Replaced package `rtk v0.9.4` with `rtk v0.11.0` (/Users/user/.cargo/bin/rtk)\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "cargo install") {
		t.Errorf("missing 'cargo install': %s", result)
	}
	if !strings.Contains(result, "Replacing") {
		t.Errorf("missing 'Replacing': %s", result)
	}
	if !strings.Contains(result, "Replaced") {
		t.Errorf("missing 'Replaced': %s", result)
	}
}

func TestFilterCargoInstallError(t *testing.T) {
	output := "" +
		"  Installing rtk v0.11.0\n" +
		"   Compiling rtk v0.11.0\n" +
		"error[E0308]: mismatched types\n" +
		" --> src/main.rs:10:5\n" +
		"  |\n" +
		"10|     \"hello\"\n" +
		"  |     ^^^^^^^ expected `i32`, found `&str`\n" +
		"\n" +
		"error: aborting due to 1 previous error\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "cargo install: 1 error") {
		t.Errorf("missing '1 error': %s", result)
	}
	if !strings.Contains(result, "E0308") {
		t.Errorf("missing 'E0308': %s", result)
	}
	if !strings.Contains(result, "mismatched types") {
		t.Errorf("missing 'mismatched types': %s", result)
	}
	if strings.Contains(result, "aborting") {
		t.Errorf("should not contain 'aborting': %s", result)
	}
}

func TestFilterCargoInstallAlreadyInstalled(t *testing.T) {
	output := "  Ignored package `rtk v0.11.0`, is already installed\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "already installed") {
		t.Errorf("missing 'already installed': %s", result)
	}
	if !strings.Contains(result, "rtk v0.11.0") {
		t.Errorf("missing crate info: %s", result)
	}
}

func TestFilterCargoInstallUpToDate(t *testing.T) {
	output := "  Ignored package `cargo-deb v2.1.0 (/Users/user/cargo-deb)`, is already installed\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "already installed") {
		t.Errorf("missing 'already installed': %s", result)
	}
	if !strings.Contains(result, "cargo-deb v2.1.0") {
		t.Errorf("missing crate info: %s", result)
	}
}

func TestFilterCargoInstallEmptyOutput(t *testing.T) {
	result := filterCargoInstall("")
	if !strings.Contains(result, "cargo install") {
		t.Errorf("missing 'cargo install': %s", result)
	}
	if !strings.Contains(result, "0 deps compiled") {
		t.Errorf("missing '0 deps compiled': %s", result)
	}
}

func TestFilterCargoInstallPathWarning(t *testing.T) {
	output := "" +
		"  Installing rtk v0.11.0\n" +
		"   Compiling rtk v0.11.0\n" +
		"    Finished `release` profile [optimized] target(s) in 10.0s\n" +
		"  Replacing /Users/user/.cargo/bin/rtk\n" +
		"   Replaced package `rtk v0.9.4` with `rtk v0.11.0` (/Users/user/.cargo/bin/rtk)\n" +
		"warning: be sure to add `/Users/user/.cargo/bin` to your PATH\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "cargo install") {
		t.Errorf("missing 'cargo install': %s", result)
	}
	if !strings.Contains(result, "be sure to add") {
		t.Errorf("PATH warning should be kept: %s", result)
	}
	if !strings.Contains(result, "Replaced") {
		t.Errorf("missing 'Replaced': %s", result)
	}
}

func TestFilterCargoInstallMultipleErrors(t *testing.T) {
	output := "" +
		"  Installing rtk v0.11.0\n" +
		"   Compiling rtk v0.11.0\n" +
		"error[E0308]: mismatched types\n" +
		" --> src/main.rs:10:5\n" +
		"  |\n" +
		"10|     \"hello\"\n" +
		"  |     ^^^^^^^ expected `i32`, found `&str`\n" +
		"\n" +
		"error[E0425]: cannot find value `foo`\n" +
		" --> src/lib.rs:20:9\n" +
		"  |\n" +
		"20|     foo\n" +
		"  |     ^^^ not found in this scope\n" +
		"\n" +
		"error: aborting due to 2 previous errors\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "2 errors") {
		t.Errorf("should show 2 errors: %s", result)
	}
	if !strings.Contains(result, "E0308") {
		t.Errorf("missing 'E0308': %s", result)
	}
	if !strings.Contains(result, "E0425") {
		t.Errorf("missing 'E0425': %s", result)
	}
	if strings.Contains(result, "aborting") {
		t.Errorf("should not contain 'aborting': %s", result)
	}
}

func TestFilterCargoInstallLockingAndBlocking(t *testing.T) {
	output := "" +
		"  Locking 45 packages to latest compatible versions\n" +
		"  Blocking waiting for file lock on package cache\n" +
		"  Downloading crates ...\n" +
		"  Downloaded serde v1.0.200\n" +
		"   Compiling serde v1.0.200\n" +
		"   Compiling rtk v0.11.0\n" +
		"    Finished `release` profile [optimized] target(s) in 30.0s\n" +
		"  Installing rtk v0.11.0\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "cargo install") {
		t.Errorf("missing 'cargo install': %s", result)
	}
	for _, bad := range []string{"Locking", "Blocking", "Downloading"} {
		if strings.Contains(result, bad) {
			t.Errorf("should strip %q: %s", bad, result)
		}
	}
}

func TestFilterCargoInstallFromPath(t *testing.T) {
	output := "" +
		"  Installing /Users/user/projects/rtk\n" +
		"   Compiling rtk v0.11.0\n" +
		"    Finished `release` profile [optimized] target(s) in 10.0s\n"
	result := filterCargoInstall(output)
	if !strings.Contains(result, "cargo install") {
		t.Errorf("missing 'cargo install': %s", result)
	}
	if !strings.Contains(result, "1 deps compiled") {
		t.Errorf("missing '1 deps compiled': %s", result)
	}
}

func TestFormatCrateInfo(t *testing.T) {
	cases := []struct {
		name, version, fallback, want string
	}{
		{"rtk", "v0.11.0", "", "rtk v0.11.0"},
		{"rtk", "", "", "rtk"},
		{"", "", "package", "package"},
		{"", "v0.1.0", "fallback", "fallback"},
	}
	for _, c := range cases {
		if got := formatCrateInfo(c.name, c.version, c.fallback); got != c.want {
			t.Errorf("formatCrateInfo(%q,%q,%q) = %q, want %q", c.name, c.version, c.fallback, got, c.want)
		}
	}
}

// ---- filter_cargo_nextest ---------------------------------------------------

func TestFilterCargoNextestAllPass(t *testing.T) {
	output := "" +
		"   Compiling rtk v0.15.2\n" +
		"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.04s\n" +
		"────────────────────────────\n" +
		"    Starting 301 tests across 1 binary\n" +
		"        PASS [   0.009s] (1/301) rtk::bin/rtk cargo_cmd::tests::test_one\n" +
		"        PASS [   0.008s] (2/301) rtk::bin/rtk cargo_cmd::tests::test_two\n" +
		"        PASS [   0.007s] (301/301) rtk::bin/rtk cargo_cmd::tests::test_last\n" +
		"────────────────────────────\n" +
		"     Summary [   0.192s] 301 tests run: 301 passed, 0 skipped\n"
	result := filterCargoNextest(output)
	want := "cargo nextest: 301 passed (1 binary, 0.192s)"
	if result != want {
		t.Errorf("got: %q, want %q", result, want)
	}
}

func TestFilterCargoNextestWithFailures(t *testing.T) {
	output := "" +
		"    Starting 4 tests across 1 binary (1 test skipped)\n" +
		"        PASS [   0.006s] (1/4) test-proj tests::passing_test\n" +
		"        FAIL [   0.006s] (2/4) test-proj tests::failing_test\n" +
		"\n" +
		"  stderr ───\n" +
		"\n" +
		"    thread 'tests::failing_test' panicked at src/lib.rs:15:9:\n" +
		"    assertion `left == right` failed\n" +
		"      left: 1\n" +
		"     right: 2\n" +
		"\n" +
		"  Cancelling due to test failure: 2 tests still running\n" +
		"        PASS [   0.007s] (3/4) test-proj tests::another_passing\n" +
		"        FAIL [   0.006s] (4/4) test-proj tests::another_failing\n" +
		"\n" +
		"  stderr ───\n" +
		"\n" +
		"    thread 'tests::another_failing' panicked at src/lib.rs:20:9:\n" +
		"    something went wrong\n" +
		"\n" +
		"────────────────────────────\n" +
		"     Summary [   0.007s] 4 tests run: 2 passed, 2 failed, 1 skipped\n" +
		"        FAIL [   0.006s] (2/4) test-proj tests::failing_test\n" +
		"        FAIL [   0.006s] (4/4) test-proj tests::another_failing\n" +
		"error: test run failed\n"
	result := filterCargoNextest(output)
	if !strings.Contains(result, "tests::failing_test") {
		t.Errorf("should contain first failure: %s", result)
	}
	if !strings.Contains(result, "tests::another_failing") {
		t.Errorf("should contain second failure: %s", result)
	}
	if !strings.Contains(result, "panicked") {
		t.Errorf("should contain stderr detail: %s", result)
	}
	if !strings.Contains(result, "2 passed, 2 failed, 1 skipped") {
		t.Errorf("should contain summary: %s", result)
	}
	if strings.Contains(result, "PASS") {
		t.Errorf("should not contain PASS lines: %s", result)
	}
	if got := strings.Count(result, "FAIL ["); got != 2 {
		t.Errorf("should have exactly 2 FAIL headers, got %d: %s", got, result)
	}
	if strings.Contains(result, "error: test run failed") {
		t.Errorf("should not contain post-summary error line: %s", result)
	}
}

func TestFilterCargoNextestWithSkipped(t *testing.T) {
	output := "" +
		"    Starting 50 tests across 2 binaries (3 tests skipped)\n" +
		"        PASS [   0.010s] (1/50) rtk::bin/rtk test_one\n" +
		"        PASS [   0.010s] (50/50) rtk::bin/rtk test_last\n" +
		"────────────────────────────\n" +
		"     Summary [   0.500s] 50 tests run: 50 passed, 3 skipped\n"
	result := filterCargoNextest(output)
	want := "cargo nextest: 50 passed, 3 skipped (2 binaries, 0.500s)"
	if result != want {
		t.Errorf("got: %q, want %q", result, want)
	}
}

func TestFilterCargoNextestSingleFailureDetail(t *testing.T) {
	output := "" +
		"    Starting 2 tests across 1 binary\n" +
		"        PASS [   0.005s] (1/2) proj tests::good\n" +
		"        FAIL [   0.005s] (2/2) proj tests::bad\n" +
		"\n" +
		"  stderr ───\n" +
		"\n" +
		"    thread 'tests::bad' panicked at src/lib.rs:5:9:\n" +
		"    assertion failed: false\n" +
		"\n" +
		"────────────────────────────\n" +
		"     Summary [   0.010s] 2 tests run: 1 passed, 1 failed\n" +
		"        FAIL [   0.005s] (2/2) proj tests::bad\n" +
		"error: test run failed\n"
	result := filterCargoNextest(output)
	if !strings.Contains(result, "assertion failed: false") {
		t.Errorf("should show panic message: %s", result)
	}
	if !strings.Contains(result, "1 passed, 1 failed") {
		t.Errorf("should show summary: %s", result)
	}
	if got := strings.Count(result, "FAIL ["); got != 1 {
		t.Errorf("should have exactly 1 FAIL header, got %d: %s", got, result)
	}
}

func TestFilterCargoNextestMultipleBinaries(t *testing.T) {
	output := "" +
		"    Starting 100 tests across 5 binaries\n" +
		"        PASS [   0.010s] (100/100) test_last\n" +
		"────────────────────────────\n" +
		"     Summary [   1.234s] 100 tests run: 100 passed, 0 skipped\n"
	result := filterCargoNextest(output)
	want := "cargo nextest: 100 passed (5 binaries, 1.234s)"
	if result != want {
		t.Errorf("got: %q, want %q", result, want)
	}
}

func TestFilterCargoNextestCompilationStripped(t *testing.T) {
	output := "" +
		"   Compiling serde v1.0.200\n" +
		"   Compiling rtk v0.15.2\n" +
		"   Downloading crates ...\n" +
		"    Finished `test` profile [unoptimized + debuginfo] target(s) in 5.00s\n" +
		"────────────────────────────\n" +
		"    Starting 10 tests across 1 binary\n" +
		"        PASS [   0.010s] (10/10) test_last\n" +
		"────────────────────────────\n" +
		"     Summary [   0.050s] 10 tests run: 10 passed, 0 skipped\n"
	result := filterCargoNextest(output)
	for _, bad := range []string{"Compiling", "Downloading", "Finished"} {
		if strings.Contains(result, bad) {
			t.Errorf("should strip %q: %s", bad, result)
		}
	}
	if !strings.Contains(result, "cargo nextest: 10 passed") {
		t.Errorf("missing summary: %s", result)
	}
}

func TestFilterCargoNextestEmpty(t *testing.T) {
	result := filterCargoNextest("")
	if result != "" {
		t.Errorf("got: %q, want empty", result)
	}
}

func TestFilterCargoNextestCancellationNotice(t *testing.T) {
	output := "" +
		"    Starting 3 tests across 1 binary\n" +
		"        FAIL [   0.005s] (1/3) proj tests::bad\n" +
		"\n" +
		"  stderr ───\n" +
		"\n" +
		"    thread panicked at 'oops'\n" +
		"\n" +
		"  Cancelling due to test failure: 2 tests still running\n" +
		"────────────────────────────\n" +
		"     Summary [   0.010s] 3 tests run: 2 passed, 1 failed\n" +
		"        FAIL [   0.005s] (1/3) proj tests::bad\n" +
		"error: test run failed\n"
	result := filterCargoNextest(output)
	if !strings.Contains(result, "Cancelling due to test failure") {
		t.Errorf("should include cancel notice: %s", result)
	}
	if !strings.Contains(result, "1 failed") {
		t.Errorf("should show failure count: %s", result)
	}
	if got := strings.Count(result, "FAIL ["); got != 1 {
		t.Errorf("should have exactly 1 FAIL header, got %d: %s", got, result)
	}
}

func TestFilterCargoNextestSummaryRegexFallback(t *testing.T) {
	output := "" +
		"    Starting 5 tests across 1 binary\n" +
		"        PASS [   0.005s] (5/5) test_last\n" +
		"────────────────────────────\n" +
		"     Summary MALFORMED LINE\n"
	result := filterCargoNextest(output)
	if !strings.Contains(result, "Summary MALFORMED") {
		t.Errorf("should fall back to raw summary: %s", result)
	}
}
