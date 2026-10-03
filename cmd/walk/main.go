// Command walk reads a finite state model and walks every state it can reach.
//
//	walk check <model.json>      walk the model and report what breaks
//	walk pin   <model.json>      record the current reachable-state count
//	walk states <model.json>     list every reachable state
//
// check exits 0 when the model is clean, 1 when it found something, and 2 when
// the model itself could not be read.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/itsHabib/walk"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "walk:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command given")
	}
	cmd, rest := args[0], args[1:]

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "write the report as JSON")
	limit := fs.Int("limit", 0, "stop after this many states (0 uses the default)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		usage()
		return fmt.Errorf("%s needs exactly one model file", cmd)
	}
	path := fs.Arg(0)

	m, err := walk.Load(path)
	if err != nil {
		return err
	}

	switch cmd {
	case "check":
		rep, err := walk.Check(m, *limit)
		if err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return err
			}
		} else {
			printReport(rep)
		}
		if !rep.OK() {
			os.Exit(1)
		}
		return nil

	case "pin":
		rep, err := walk.Check(m, *limit)
		if err != nil {
			return err
		}
		if m.Pin == rep.States {
			fmt.Printf("%s: already pinned at %d states\n", path, rep.States)
			return nil
		}
		old := m.Pin
		if err := repin(path, rep.States); err != nil {
			return err
		}
		fmt.Printf("%s: pin %d -> %d states\n", path, old, rep.States)
		return nil

	case "states":
		states, err := walk.States(m, *limit)
		if err != nil {
			return err
		}
		sort.Strings(states)
		for _, s := range states {
			fmt.Println(s)
		}
		fmt.Fprintf(os.Stderr, "%d states\n", len(states))
		return nil
	}

	usage()
	return fmt.Errorf("unknown command %q", cmd)
}

// pinField matches an existing pin entry so it can be rewritten in place.
var pinField = regexp.MustCompile(`"pin"\s*:\s*-?\d+`)

// repin edits the pin value textually rather than re-encoding the document,
// because a model is written by hand and round-tripping it through a Go map
// would sort its keys and explode its expressions onto one line each.
//
// The result is parsed before it is written, so a botched edit fails here
// instead of on the next run.
func repin(path string, states int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	replacement := fmt.Sprintf(`"pin": %d`, states)

	var out string
	if loc := pinField.FindStringIndex(text); loc != nil {
		out = text[:loc[0]] + replacement + text[loc[1]:]
	} else {
		// No pin yet: add one as the first key, which needs no comma fixups
		// on the line before it. A model always has at least "vars" after it.
		open := strings.Index(text, "{")
		if open < 0 {
			return fmt.Errorf("%s: not a JSON object", path)
		}
		out = text[:open+1] + "\n  " + replacement + "," + text[open+1:]
	}

	var check map[string]any
	if err := json.Unmarshal([]byte(out), &check); err != nil {
		return fmt.Errorf("%s: rewriting the pin produced invalid JSON: %w", path, err)
	}
	if got, ok := check["pin"].(float64); !ok || int(got) != states {
		return fmt.Errorf("%s: rewriting the pin did not set it to %d", path, states)
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

func printReport(r *walk.Report) {
	name := r.Model
	if name == "" {
		name = "model"
	}
	fmt.Printf("%s: %d states, %d transitions, depth %d\n", name, r.States, r.Transitions, r.Depth)
	if r.OK() {
		fmt.Println("clean")
		return
	}
	for _, f := range r.Findings {
		fmt.Println()
		if f.Rule != "" {
			fmt.Printf("%s: %s\n", f.Kind, f.Rule)
		} else {
			fmt.Printf("%s\n", f.Kind)
		}
		fmt.Printf("  %s\n", f.Detail)
		for i, s := range f.Trace {
			fmt.Printf("  %2d. %-22s %s\n", i, s.Action, s.State.Key())
		}
	}
	fmt.Printf("\n%d finding(s)\n", len(r.Findings))
}

func usage() {
	fmt.Fprintln(os.Stderr, `walk - walk every reachable state of a finite model

  walk check  <model.json>   walk the model and report what breaks
  walk pin    <model.json>   record the current reachable-state count
  walk states <model.json>   list every reachable state

  -json    write the report as JSON
  -limit   stop after this many states`)
}
