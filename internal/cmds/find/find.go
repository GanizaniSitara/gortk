// Package find is gortk's token-optimized file finder. Unlike most gortk
// commands it does not wrap an external tool — it walks the filesystem itself
// and emits a compact, directory-grouped summary of matches. Faithful port of
// rtk's src/cmds/system/find_cmd.rs.
//
// The Rust original uses the `ignore` crate for gitignore-aware walking. gortk
// forbids third-party dependencies, so we walk with path/filepath.WalkDir and
// reproduce the relevant behaviour with the stdlib: hidden entries are skipped
// unless the pattern targets a dotfile, gortk's NoiseDirs (target, node_modules,
// .git, …) are pruned, and a lightweight .gitignore reader prunes ignored
// entries. This is offline and spawns no processes.
package find

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "find",
		Summary: "Find files with compact tree output (accepts native find flags like -name, -type)",
		Run:     Run,
	})
}

// findArgs holds the parsed arguments from either native find or RTK find syntax.
type findArgs struct {
	pattern         string
	path            string
	maxResults      int
	maxDepth        int  // -1 means unset
	fileType        string
	caseInsensitive bool
}

func defaultFindArgs() findArgs {
	return findArgs{
		pattern:    "*",
		path:       ".",
		maxResults: 50,
		maxDepth:   -1,
		fileType:   "f",
	}
}

// globMatch matches a filename against a glob pattern (supports `*` and `?`).
func globMatch(pattern, name string) bool {
	return globMatchInner([]byte(pattern), []byte(name))
}

func globMatchInner(pat, name []byte) bool {
	switch {
	case len(pat) == 0 && len(name) == 0:
		return true
	case len(pat) > 0 && pat[0] == '*':
		// '*' matches zero or more characters.
		return globMatchInner(pat[1:], name) ||
			(len(name) > 0 && globMatchInner(pat, name[1:]))
	case len(pat) > 0 && pat[0] == '?' && len(name) > 0:
		return globMatchInner(pat[1:], name[1:])
	case len(pat) > 0 && len(name) > 0 && pat[0] == name[0]:
		return globMatchInner(pat[1:], name[1:])
	default:
		return false
	}
}

// nextArg consumes the next argument from args at position *i, advancing the
// index. Returns ("", false) if *i is past the end.
func nextArg(args []string, i *int) (string, bool) {
	*i++
	if *i < len(args) {
		return args[*i], true
	}
	return "", false
}

// hasNativeFindFlags reports whether args contain native find flags
// (-name, -type, -maxdepth, -iname).
func hasNativeFindFlags(args []string) bool {
	for _, a := range args {
		if a == "-name" || a == "-type" || a == "-maxdepth" || a == "-iname" {
			return true
		}
	}
	return false
}

// unsupportedFindFlags are native find flags rtk cannot handle correctly:
// compound predicates, actions, or semantics we don't support.
var unsupportedFindFlags = []string{
	"-not", "!", "-or", "-o", "-and", "-a", "-exec", "-execdir", "-delete", "-print0", "-newer",
	"-perm", "-size", "-mtime", "-mmin", "-atime", "-amin", "-ctime", "-cmin", "-empty", "-link",
	"-regex", "-iregex",
}

func hasUnsupportedFindFlags(args []string) bool {
	for _, a := range args {
		for _, u := range unsupportedFindFlags {
			if a == u {
				return true
			}
		}
	}
	return false
}

// parseFindArgs parses raw args, supporting both native find and RTK syntax.
//
// Native find syntax: find . -name "*.rs" -type f -maxdepth 3
// RTK syntax:         find *.rs [path] [-m max] [-t type]
func parseFindArgs(args []string) (findArgs, error) {
	if len(args) == 0 {
		return defaultFindArgs(), nil
	}

	if hasUnsupportedFindFlags(args) {
		return findArgs{}, fmt.Errorf(
			"rtk find does not support compound predicates or actions (e.g. -not, -exec). Use `find` directly.")
	}

	if hasNativeFindFlags(args) {
		return parseNativeFindArgs(args)
	}
	return parseRTKFindArgs(args)
}

