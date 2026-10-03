package walk

import (
	"encoding/json"
	"strings"
	"testing"
)

// tiny is a two-state model used where the fleet model would be noise.
const tiny = `{
  "name": "tiny",
  "vars": {"s": ["a", "b"]},
  "init": {"s": "a"},
  "actions": [
    {"name": "forward", "guard": ["==", "s", "a"], "effect": {"s": "b"}},
    {"name": "back", "guard": ["==", "s", "b"], "effect": {"s": "a"}}
  ]
}`

func parse(t *testing.T, doc string) *Model {
	t.Helper()
	var m Model
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return &m
}

// The fleet job lifecycle, modelled as its documentation describes it, must be
// clean. This is the baseline the mutation test below moves away from: if this
// model is not clean, the mutation test proves nothing.
func TestFleetJobsModelIsClean(t *testing.T) {
	m, err := Load("models/fleet-jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		for _, f := range rep.Findings {
			t.Errorf("unexpected %s %s: %s", f.Kind, f.Rule, f.Detail)
		}
	}
	if rep.States == 0 {
		t.Fatal("walked no states")
	}
}

// Dropping the fencing check from completion is the defect the fleet jobs
// documentation calls out by name: "Old tokens cannot complete or renew a
// replacement attempt." Removing that one guard clause must produce a
// counterexample, or the model's invariants are decoration.
//
// The mutation is applied to the real model rather than to a hand-written
// copy, so the two cannot drift apart.
func TestUnfencedCompletionIsCaught(t *testing.T) {
	m, err := Load("models/fleet-jobs.json")
	if err != nil {
		t.Fatal(err)
	}

	mutated := false
	for i, a := range m.Actions {
		if a.Name != "complete_w1" {
			continue
		}
		// Keep the phase and lease checks; drop only "the worker's token is
		// the current attempt".
		m.Actions[i].Guard = []any{"and",
			[]any{"==", "phase", "claimed"},
			[]any{"==", "lease", "live"},
			[]any{"!=", "w1tok", "none"},
		}
		mutated = true
	}
	if !mutated {
		t.Fatal("complete_w1 is not in the model; the mutation target moved")
	}
	// The pin describes the original model, not this one.
	m.Pin = 0

	rep, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}

	var found *Finding
	for i := range rep.Findings {
		if rep.Findings[i].Rule == "report_matches_attempt" {
			found = &rep.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("unfenced completion was not caught; findings: %+v", rep.Findings)
	}
	for i, s := range found.Trace {
		t.Logf("%2d. %-16s %s", i, s.Action, s.State.Key())
	}
	if len(found.Trace) < 2 {
		t.Fatalf("counterexample has no usable trace: %+v", found.Trace)
	}
	if got := found.Trace[0].Action; got != "init" {
		t.Errorf("trace starts at %q, want init", got)
	}
	last := found.Trace[len(found.Trace)-1]
	if last.Action != "complete_w1" {
		t.Errorf("trace ends with %q, want complete_w1", last.Action)
	}
	// The defect needs a second worker to have taken the attempt over, so the
	// shortest path cannot be shorter than: claim, expire, re-claim, complete.
	if len(found.Trace) < 5 {
		t.Errorf("trace is %d steps, too short to describe a takeover: %+v", len(found.Trace), found.Trace)
	}
}

// A walk is only worth trusting if the count it reports is stable, because the
// pin turns that count into a regression test.
func TestWalkIsDeterministic(t *testing.T) {
	m, err := Load("models/fleet-jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	first, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := Check(m, 0)
		if err != nil {
			t.Fatal(err)
		}
		if again.States != first.States || again.Transitions != first.Transitions || again.Depth != first.Depth {
			t.Fatalf("run %d walked %d/%d/%d, first walked %d/%d/%d",
				i, again.States, again.Transitions, again.Depth,
				first.States, first.Transitions, first.Depth)
		}
	}
}

func TestPinMismatchIsReported(t *testing.T) {
	m := parse(t, tiny)
	m.Pin = 99
	rep, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.States != 2 {
		t.Fatalf("walked %d states, want 2", rep.States)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != "pin" {
		t.Fatalf("want one pin finding, got %+v", rep.Findings)
	}

	m.Pin = 2
	rep, err = Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("correct pin should be clean, got %+v", rep.Findings)
	}
}

// A state with nothing left to do is a defect unless the model says otherwise,
// because silently stopping is how a queue loses work.
func TestDeadlockIsReportedUnlessTerminal(t *testing.T) {
	doc := `{
      "name": "dead-end",
      "vars": {"s": ["a", "b"]},
      "init": {"s": "a"},
      "actions": [{"name": "stop", "guard": ["==", "s", "a"], "effect": {"s": "b"}}]
    }`
	m := parse(t, doc)
	rep, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != "deadlock" {
		t.Fatalf("want one deadlock finding, got %+v", rep.Findings)
	}

	m.Terminal = []any{"==", "s", "b"}
	rep, err = Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("declared terminal state should be clean, got %+v", rep.Findings)
	}
}

