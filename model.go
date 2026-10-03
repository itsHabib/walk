// Package walk reads a finite state model as JSON and walks every state it can
// reach, reporting the shortest path to any state that breaks a stated rule.
//
// The JSON document is the contract. Everything else in this package exists to
// read it, check it, or walk it.
package walk

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Model is the whole input: what can vary, where it starts, what can happen,
// and what must stay true.
type Model struct {
	// Name labels the model in reports.
	Name string `json:"name"`

	// Vars declares every variable and the complete set of values it may hold.
	// The state space is the product of these domains, so keep them small.
	Vars map[string][]string `json:"vars"`

	// Init is the single starting assignment. Every variable needs a value.
	Init map[string]string `json:"init"`

	// Actions are the possible steps. An action whose guard holds in a state
	// may run there; when several hold, all of them are explored.
	Actions []Action `json:"actions"`

	// Invariants must hold in every reachable state, including the initial one.
	Invariants []Named `json:"invariants"`

	// Terminal marks states that are allowed to have no enabled action. A
	// state with nothing enabled that does not match this is reported as a
	// deadlock. Omit it to require that every state can always take a step.
	Terminal Expr `json:"terminal,omitempty"`

	// Pin, when set above zero, is the number of reachable states this model
	// is expected to have. A change to that count fails the run, so a refactor
	// that quietly widens or narrows the state space has to be acknowledged.
	Pin int `json:"pin,omitempty"`
}

// Action is one possible step: a guard that says when it may run, and the
// assignments it makes when it does.
type Action struct {
	Name   string            `json:"name"`
	Guard  Expr              `json:"guard,omitempty"`
	Effect map[string]string `json:"effect"`
}

// Named is a labelled expression, used for invariants so a violation can be
// reported by name rather than by shape.
type Named struct {
	Name string `json:"name"`
	Expr Expr   `json:"expr"`
}

// Expr is an expression in the JSON form: either a bare string (a variable
// reference or a value literal) or an array whose first element is an
// operator. Nil means "always true".
//
//	["==", "state", "claimed"]
//	["and", ["==", "state", "claimed"], ["!=", "token", "none"]]
//	["in", "state", "reported", "accepted"]
type Expr any

// Load reads a model from a JSON file and checks it over before returning it.
func Load(path string) (*Model, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Model
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Validate rejects a model that cannot be walked honestly: an undeclared
// variable, a value outside its domain, an incomplete starting state, or a
// name collision that would make an expression ambiguous.
func (m *Model) Validate() error {
	if len(m.Vars) == 0 {
		return fmt.Errorf("model declares no vars")
	}
	for name, domain := range m.Vars {
		if len(domain) == 0 {
			return fmt.Errorf("var %q has an empty domain", name)
		}
		seen := map[string]bool{}
		for _, v := range domain {
			if seen[v] {
				return fmt.Errorf("var %q lists value %q twice", name, v)
			}
			seen[v] = true
		}
	}

	// A bare string in an expression is read as a variable when it names one
	// and as a literal otherwise. If a value shares a variable's name that
	// reading is ambiguous, so refuse the model rather than guess.
	for name, domain := range m.Vars {
		for _, v := range domain {
			if _, isVar := m.Vars[v]; isVar {
				return fmt.Errorf("var %q may hold %q, which is also a var name: rename one so expressions stay unambiguous", name, v)
			}
		}
	}

	if len(m.Init) != len(m.Vars) {
		missing := []string{}
		for name := range m.Vars {
			if _, ok := m.Init[name]; !ok {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return fmt.Errorf("init does not set: %s", strings.Join(missing, ", "))
		}
	}
	for name, value := range m.Init {
		if err := m.checkValue(name, value); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}

	if len(m.Actions) == 0 {
		return fmt.Errorf("model declares no actions")
	}
	names := map[string]bool{}
	for i, a := range m.Actions {
		if a.Name == "" {
			return fmt.Errorf("action %d has no name", i)
		}
		if names[a.Name] {
			return fmt.Errorf("two actions are named %q", a.Name)
		}
		names[a.Name] = true
		if len(a.Effect) == 0 {
			return fmt.Errorf("action %q has no effect, so it cannot change anything", a.Name)
		}
		if err := m.checkExpr(a.Guard); err != nil {
			return fmt.Errorf("action %q guard: %w", a.Name, err)
		}
		for name, value := range a.Effect {
			if _, ok := m.Vars[name]; !ok {
				return fmt.Errorf("action %q assigns undeclared var %q", a.Name, name)
			}
			// The right-hand side is another variable's value or a literal.
			if _, isVar := m.Vars[value]; isVar {
				continue
			}
			if err := m.checkValue(name, value); err != nil {
				return fmt.Errorf("action %q: %w", a.Name, err)
			}
		}
	}

	invNames := map[string]bool{}
	for i, inv := range m.Invariants {
		if inv.Name == "" {
			return fmt.Errorf("invariant %d has no name", i)
		}
		if invNames[inv.Name] {
			return fmt.Errorf("two invariants are named %q", inv.Name)
		}
		invNames[inv.Name] = true
		if inv.Expr == nil {
			return fmt.Errorf("invariant %q has no expression", inv.Name)
		}
		if err := m.checkExpr(inv.Expr); err != nil {
			return fmt.Errorf("invariant %q: %w", inv.Name, err)
		}
	}
	if err := m.checkExpr(m.Terminal); err != nil {
		return fmt.Errorf("terminal: %w", err)
	}
	if m.Pin < 0 {
		return fmt.Errorf("pin cannot be negative")
	}
	return nil
}

func (m *Model) checkValue(name, value string) error {
	domain, ok := m.Vars[name]
	if !ok {
		return fmt.Errorf("undeclared var %q", name)
	}
	for _, v := range domain {
		if v == value {
			return nil
		}
	}
	return fmt.Errorf("var %q cannot hold %q (domain: %s)", name, value, strings.Join(domain, ", "))
}

// checkExpr walks an expression looking for operators that do not exist and
// operand counts that cannot be evaluated.
func (m *Model) checkExpr(e Expr) error {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case string:
		return nil
	case bool:
		return nil
	case []any:
		if len(v) == 0 {
			return fmt.Errorf("empty expression")
		}
		op, ok := v[0].(string)
		if !ok {
			return fmt.Errorf("expression does not start with an operator")
		}
		args := v[1:]
		switch op {
		case "==", "!=":
			if len(args) != 2 {
				return fmt.Errorf("%q takes 2 operands, got %d", op, len(args))
			}
		case "not":
			if len(args) != 1 {
				return fmt.Errorf("%q takes 1 operand, got %d", op, len(args))
			}
		case "and", "or":
			if len(args) == 0 {
				return fmt.Errorf("%q takes at least 1 operand", op)
			}
		case "in":
			if len(args) < 2 {
				return fmt.Errorf("%q takes a term and at least 1 value", op)
			}
		default:
			return fmt.Errorf("unknown operator %q", op)
		}
		for _, a := range args {
			if err := m.checkExpr(a); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("expression has unsupported type %T", e)
	}
}

// State is one assignment of every variable.
type State map[string]string

// Key renders a state as a stable string, used to recognise a state already
// seen. Sorting keeps it independent of map order.
func (s State) Key() string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(s[name])
	}
	return b.String()
}

func (s State) clone() State {
	out := make(State, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
