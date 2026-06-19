package wc

import "testing"

func TestSingleFileFull(t *testing.T) {
	raw := "      30      96     978 scripts/find_duplicate_attrs.py\n"
	if got := filterWcOutput(raw, modeFull); got != "30L 96W 978B" {
		t.Errorf("got %q, want %q", got, "30L 96W 978B")
	}
}

func TestSingleFileLinesOnly(t *testing.T) {
	raw := "      30 scripts/find_duplicate_attrs.py\n"
	if got := filterWcOutput(raw, modeLines); got != "30" {
		t.Errorf("got %q, want %q", got, "30")
	}
}

func TestSingleFileWordsOnly(t *testing.T) {
	raw := "      96 scripts/find_duplicate_attrs.py\n"
	if got := filterWcOutput(raw, modeWords); got != "96" {
		t.Errorf("got %q, want %q", got, "96")
	}
}

func TestStdinFull(t *testing.T) {
	raw := "      30      96     978\n"
	if got := filterWcOutput(raw, modeFull); got != "30L 96W 978B" {
		t.Errorf("got %q, want %q", got, "30L 96W 978B")
	}
}

func TestStdinLines(t *testing.T) {
	raw := "      30\n"
	if got := filterWcOutput(raw, modeLines); got != "30" {
		t.Errorf("got %q, want %q", got, "30")
	}
}

func TestMultiFileLines(t *testing.T) {
	raw := "      30 src/main.rs\n      50 src/lib.rs\n      80 total\n"
	want := "30 main.rs\n50 lib.rs\nΣ 80"
	if got := filterWcOutput(raw, modeLines); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMultiFileFull(t *testing.T) {
	raw := "      30      96     978 src/main.rs\n      50     120    1500 src/lib.rs\n      80     216    2478 total\n"
	want := "30L 96W 978B main.rs\n50L 120W 1500B lib.rs\nΣ 80L 216W 2478B"
	if got := filterWcOutput(raw, modeFull); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDetectModeFull(t *testing.T) {
	args := []string{"file.py"}
	if got := detectMode(args); got != modeFull {
		t.Errorf("got %v, want modeFull", got)
	}
}

func TestDetectModeLines(t *testing.T) {
	args := []string{"-l", "file.py"}
	if got := detectMode(args); got != modeLines {
		t.Errorf("got %v, want modeLines", got)
	}
}

func TestDetectModeMixed(t *testing.T) {
	args := []string{"-lw", "file.py"}
	if got := detectMode(args); got != modeMixed {
		t.Errorf("got %v, want modeMixed", got)
	}
}

func TestDetectModeSeparateFlags(t *testing.T) {
	args := []string{"-l", "-w", "file.py"}
	if got := detectMode(args); got != modeMixed {
		t.Errorf("got %v, want modeMixed", got)
	}
}

func TestCommonPrefix(t *testing.T) {
	paths := []string{"src/main.rs", "src/lib.rs", "src/utils.rs"}
	if got := findCommonPrefix(paths); got != "src/" {
		t.Errorf("got %q, want %q", got, "src/")
	}
}

func TestNoCommonPrefix(t *testing.T) {
	paths := []string{"main.rs", "lib.rs"}
	if got := findCommonPrefix(paths); got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
}

func TestDeepCommonPrefix(t *testing.T) {
	paths := []string{"src/cmd/wc.rs", "src/cmd/ls.rs"}
	if got := findCommonPrefix(paths); got != "src/cmd/" {
		t.Errorf("got %q, want %q", got, "src/cmd/")
	}
}

func TestEmpty(t *testing.T) {
	if got := filterWcOutput("", modeFull); got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
}