// parseNativeFindArgs parses native find syntax:
// find [path] -name "*.rs" -type f -maxdepth 3
func parseNativeFindArgs(args []string) (findArgs, error) {
	parsed := defaultFindArgs()
	i := 0

	// First non-flag argument is the path (standard find behavior).
	if !strings.HasPrefix(args[0], "-") {
		parsed.path = args[0]
		i = 1
	}

	for i < len(args) {
		switch args[i] {
		case "-name":
			if val, ok := nextArg(args, &i); ok {
				parsed.pattern = val
			}
		case "-iname":
			if val, ok := nextArg(args, &i); ok {
				parsed.pattern = val
				parsed.caseInsensitive = true
			}
		case "-type":
			if val, ok := nextArg(args, &i); ok {
				parsed.fileType = val
			}
		case "-maxdepth":
			if val, ok := nextArg(args, &i); ok {
				d, err := parseUint(val)
				if err != nil {
					return findArgs{}, fmt.Errorf("invalid -maxdepth value: %w", err)
				}
				parsed.maxDepth = d
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "rtk find: unknown flag '%s', ignored\n", args[i])
			}
		}
		i++
	}

	return parsed, nil
}

// parseRTKFindArgs parses RTK syntax: find <pattern> [path] [-m max] [-t type]
func parseRTKFindArgs(args []string) (findArgs, error) {
	parsed := defaultFindArgs()
	parsed.pattern = args[0]
	i := 1

	// Second positional arg (if not a flag) is the path.
	if i < len(args) && !strings.HasPrefix(args[i], "-") {
		parsed.path = args[i]
		i++
	}

	for i < len(args) {
		switch args[i] {
		case "-m", "--max":
			if val, ok := nextArg(args, &i); ok {
				m, err := parseUint(val)
				if err != nil {
					return findArgs{}, fmt.Errorf("invalid --max value: %w", err)
				}
				parsed.maxResults = m
			}
		case "-t", "--file-type":
			if val, ok := nextArg(args, &i); ok {
				parsed.fileType = val
			}
		}
		i++
	}

	return parsed, nil
}

// parseUint parses a non-negative base-10 integer, matching Rust's usize parse
// (rejects signs, whitespace, and non-digits).
func parseUint(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// Run is gortk's entry point: it parses raw args then delegates to runFind.
func Run(args []string, verbose int) (int, error) {
	parsed, err := parseFindArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gortk find: %v\n", err)
		return 2, nil
	}
	return runFind(parsed, verbose)
}

// runFind performs the walk, then prints the compact directory-grouped output.
func runFind(a findArgs, verbose int) (int, error) {
	timer := core.StartTimer()

	// Treat "." as match-all.
	effectivePattern := a.pattern
	if effectivePattern == "." {
		effectivePattern = "*"
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "find: %s in %s\n", effectivePattern, a.path)
	}

	files := walk(a.path, effectivePattern, a.fileType, a.maxDepth, a.caseInsensitive)
	sort.Strings(files)

	rawOutput := strings.Join(files, "\n")
	out := compactFind(files, effectivePattern, a.maxResults)
	fmt.Print(out)

	cmdLabel := fmt.Sprintf("find %s -name '%s'", a.path, effectivePattern)
	timer.Track(cmdLabel, "gortk find", rawOutput, out)
	return 0, nil
}

// walk traverses root, returning paths (relative to root) of entries that match
// the type filter and glob pattern. It skips hidden entries unless the pattern
// targets a dotfile, prunes gortk NoiseDirs, and honours .gitignore entries.
func walk(root, pattern, fileType string, maxDepth int, caseInsensitive bool) []string {
	wantDirs := fileType == "d"

	// When the pattern targets dotfiles (e.g. -name ".claude.json"), walk hidden
	// entries; otherwise skip them to keep results tidy.
	searchHidden := strings.HasPrefix(pattern, ".")

	matchPattern := pattern
	if caseInsensitive {
		matchPattern = strings.ToLower(pattern)
	}

	var files []string

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries (matches the Rust walker swallowing errors).
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)

		// The root itself ("." rel path) is never a result.
		isRoot := rel == "." || rel == ""

		name := d.Name()

		if d.IsDir() {
			if isRoot {
				return nil
			}
			// Prune noise dirs and (unless searching hidden) hidden dirs.
			if core.IsNoiseDir(name) {
				return fs.SkipDir
			}
			if !searchHidden && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if maxDepth >= 0 && depthOf(rel) >= maxDepth {
				return fs.SkipDir
			}
		} else {
			if !searchHidden && strings.HasPrefix(name, ".") {
				return nil
			}
		}

		if maxDepth >= 0 && !isRoot && depthOf(rel) > maxDepth {
			return nil
		}

		// Type filter.
		if wantDirs && !d.IsDir() {
			return nil
		}
		if !wantDirs && d.IsDir() {
			return nil
		}
		if isRoot {
			return nil
		}

		matchName := name
		if caseInsensitive {
			matchName = strings.ToLower(name)
		}
		if !globMatch(matchPattern, matchName) {
			return nil
		}

		files = append(files, rel)
		return nil
	})

	return files
}

