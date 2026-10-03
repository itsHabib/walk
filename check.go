package walk

import (
	"fmt"
	"sort"
)

// Step is one entry in a counterexample: the action that ran and the state it
// produced.
type Step struct {
	Action string `json:"action"`
	State  State  `json:"state"`
}

// Finding is one thing the walk found wrong, with the shortest path to it.
// Shortest because the walk is breadth-first, so the first time a state is
// reached is along a path of minimum length.
type Finding struct {
	// Kind is "invariant", "deadlock" or "pin".
	Kind string `json:"kind"`
	// Rule names the broken invariant, empty for other kinds.
	Rule string `json:"rule,omitempty"`
	// Detail explains the finding in a sentence.
	Detail string `json:"detail"`
	// Trace is the path from the initial state, empty for a pin mismatch.
	Trace []Step `json:"trace,omitempty"`
}

// Report is the result of a walk.
type Report struct {
	Model string `json:"model"`
	// States is the number of distinct reachable states.
	States int `json:"states"`
	// Transitions is the number of enabled steps across those states.
	Transitions int `json:"transitions"`
	// Depth is the length of the longest shortest-path from the initial state.
	Depth    int       `json:"depth"`
	Findings []Finding `json:"findings"`
}

// OK reports whether the walk found nothing wrong.
func (r *Report) OK() bool { return len(r.Findings) == 0 }

type node struct {
	state  State
	parent int
	action string
	depth  int
}

// Check walks every reachable state breadth-first from the initial state,
// testing the invariants in each one and noting states that can take no step.
//
// The walk is exhaustive: it terminates because every variable has a finite
// domain, so there are finitely many states, and each is expanded once. The
// limit argument caps the states visited so a model with an accidentally huge
// product does not run forever; reaching it is reported as a finding rather
// than a silent partial answer.
func Check(m *Model, limit int) (*Report, error) {
	if limit <= 0 {
		limit = 1 << 20
	}
	rep := &Report{Model: m.Name}

	init := State(m.Init).clone()
	nodes := []node{{state: init, parent: -1, depth: 0}}
	seen := map[string]int{init.Key(): 0}

	// Invariants are checked on discovery, so each state is tested once and
	// the trace recorded is the shortest path to it.
	if err := m.checkInvariants(rep, nodes, 0); err != nil {
		return nil, err
	}

	for i := 0; i < len(nodes); i++ {
		cur := nodes[i]
		if cur.depth > rep.Depth {
			rep.Depth = cur.depth
		}
		enabled := 0
		for _, a := range m.Actions {
			ok, err := m.eval(a.Guard, cur.state)
			if err != nil {
				return nil, fmt.Errorf("action %q: %w", a.Name, err)
			}
			if !ok {
				continue
			}
			enabled++
			rep.Transitions++

			next, err := m.apply(a, cur.state)
			if err != nil {
				return nil, fmt.Errorf("action %q: %w", a.Name, err)
			}
			key := next.Key()
			if _, ok := seen[key]; ok {
				continue
			}
			if len(nodes) >= limit {
				rep.Findings = append(rep.Findings, Finding{
					Kind:   "limit",
					Detail: fmt.Sprintf("stopped after %d states; the model is larger than the limit, so this walk is not exhaustive", limit),
				})
				rep.States = len(nodes)
				return rep, nil
			}
			seen[key] = len(nodes)
			nodes = append(nodes, node{state: next, parent: i, action: a.Name, depth: cur.depth + 1})
			if err := m.checkInvariants(rep, nodes, len(nodes)-1); err != nil {
				return nil, err
			}
		}

		if enabled == 0 {
			allowed, err := m.eval(m.Terminal, cur.state)
			if err != nil {
				return nil, fmt.Errorf("terminal: %w", err)
			}
			// A nil Terminal evaluates to true, which would excuse every
			// dead end. Treat "not stated" as "no dead end is allowed".
			if m.Terminal == nil {
				allowed = false
			}
			if !allowed {
				rep.Findings = append(rep.Findings, Finding{
					Kind:   "deadlock",
					Detail: fmt.Sprintf("state %q has no enabled action and is not marked terminal", cur.state.Key()),
					Trace:  trace(nodes, i),
				})
			}
		}
	}

	rep.States = len(nodes)
	if m.Pin > 0 && m.Pin != rep.States {
		rep.Findings = append(rep.Findings, Finding{
			Kind:   "pin",
			Detail: fmt.Sprintf("reachable states changed: pinned at %d, walked %d", m.Pin, rep.States),
		})
	}
	sortFindings(rep.Findings)
	return rep, nil
}

func (m *Model) checkInvariants(rep *Report, nodes []node, idx int) error {
	s := nodes[idx].state
	for _, inv := range m.Invariants {
		ok, err := m.eval(inv.Expr, s)
		if err != nil {
			return fmt.Errorf("invariant %q: %w", inv.Name, err)
		}
		if ok {
			continue
		}
		// Report a given invariant once, at the shortest path that breaks it.
		// A second state breaking the same rule is almost always the same
		// defect reached another way, and one trace is what gets debugged.
		if hasRule(rep.Findings, inv.Name) {
			continue
		}
		rep.Findings = append(rep.Findings, Finding{
			Kind:   "invariant",
			Rule:   inv.Name,
			Detail: fmt.Sprintf("%s does not hold in %s", inv.Name, s.Key()),
			Trace:  trace(nodes, idx),
		})
	}
	return nil
}

// apply produces the successor state. Every assignment reads from the state
// before the action, so two assignments that swap variables do so at once
// rather than one overwriting the other.
func (m *Model) apply(a Action, s State) (State, error) {
	next := s.clone()
	for name, value := range a.Effect {
		if _, isVar := m.Vars[value]; isVar {
			next[name] = s[value]
			if err := m.checkValue(name, next[name]); err != nil {
				return nil, fmt.Errorf("assigning %s to %s: %w", value, name, err)
			}
			continue
		}
		next[name] = value
	}
	return next, nil
}

func trace(nodes []node, idx int) []Step {
	var out []Step
	for i := idx; i != -1; i = nodes[i].parent {
		if nodes[i].parent == -1 {
			out = append(out, Step{Action: "init", State: nodes[i].state})
			break
		}
		out = append(out, Step{Action: nodes[i].action, State: nodes[i].state})
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out
}

func hasRule(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.Kind == "invariant" && f.Rule == rule {
			return true
		}
	}
	return false
}

// sortFindings puts the shortest traces first, so the easiest defect to read
// is the one at the top of the report.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		return len(fs[i].Trace) < len(fs[j].Trace)
	})
}