// Every assignment in an action reads the state as it was before the action,
// so two variables can trade values in one step.
func TestEffectsApplySimultaneously(t *testing.T) {
	doc := `{
      "name": "swap",
      "vars": {"x": ["l", "r"], "y": ["l", "r"]},
      "init": {"x": "l", "y": "r"},
      "actions": [{"name": "swap", "effect": {"x": "y", "y": "x"}}],
      "terminal": true
    }`
	m := parse(t, doc)
	next, err := m.apply(m.Actions[0], State(m.Init))
	if err != nil {
		t.Fatal(err)
	}
	if next["x"] != "r" || next["y"] != "l" {
		t.Fatalf("swap produced x=%s y=%s, want x=r y=l", next["x"], next["y"])
	}
}

func TestValidateRefusesBadModels(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "value shares a var name",
			doc:  `{"vars":{"state":["idle","done"],"done":["yes","no"]},"init":{"state":"idle","done":"no"},"actions":[{"name":"a","effect":{"state":"done"}}]}`,
			want: "also a var name",
		},
		{
			name: "init misses a var",
			doc:  `{"vars":{"a":["x"],"b":["y"]},"init":{"a":"x"},"actions":[{"name":"a","effect":{"a":"x"}}]}`,
			want: "init does not set",
		},
		{
			name: "value outside its domain",
			doc:  `{"vars":{"a":["x"]},"init":{"a":"z"},"actions":[{"name":"a","effect":{"a":"x"}}]}`,
			want: "cannot hold",
		},
		{
			name: "assigns an undeclared var",
			doc:  `{"vars":{"a":["x"]},"init":{"a":"x"},"actions":[{"name":"a","effect":{"nope":"x"}}]}`,
			want: "undeclared var",
		},
		{
			name: "unknown operator",
			doc:  `{"vars":{"a":["x"]},"init":{"a":"x"},"actions":[{"name":"a","guard":["~=","a","x"],"effect":{"a":"x"}}]}`,
			want: "unknown operator",
		},
		{
			name: "wrong operand count",
			doc:  `{"vars":{"a":["x"]},"init":{"a":"x"},"actions":[{"name":"a","guard":["==","a"],"effect":{"a":"x"}}]}`,
			want: "takes 2 operands",
		},
		{
			name: "action with no effect",
			doc:  `{"vars":{"a":["x"]},"init":{"a":"x"},"actions":[{"name":"a","effect":{}}]}`,
			want: "no effect",
		},
		{
			name: "duplicate action names",
			doc:  `{"vars":{"a":["x","y"]},"init":{"a":"x"},"actions":[{"name":"a","effect":{"a":"y"}},{"name":"a","effect":{"a":"x"}}]}`,
			want: "two actions are named",
		},
		{
			name: "empty domain",
			doc:  `{"vars":{"a":[]},"init":{"a":"x"},"actions":[{"name":"a","effect":{"a":"x"}}]}`,
			want: "empty domain",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m Model
			if err := json.Unmarshal([]byte(c.doc), &m); err != nil {
				t.Fatalf("decode: %v", err)
			}
			err := m.Validate()
			if err == nil {
				t.Fatalf("validate accepted a model it should refuse")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestEval(t *testing.T) {
	m := parse(t, `{
      "vars": {"a": ["x", "y"], "b": ["x", "y"]},
      "init": {"a": "x", "b": "y"},
      "actions": [{"name": "n", "effect": {"a": "y"}}],
      "terminal": true
    }`)
	s := State{"a": "x", "b": "y"}

	cases := []struct {
		expr Expr
		want bool
	}{
		{nil, true},
		{[]any{"==", "a", "x"}, true},  // var against a literal
		{[]any{"==", "a", "b"}, false}, // var against a var
		{[]any{"!=", "a", "b"}, true},
		{[]any{"not", []any{"==", "a", "x"}}, false},
		{[]any{"and", []any{"==", "a", "x"}, []any{"==", "b", "y"}}, true},
		{[]any{"and", []any{"==", "a", "x"}, []any{"==", "b", "x"}}, false},
		{[]any{"or", []any{"==", "a", "y"}, []any{"==", "b", "y"}}, true},
		{[]any{"in", "a", "x", "y"}, true},
		{[]any{"in", "a", "y"}, false},
	}
	for i, c := range cases {
		got, err := m.eval(c.expr, s)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got != c.want {
			t.Errorf("case %d: got %v, want %v (%v)", i, got, c.want, c.expr)
		}
	}
}

// A model bigger than the limit must say so rather than report a small,
// confident, wrong answer.
func TestLimitIsReportedNotHidden(t *testing.T) {
	m := parse(t, tiny)
	rep, err := Check(m, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != "limit" {
		t.Fatalf("want a limit finding, got %+v", rep.Findings)
	}
}

func TestStateKeyIsOrderIndependent(t *testing.T) {
	a := State{"z": "1", "a": "2", "m": "3"}
	b := State{"m": "3", "z": "1", "a": "2"}
	if a.Key() != b.Key() {
		t.Fatalf("%q != %q", a.Key(), b.Key())
	}
}

// States and Check walk the same graph by different code paths, so they must
// agree on how many states there are. Disagreement means one of them is wrong.
func TestStatesAgreesWithCheck(t *testing.T) {
	for _, path := range []string{"models/fleet-jobs.json"} {
		m, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := Check(m, 0)
		if err != nil {
			t.Fatal(err)
		}
		states, err := States(m, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != rep.States {
			t.Fatalf("%s: States walked %d, Check walked %d", path, len(states), rep.States)
		}
		seen := map[string]bool{}
		for _, s := range states {
			if seen[s] {
				t.Errorf("%s: %q listed twice", path, s)
			}
			seen[s] = true
		}
	}
}

func TestStatesReportsItsLimit(t *testing.T) {
	m := parse(t, tiny)
	states, err := States(m, 1)
	if err == nil {
		t.Fatal("States hit its limit without saying so")
	}
	if len(states) != 1 {
		t.Fatalf("returned %d states, want the 1 it managed", len(states))
	}
}

// The gate model asks a different question from the fleet one: not "is this
// design sound" but "how far can an agent get on its own". Its safety rules
// must hold, and they do.
func TestGateModelIsClean(t *testing.T) {
	m, err := Load("models/gate-autonomous.json")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		for _, f := range rep.Findings {
			t.Errorf("unexpected %s %s: %s", f.Kind, f.Rule, f.Detail)
		}
	}
}

// The gate model declares one terminal state that is not a merge: parked, at
// the cycle cap, after both fix rounds. Declaring it terminal is the claim
// that this is where autonomy legitimately ends and the operator begins.
//
// Withdraw that declaration and the state must show up as a dead end, or the
// claim is unfounded. The single trace it produces is the shape every parked
// pull request in the portfolio arrives in: the panel finds something on every
// head, so no fix round ever reaches a clean gate.
func TestGateRunsOutOfAutonomousMoves(t *testing.T) {
	m, err := Load("models/gate-autonomous.json")
	if err != nil {
		t.Fatal(err)
	}
	// Only a merge is allowed to be an ending now.
	m.Terminal = []any{"==", "pr", "merged"}
	m.Pin = 0

	rep, err := Check(m, 0)
	if err != nil {
		t.Fatal(err)
	}

	var dead []Finding
	for _, f := range rep.Findings {
		if f.Kind == "deadlock" {
			dead = append(dead, f)
		}
		if f.Kind == "invariant" {
			t.Errorf("a safety rule broke, which is a different and worse problem: %s", f.Detail)
		}
	}
	if len(dead) != 1 {
		t.Fatalf("want exactly one dead end, got %d: %+v", len(dead), dead)
	}

	end := dead[0].Trace[len(dead[0].Trace)-1].State
	for name, want := range map[string]string{
		"pr":       "open",
		"run":      "parked",
		"cycles":   "c3",
		"head":     "h3",
		"findings": "actionable",
	} {
		if end[name] != want {
			t.Errorf("dead end has %s=%s, want %s", name, end[name], want)
		}
	}

	// Every step that produced it is the panel finding something again.
	panels := 0
	for _, s := range dead[0].Trace {
		if s.Action == "panel_finds_something" {
			panels++
		}
	}
	if panels != 3 {
		t.Errorf("the dead end took %d rounds of new findings, want 3", panels)
	}
}

// The grant default of three cycles is documented as matching two fix rounds.
// The walk settles what that correspondence actually is, and it is tighter than
// "roughly equal": the cap is never what stops you.
//
// No reachable state is ready to gate (panel complete, nothing in flight) while
// standing at the cap. After the third run the pull request is parked or passed,
// and returning to a gateable position takes a fix round, which by then is spent.
// So three cycles is the exact bound for two fix rounds: never slack, and never
// the binding constraint either. Widening a grant's ceiling cannot help a parked
// pull request, because the ceiling was never in the way.
func TestTheCycleCapIsNeverWhatBlocksAGateRun(t *testing.T) {
	m, err := Load("models/gate-autonomous.json")
	if err != nil {
		t.Fatal(err)
	}
	states, err := States(m, 0)
	if err != nil {
		t.Fatal(err)
	}

	reachesTheCap := false
	for _, s := range states {
		if strings.Contains(s, "cycles=c3") {
			reachesTheCap = true
		}
		readyToGate := strings.Contains(s, "pr=open") &&
			strings.Contains(s, "panel=complete") &&
			strings.Contains(s, "run=none")
		if readyToGate && strings.Contains(s, "cycles=c3") {
			t.Errorf("the cycle cap blocks a gate run that is otherwise ready: %s", s)
		}
	}
	if !reachesTheCap {
		t.Fatal("the cap is never reached at all; the model no longer exercises it")
	}
}
