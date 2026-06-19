package gradlew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countTokens mirrors the Rust helper count_tokens: whitespace-separated words.
func countTokens(text string) int {
	return len(strings.Fields(text))
}

// loadFixture reads a copied rtk fixture from testdata/, normalizing newlines so
// the line-oriented filters behave identically on Windows checkouts (CRLF).
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
}

// buildFilterJoin mirrors the Rust `input.lines().filter(filter_build_line)` then
// joined with "\n" — used by the build fixture/format tests.
func buildFilterJoin(input string) string {
	var kept []string
	for _, l := range splitLines(input) {
		if filterBuildLine(l) {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// ── TASK DETECTION ────────────────────────────────────────────────────────────

func TestDetectTask(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want gradlewTask
	}{
		{"connected_wins_over_test", []string{"connectedDebugAndroidTest"}, taskConnectedTest},
		{"assemble_debug", []string{"assembleDebug"}, taskBuild},
		{"test_debug_unit_test", []string{"testDebugUnitTest"}, taskTest},
		{"module_prefixed_task", []string{":app:testDebugUnitTest"}, taskTest},
		{"module_prefixed_assemble", []string{":app:assembleDebug"}, taskBuild},
		{"flag_value_does_not_trigger_test", []string{"assembleRelease", "-Pflavor=testRelease"}, taskBuild},
		{"multi_task_uses_last", []string{"clean", "assembleDebug"}, taskBuild},
		{"lint", []string{"lint"}, taskLint},
		{"ktlint", []string{"ktlintCheck"}, taskLint},
		{"bundle", []string{"bundleRelease"}, taskBuild},
		{"unknown_passthrough", []string{"signingReport"}, taskOther},
		{"clean_alone_is_build", []string{"clean"}, taskBuild},
		{"install_debug", []string{"installDebug"}, taskBuild},
		{"uninstall_debug", []string{"uninstallDebug"}, taskBuild},
		{"clean_install", []string{"clean", "installDebug"}, taskBuild},
		{"check", []string{"check"}, taskTest},
		{"dependencies", []string{"dependencies"}, taskDependencies},
		{"dependencies_with_module", []string{":app:dependencies"}, taskDependencies},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectTask(c.args); got != c.want {
				t.Errorf("detectTask(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}

// ── BUILD FILTER ────────────────────────────────────────────────────────────

func TestBuildSuccessStripsTaskLines(t *testing.T) {
	input := `> Configure project :app
> Task :app:preBuild UP-TO-DATE
> Task :app:generateDebugBuildConfig UP-TO-DATE
> Task :app:generateDebugResValues UP-TO-DATE
> Task :app:generateDebugResources UP-TO-DATE
> Task :app:mergeDebugResources UP-TO-DATE
> Task :app:processDebugManifest UP-TO-DATE
> Task :app:compileDebugKotlin UP-TO-DATE
> Task :app:compileDebugJavaWithJavac UP-TO-DATE
> Task :app:validateSigningDebug UP-TO-DATE
> Task :app:packageDebug UP-TO-DATE
> Task :app:assembleDebug UP-TO-DATE

BUILD SUCCESSFUL in 1m 23s
42 actionable tasks: 42 executed`

	out := buildFilterJoin(input)
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 70.0 {
		t.Errorf("expected >=70%% savings, got %.1f%%", savings)
	}
	if !strings.Contains(out, "BUILD SUCCESSFUL") {
		t.Errorf("must keep BUILD SUCCESSFUL: %s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "> Task :") {
			t.Errorf("task line not stripped: %s", l)
		}
	}
}

func TestBuildFailurePreservesErrorsStripsTry(t *testing.T) {
	input := `> Task :app:compileDebugKotlin FAILED

FAILURE: Build failed with an exception.

* What went wrong:
e: /src/app/MainActivity.kt: (42, 5): Unresolved reference: MyService

* Try:
> Run with --stacktrace option to get the stack trace.
> Run with --info or --debug option to get more log output.
> Get more help at https://help.gradle.org

BUILD FAILED in 12s`

	out := buildFilterJoin(input)
	if !strings.Contains(out, "Unresolved reference") {
		t.Errorf("must keep error: %s", out)
	}
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("must keep BUILD FAILED: %s", out)
	}
	if strings.Contains(out, "Run with --stacktrace") {
		t.Errorf("must strip Try section: %s", out)
	}
	if strings.Contains(out, "Get more help at") {
		t.Errorf("must strip Get more help: %s", out)
	}
}

func TestBuildFilterNeverEmptyOnSuccess(t *testing.T) {
	input := `> Task :app:assembleDebug UP-TO-DATE
BUILD SUCCESSFUL in 3s
1 actionable tasks: 1 up-to-date`
	out := buildFilterJoin(input)
	if out == "" {
		t.Errorf("filter must not produce empty output on success")
	}
}

func TestBuildDaemonLinesStripped(t *testing.T) {
	input := `Starting a Gradle Daemon (subsequent builds will be faster)
Daemon will be stopped at the end of the build after running out of JVM memory
> Task :app:assembleDebug
BUILD SUCCESSFUL in 5s`
	out := buildFilterJoin(input)
	if strings.Contains(out, "Daemon") {
		t.Errorf("daemon lines must be stripped: %s", out)
	}
	if !strings.Contains(out, "BUILD SUCCESSFUL") {
		t.Errorf("must keep BUILD SUCCESSFUL: %s", out)
	}
}

func TestBuildScanURLPreserved(t *testing.T) {
	input := `> Task :app:assembleDebug
BUILD SUCCESSFUL in 5s
Publishing build scan...
https://gradle.com/s/abc123`
	out := buildFilterJoin(input)
	if !strings.Contains(out, "gradle.com/s/") {
		t.Errorf("build scan URL must be preserved: %s", out)
	}
}

func TestBuildFilterKeepsCompilerWarnings(t *testing.T) {
	input := `> Task :app:compileDebugKotlin
w: /src/Foo.kt: (42, 5): Parameter 'unused' is never used
warning: [options] bootstrap class path not set
Warning: Gradle deprecation detected

BUILD SUCCESSFUL in 4s`
	out := buildFilterJoin(input)
	if !strings.Contains(out, "w: ") {
		t.Errorf("kotlinc warnings must be kept: %s", out)
	}
	if !strings.Contains(out, "warning: [options]") {
		t.Errorf("javac warnings must be kept: %s", out)
	}
	if !strings.Contains(out, "Warning: Gradle") {
		t.Errorf("gradle warnings must be kept: %s", out)
	}
	if !strings.Contains(out, "BUILD SUCCESSFUL") {
		t.Errorf("status must be kept: %s", out)
	}
	if strings.Contains(out, "> Task :") {
		t.Errorf("task progress must be stripped: %s", out)
	}
}

func TestBuildFilterStripsConfigureAndDokkaNoise(t *testing.T) {
	input := `Calculating task graph as no cached configuration is available for tasks: check

> Configure project :core
class org.jetbrains.dokka.gradle.adapters.AndroidExtensionWrapper could not get Android Extension for project :core
[android-junit5]: Cannot configure Jacoco for this project

> Task :core:preBuild UP-TO-DATE
> Task :core:preDebugBuild UP-TO-DATE
> Task :core:compileDebugKotlin UP-TO-DATE
> Task :samplev2:lintDebug FAILED
Lint found 8 errors, 21 warnings. First failure:

/src/LogsScreen.kt:50: Error: Field requires API level 26 [NewApi]
    val uiState = viewModel.uiState.collectAsState()

[Incubating] Problems report is available at: file:///build/reports/problems.html

Deprecated Gradle features were used in this build, making it incompatible with Gradle 10.

You can use '--warning-mode all' to show the individual deprecation warnings.
388 actionable tasks: 97 executed

FAILURE: Build failed with an exception.

* What went wrong:
Execution failed for task ':samplev2:lintDebug'.

* Try:
> Run with --stacktrace option to get the stack trace.

BUILD FAILED in 3s`

	out := buildFilterJoin(input)

	// Must keep.
	for _, want := range []string{"BUILD FAILED", "FAILURE:", "Execution failed", "Lint found 8 error", "Error: Field requires"} {
		if !strings.Contains(out, want) {
			t.Errorf("must preserve %q: %s", want, out)
		}
	}
	// Must strip.
	for _, bad := range []string{"Configure project", "dokka", "android-junit5", "> Task :", "Incubating", "Deprecated Gradle", "Run with --stacktrace"} {
		if strings.Contains(out, bad) {
			t.Errorf("must strip %q: %s", bad, out)
		}
	}

	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 60.0 {
		t.Errorf("expected >=60%% savings, got %.1f%%", savings)
	}
}

func TestBuildFilterEmptyLinePreserved(t *testing.T) {
	if !filterBuildLine("") {
		t.Errorf("empty line must pass through")
	}
	if !filterBuildLine("   ") {
		t.Errorf("whitespace-only line must pass through")
	}
}

func TestBuildTokenSavings(t *testing.T) {
	input := `Starting a Gradle Daemon (subsequent builds will be faster)
> Configure project :app
> Task :app:preBuild UP-TO-DATE
> Task :app:generateDebugBuildConfig UP-TO-DATE
> Task :app:generateDebugResValues UP-TO-DATE
> Task :app:generateDebugResources UP-TO-DATE
> Task :app:mergeDebugResources UP-TO-DATE
> Task :app:processDebugManifest UP-TO-DATE
> Task :app:compileDebugKotlin UP-TO-DATE
> Task :app:compileDebugJavaWithJavac UP-TO-DATE
> Task :app:compileDebugSources UP-TO-DATE
> Task :app:mergeDebugShaders UP-TO-DATE
> Task :app:compileDebugShaders UP-TO-DATE
> Task :app:generateDebugAssets UP-TO-DATE
> Task :app:mergeDebugAssets UP-TO-DATE
> Task :app:mergeDebugJniLibFolders UP-TO-DATE
> Task :app:validateSigningDebug UP-TO-DATE
> Task :app:packageDebug UP-TO-DATE
> Task :app:assembleDebug UP-TO-DATE

BUILD SUCCESSFUL in 3s
18 actionable tasks: 18 up-to-date`
	out := buildFilterJoin(input)
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 70.0 {
		t.Errorf("expected >=70%% token savings, got %.1f%%", savings)
	}
}

// ── TEST FILTER ──────────────────────────────────────────────────────────────

func TestUnitTestFailuresPreservedPassesStripped(t *testing.T) {
	input := `> Task :app:testDebugUnitTest
com.example.FooTest > test1 PASSED
com.example.FooTest > test2 PASSED
com.example.FooTest > test3 PASSED
com.example.FooTest > test4 PASSED
com.example.FooTest > test5 PASSED
com.example.FooTest > test6 PASSED
com.example.FooTest > test7 PASSED
com.example.FooTest > testBar FAILED
    java.lang.AssertionError: expected:<3> but was:<-1>
        at org.junit.Assert.fail(Assert.java:89)
        at org.junit.Assert.assertEquals(Assert.java:197)
        at com.example.FooTest.testBar(FooTest.kt:25)
com.example.FooTest > testQux PASSED

10 tests completed, 1 failed`
	out := filterTest(input)
	if !strings.Contains(out, "testBar FAILED") {
		t.Errorf("FAILED test must be preserved: %s", out)
	}
	if !strings.Contains(out, "AssertionError") {
		t.Errorf("exception class must be preserved: %s", out)
	}
	if !strings.Contains(out, "FooTest.testBar") {
		t.Errorf("user code frame must be preserved: %s", out)
	}
	if strings.Contains(out, "org.junit.Assert.fail") {
		t.Errorf("framework frames must be skipped: %s", out)
	}
	if strings.Contains(out, "PASSED") {
		t.Errorf("PASSED tests must be stripped: %s", out)
	}
	if !strings.Contains(out, "10 tests completed, 1 failed") {
		t.Errorf("summary must be preserved: %s", out)
	}
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 60.0 {
		t.Errorf("expected >=60%% savings, got %.1f%%", savings)
	}
}

func TestUnitTestSkipsFrameworkFrames(t *testing.T) {
	input := `com.example.CalcTest > testAdd FAILED
    java.lang.AssertionError: expected:<5> but was:<3>
        at org.junit.Assert.fail(Assert.java:89)
        at org.junit.Assert.assertEquals(Assert.java:197)
        at java.lang.reflect.Method.invoke(Method.java:498)
        at com.example.CalcTest.testAdd(CalcTest.kt:10)`
	out := filterTest(input)
	if !strings.Contains(out, "com.example.CalcTest.testAdd") {
		t.Errorf("user code frame must be shown: %s", out)
	}
	if strings.Contains(out, "org.junit.Assert") {
		t.Errorf("JUnit frames must be skipped: %s", out)
	}
	if strings.Contains(out, "java.lang.reflect") {
		t.Errorf("reflection frames must be skipped: %s", out)
	}
}

func TestUnitTestGradleDefaultNoTestLogging(t *testing.T) {
	input := `> Task :app:testDebugUnitTest

BUILD SUCCESSFUL in 15s
3 actionable tasks: 1 executed, 2 up-to-date`
	out := filterTest(input)
	if !strings.Contains(out, "BUILD SUCCESSFUL") && !strings.Contains(out, "ok ✓") {
		t.Errorf("must output something on success: %s", out)
	}
	if out == "" {
		t.Errorf("must not produce empty output")
	}
}

func TestUnitTestReportPathPreserved(t *testing.T) {
	input := `There were failing tests. See the report at: file:///app/build/reports/tests/testDebugUnitTest/index.html
BUILD FAILED in 20s`
	out := filterTest(input)
	if !strings.Contains(out, "See the report at") {
		t.Errorf("report path must be preserved: %s", out)
	}
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("BUILD FAILED must be preserved: %s", out)
	}
}

func TestTrySectionStrippedFromTestOutput(t *testing.T) {
	input := `com.example.FooTest > testBar FAILED
    java.lang.AssertionError: expected true

* Try:
> Run with --stacktrace option to get the stack trace.
> Run with --info or --debug option to get more log output.
> Get more help at https://help.gradle.org

BUILD FAILED in 5s`
	out := filterTest(input)
	if strings.Contains(out, "Run with --stacktrace") {
		t.Errorf("must strip Try section: %s", out)
	}
	if strings.Contains(out, "Get more help at") {
		t.Errorf("must strip Get more help: %s", out)
	}
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("BUILD FAILED must be preserved: %s", out)
	}
}

// ── CONNECTED TEST FILTER ────────────────────────────────────────────────────

func TestConnectedStripsDeviceNoise(t *testing.T) {
	input := `Starting 3 tests on Pixel_6_API_33(AVD) - 13
INSTRUMENTATION_STATUS: numtests=3
INSTRUMENTATION_STATUS_CODE: 1
com.example.MainActivityTest > exampleTest[Pixel_6_API_33] FAILED
    AssertionError: expected true
INSTRUMENTATION_STATUS_CODE: -2
Tests run: 3, Failures: 1, Errors: 0, Skipped: 0`
	out := filterConnected(input)
	if !strings.Contains(out, "FAILED") {
		t.Errorf("FAILED test must be preserved: %s", out)
	}
	if strings.Contains(out, "INSTRUMENTATION_STATUS:") {
		t.Errorf("instrumentation lines must be stripped: %s", out)
	}
	if strings.Contains(out, "Starting 3 tests") {
		t.Errorf("Starting tests line must be stripped: %s", out)
	}
}

func TestConnectedNoDeviceError(t *testing.T) {
	input := "com.android.builder.testing.api.DeviceException: No connected devices!"
	out := filterConnected(input)
	if !strings.Contains(out, "No connected devices") {
		t.Errorf("must show actionable error: %s", out)
	}
}

// ── LINT FILTER ──────────────────────────────────────────────────────────────

func TestLintPreservesViolations(t *testing.T) {
	input := `Wrote HTML report to file:/path/app/build/reports/lint-results-debug.html
src/main/java/com/example/MainActivity.kt:45: Error: Format string invalid [StringFormatInvalid]
  String.format(getString(R.string.no_args), arg)
  ^
0 errors, 4 warnings`
	out := filterLint(input)
	if !strings.Contains(out, "StringFormatInvalid") {
		t.Errorf("lint violation must be preserved: %s", out)
	}
	if !strings.Contains(out, "0 errors, 4 warnings") {
		t.Errorf("summary must be preserved: %s", out)
	}
	if strings.Contains(out, "Wrote HTML report") {
		t.Errorf("report path must be stripped: %s", out)
	}
}

func TestLintPreservesWarnings(t *testing.T) {
	input := `src/main/java/com/example/Utils.kt:89: Warning: HardcodedText [HardcodedText]
    return "Hello World"
           ~~~~~~~~~~~~~
src/main/res/layout/activity_main.xml:15: Warning: Missing contentDescription attribute on image [ContentDescription]
    <ImageView
Ran lint on variant debug: 2 warnings`
	out := filterLint(input)
	if !strings.Contains(out, "HardcodedText") {
		t.Errorf("warning violation must be preserved: %s", out)
	}
	if !strings.Contains(out, "ContentDescription") {
		t.Errorf("warning violation must be preserved: %s", out)
	}
	if !strings.Contains(out, "2 warnings") {
		t.Errorf("summary must be preserved: %s", out)
	}
}

func TestLintNoViolationsSuccess(t *testing.T) {
	input := `> Task :app:lint
BUILD SUCCESSFUL in 8s
3 actionable tasks: 1 executed, 2 up-to-date`
	out := filterLint(input)
	if out == "" {
		t.Errorf("must produce output on lint success")
	}
	if !strings.Contains(out, "BUILD SUCCESSFUL") && !strings.Contains(out, "ok ✓") {
		t.Errorf("must indicate success: %s", out)
	}
}

// ── DEPENDENCIES FILTER ──────────────────────────────────────────────────────

func TestDependenciesFilterExtractsTopLevel(t *testing.T) {
	input := `> Task :app:dependencies

------------------------------------------------------------
Project ':app'
------------------------------------------------------------

implementation - Implementation dependencies for the 'main' feature.
+--- org.jetbrains.kotlin:kotlin-stdlib:1.9.22
+--- androidx.core:core-ktx:1.12.0
+--- androidx.appcompat:appcompat:1.6.1
|    +--- androidx.annotation:annotation:1.3.0
|    +--- androidx.core:core:1.9.0
|    \--- androidx.cursoradapter:cursoradapter:1.0.0
+--- com.google.android.material:material:1.11.0
|    +--- androidx.annotation:annotation:1.2.0
|    +--- androidx.appcompat:appcompat:1.1.0
|    \--- androidx.recyclerview:recyclerview:1.0.0
\--- com.squareup.retrofit2:retrofit:2.9.0
     +--- com.squareup.okhttp3:okhttp:3.14.9
     \--- com.squareup.okio:okio:1.17.2

testImplementation - Test dependencies for the 'main' feature.
+--- junit:junit:4.13.2
\--- org.mockito:mockito-core:5.8.0

BUILD SUCCESSFUL in 2s
1 actionable tasks: 1 executed`

	out := filterDependencies(input)
	if !strings.Contains(out, "implementation (5):") {
		t.Errorf("must show config with count: %s", out)
	}
	if !strings.Contains(out, "testImplementation (2):") {
		t.Errorf("must show test config: %s", out)
	}
	if !strings.Contains(out, "kotlin-stdlib") {
		t.Errorf("must show top-level dep: %s", out)
	}
	if strings.Contains(out, "cursoradapter") {
		t.Errorf("transitive deps must be stripped: %s", out)
	}
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 60.0 {
		t.Errorf("expected >=60%% savings, got %.1f%%", savings)
	}
}

func TestDependenciesFilterEmpty(t *testing.T) {
	if got := filterDependencies(""); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestDependenciesFilterNoDeps(t *testing.T) {
	input := `> Task :app:dependencies
No dependencies

BUILD SUCCESSFUL in 1s`
	out := filterDependencies(input)
	if !strings.Contains(out, "ok") {
		t.Errorf("must show success: %s", out)
	}
}

// ── EDGE CASES ───────────────────────────────────────────────────────────────

func TestFilterEmptyInput(t *testing.T) {
	if filterTest("") != "" {
		t.Errorf("filterTest(\"\") must be empty")
	}
	if filterConnected("") != "" {
		t.Errorf("filterConnected(\"\") must be empty")
	}
	if filterLint("") != "" {
		t.Errorf("filterLint(\"\") must be empty")
	}
	if filterDependencies("") != "" {
		t.Errorf("filterDependencies(\"\") must be empty")
	}
}

func TestVerboseFlagDetection(t *testing.T) {
	isVerbose := func(args []string) bool {
		for _, a := range args {
			if a == "--stacktrace" || a == "--info" || a == "--debug" || a == "--full-stacktrace" {
				return true
			}
		}
		return false
	}
	if !isVerbose([]string{"assembleDebug", "--stacktrace"}) {
		t.Errorf("--stacktrace must be detected")
	}
	if !isVerbose([]string{"testDebugUnitTest", "--info"}) {
		t.Errorf("--info must be detected")
	}
}

func TestIsFrameworkFrame(t *testing.T) {
	frameworkFrames := []string{
		"at org.junit.Assert.fail(Assert.java:89)",
		"at junit.framework.Assert.fail(Assert.java:50)",
		"at java.lang.reflect.Method.invoke(Method.java:498)",
		"at org.gradle.api.internal.tasks.testing.SuiteTestClassProcessor.processTestClass(SuiteTestClassProcessor.java:51)",
	}
	for _, f := range frameworkFrames {
		if !isFrameworkFrame(f) {
			t.Errorf("expected framework frame: %s", f)
		}
	}
	userFrames := []string{
		"at com.example.FooTest.testBar(FooTest.kt:25)",
		"at com.example.MyApp.doSomething(MyApp.java:100)",
	}
	for _, f := range userFrames {
		if isFrameworkFrame(f) {
			t.Errorf("expected user frame: %s", f)
		}
	}
}

// ── FIXTURE-BASED TESTS ──────────────────────────────────────────────────────

func TestBuildFixtureTokenSavings(t *testing.T) {
	input := loadFixture(t, "gradlew_build_raw.txt")
	out := buildFilterJoin(input)
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 70.0 {
		t.Errorf("build fixture: expected >=70%% savings, got %.1f%%", savings)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "> Task :") {
			t.Errorf("task line not stripped: %s", l)
		}
	}
}

func TestBuildFailedFixtureTokenSavings(t *testing.T) {
	input := loadFixture(t, "gradlew_build_failed_raw.txt")
	out := buildFilterJoin(input)
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("BUILD FAILED must be preserved: %s", out)
	}
	if strings.Contains(out, "Run with --stacktrace") {
		t.Errorf("Try section must be stripped: %s", out)
	}
}

func TestTestFixturePreservesFailures(t *testing.T) {
	input := loadFixture(t, "gradlew_test_raw.txt")
	out := filterTest(input)
	if strings.Contains(out, "PASSED") {
		t.Errorf("PASSED tests must be stripped: %s", out)
	}
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 60.0 {
		t.Errorf("test fixture: expected >=60%% savings, got %.1f%%", savings)
	}
}

func TestTestFailedFixtureShowsUserCode(t *testing.T) {
	input := loadFixture(t, "gradlew_test_failed_raw.txt")
	out := filterTest(input)
	if !strings.Contains(out, "FAILED") {
		t.Errorf("FAILED tests must be preserved: %s", out)
	}
	if !strings.Contains(out, "CalculatorTest.testSubtraction") && !strings.Contains(out, "MainViewModelTest.loadDataError") {
		t.Errorf("user code frame must be shown: %s", out)
	}
	if !strings.Contains(out, "5 tests completed, 2 failed") {
		t.Errorf("summary must be preserved: %s", out)
	}
}

func TestConnectedFixtureTokenSavings(t *testing.T) {
	input := loadFixture(t, "gradlew_connected_raw.txt")
	out := filterConnected(input)
	if strings.Contains(out, "INSTRUMENTATION_STATUS") {
		t.Errorf("instrumentation lines must be stripped: %s", out)
	}
}

func TestLintFixtureTokenSavings(t *testing.T) {
	input := loadFixture(t, "gradlew_lint_raw.txt")
	out := filterLint(input)
	if strings.Contains(out, "Wrote HTML report") {
		t.Errorf("report lines must be stripped: %s", out)
	}
	savings := 100.0 - (float64(countTokens(out)) / float64(countTokens(input)) * 100.0)
	if savings < 60.0 {
		t.Errorf("lint fixture: expected >=60%% savings, got %.1f%%", savings)
	}
}

// ── OUTPUT FORMAT TESTS ──────────────────────────────────────────────────────

func TestBuildSuccessOutputFormat(t *testing.T) {
	input := loadFixture(t, "gradlew_build_raw.txt")
	out := buildFilterJoin(input)
	if !strings.Contains(out, "BUILD SUCCESSFUL") {
		t.Errorf("should keep BUILD SUCCESSFUL: %s", out)
	}
	if !strings.Contains(out, "actionable tasks") {
		t.Errorf("should keep actionable tasks line: %s", out)
	}
	if strings.Contains(out, "> Task :") {
		t.Errorf("should strip task progress lines: %s", out)
	}
}

func TestBuildFailedOutputFormat(t *testing.T) {
	input := loadFixture(t, "gradlew_build_failed_raw.txt")
	out := buildFilterJoin(input)
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("should keep BUILD FAILED: %s", out)
	}
	if !strings.Contains(out, "FAILURE:") {
		t.Errorf("should keep failure header: %s", out)
	}
	if !strings.Contains(out, "e: ") {
		t.Errorf("should keep error lines: %s", out)
	}
	if strings.Contains(out, "> Task :") {
		t.Errorf("should strip task progress lines: %s", out)
	}
}

func TestTestSuccessOutputFormat(t *testing.T) {
	input := loadFixture(t, "gradlew_test_raw.txt")
	out := filterTest(input)
	if !strings.Contains(out, "tests completed") {
		t.Errorf("should keep test summary: %s", out)
	}
	if !strings.Contains(out, "BUILD SUCCESSFUL") {
		t.Errorf("should keep BUILD SUCCESSFUL: %s", out)
	}
	if strings.Contains(out, "PASSED") {
		t.Errorf("should strip passing test lines: %s", out)
	}
}

func TestTestFailedOutputFormat(t *testing.T) {
	input := loadFixture(t, "gradlew_test_failed_raw.txt")
	out := filterTest(input)
	if !strings.Contains(out, "FAILED") {
		t.Errorf("should keep failed test names: %s", out)
	}
	if !strings.Contains(out, "tests completed") {
		t.Errorf("should keep test summary: %s", out)
	}
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("should keep BUILD FAILED: %s", out)
	}
	if strings.Contains(out, "PASSED") {
		t.Errorf("should strip passing test lines: %s", out)
	}
	if strings.Contains(out, "at org.junit.") {
		t.Errorf("should strip framework frames: %s", out)
	}
}