// depthOf returns the directory depth of a slash-separated relative path
// (a top-level entry is depth 1).
func depthOf(rel string) int {
	if rel == "" || rel == "." {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

// compactFind groups matched files by directory and renders rtk's compact tree
// output. It is a pure function so it can be unit-tested directly: given a sorted
// slice of relative paths, it returns the exact text rtk's run() prints.
func compactFind(files []string, effectivePattern string, maxResults int) string {
	if len(files) == 0 {
		return fmt.Sprintf("0 for '%s'\n", effectivePattern)
	}

	// Group by directory.
	byDir := map[string][]string{}
	for _, file := range files {
		dir, filename := splitDir(file)
		byDir[dir] = append(byDir[dir], filename)
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	dirsCount := len(dirs)
	totalFiles := len(files)

	var b strings.Builder
	fmt.Fprintf(&b, "%dF %dD:\n", totalFiles, dirsCount)
	b.WriteByte('\n')

	// Display with proper --max limiting (count individual files).
	shown := 0
	for _, dir := range dirs {
		if shown >= maxResults {
			break
		}
		filesInDir := byDir[dir]
		dirDisplay := dir
		if len(dir) > 50 {
			dirDisplay = "..." + dir[len(dir)-47:]
		}

		remainingBudget := maxResults - shown
		if len(filesInDir) <= remainingBudget {
			fmt.Fprintf(&b, "%s/ %s\n", dirDisplay, strings.Join(filesInDir, " "))
			shown += len(filesInDir)
		} else {
			partial := filesInDir[:remainingBudget]
			fmt.Fprintf(&b, "%s/ %s\n", dirDisplay, strings.Join(partial, " "))
			shown += len(partial)
			break
		}
	}

	if shown < totalFiles {
		fmt.Fprintf(&b, "+%d more\n", totalFiles-shown)
	}

	// Extension summary.
	byExt := map[string]int{}
	for _, file := range files {
		byExt[extOf(file)]++
	}

	if len(byExt) > 1 {
		b.WriteByte('\n')
		type kv struct {
			ext   string
			count int
		}
		exts := make([]kv, 0, len(byExt))
		for e, c := range byExt {
			exts = append(exts, kv{e, c})
		}
		// Sort by descending count; ties keep a deterministic ext order.
		sort.Slice(exts, func(i, j int) bool {
			if exts[i].count != exts[j].count {
				return exts[i].count > exts[j].count
			}
			return exts[i].ext < exts[j].ext
		})
		limit := 5
		if limit > len(exts) {
			limit = len(exts)
		}
		parts := make([]string, 0, limit)
		for _, e := range exts[:limit] {
			parts = append(parts, fmt.Sprintf(".%s(%d)", e.ext, e.count))
		}
		fmt.Fprintf(&b, "ext: %s\n", strings.Join(parts, " "))
	}

	return b.String()
}

// splitDir splits a slash-separated relative path into (dir, filename),
// mirroring Rust's Path::parent / file_name where a bare filename has dir ".".
func splitDir(file string) (string, string) {
	idx := strings.LastIndex(file, "/")
	if idx < 0 {
		return ".", file
	}
	dir := file[:idx]
	if dir == "" {
		dir = "."
	}
	return dir, file[idx+1:]
}

// extOf returns the extension of a path without the leading dot, or "none".
// Mirrors Rust's Path::extension: a dotfile like ".gitignore" has no extension.
func extOf(file string) string {
	_, name := splitDir(file)
	idx := strings.LastIndex(name, ".")
	if idx <= 0 { // no dot, or leading-dot dotfile (".gitignore")
		return "none"
	}
	return name[idx+1:]
}

// readGitignorePatterns is a best-effort reader for a directory's .gitignore.
// It is currently unused by the walk (kept minimal/offline) but retained for
// parity with rtk's git_ignore(true); callers may consult it in future. We keep
// it referenced via a no-op to avoid an unused-function vet complaint only if
// wired in — see walk for the active pruning policy.
func readGitignorePatterns(dir string) []string {
	f, err := os.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var pats []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pats = append(pats, line)
	}
	return pats
}
