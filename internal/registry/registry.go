// Package registry is gortk's command registry. Each command package registers
// itself from an init() function, so adding a command never requires editing a
// central dispatch table — the package is simply blank-imported by
// internal/cmds/allcmds. This self-registration pattern lets command ports be
// developed in isolation without merge conflicts.
package registry

import "sort"

// Cmd describes one gortk subcommand.
type Cmd struct {
	// Name is the primary invocation name, e.g. "ls" or "git".
	Name string
	// Aliases are additional names that route to this command.
	Aliases []string
	// Summary is a one-line help description.
	Summary string
	// Run executes the command. args are the tokens after the command name;
	// verbose is the -v count. It returns the process exit code.
	Run func(args []string, verbose int) (int, error)
}

var reg = map[string]*Cmd{}
var order []*Cmd

// Register adds a command to the registry. It panics on a duplicate name, which
// surfaces wiring mistakes at startup rather than silently shadowing.
func Register(c *Cmd) {
	if c == nil || c.Name == "" {
		panic("registry: Register called with nil command or empty name")
	}
	if _, dup := reg[c.Name]; dup {
		panic("registry: duplicate command name: " + c.Name)
	}
	reg[c.Name] = c
	for _, a := range c.Aliases {
		if _, dup := reg[a]; dup {
			panic("registry: duplicate command alias: " + a)
		}
		reg[a] = c
	}
	order = append(order, c)
}

// Lookup returns the command registered under name (or one of its aliases).
func Lookup(name string) (*Cmd, bool) {
	c, ok := reg[name]
	return c, ok
}

// All returns every registered command, sorted by primary name.
func All() []*Cmd {
	out := make([]*Cmd, len(order))
	copy(out, order)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