func TestConnectedOutputFormat(t *testing.T) {
	input := loadFixture(t, "gradlew_connected_raw.txt")
	out := filterConnected(input)
	if !strings.Contains(out, "BUILD SUCCESSFUL") {
		t.Errorf("should keep BUILD SUCCESSFUL: %s", out)
	}
	if strings.Contains(out, "INSTRUMENTATION_STATUS") {
		t.Errorf("should strip instrumentation noise: %s", out)
	}
}

func TestLintOutputFormat(t *testing.T) {
	input := loadFixture(t, "gradlew_lint_raw.txt")
	out := filterLint(input)
	if !strings.Contains(out, "Error:") {
		t.Errorf("should keep error violations: %s", out)
	}
	if !strings.Contains(out, "Warning:") {
		t.Errorf("should keep warning violations: %s", out)
	}
	if !strings.Contains(out, "BUILD FAILED") {
		t.Errorf("should keep BUILD FAILED: %s", out)
	}
	if strings.Contains(out, "Wrote HTML report") {
		t.Errorf("should strip report paths: %s", out)
	}
}

func TestLintPreservesCodeContext(t *testing.T) {
	input := loadFixture(t, "gradlew_lint_raw.txt")
	out := filterLint(input)
	if !strings.Contains(out, "String.format(getString(R.string.template)") {
		t.Errorf("code snippet after Android lint error must be preserved: %s", out)
	}
	if !strings.Contains(out, "This format string placeholder index") {
		t.Errorf("explanation line after caret must be preserved: %s", out)
	}
	if !strings.Contains(out, `return "Hello World"`) {
		t.Errorf("code snippet after Android lint warning must be preserved: %s", out)
	}
	if !strings.Contains(out, "<ImageView") {
		t.Errorf("XML snippet after lint warning must be preserved: %s", out)
	}
}
