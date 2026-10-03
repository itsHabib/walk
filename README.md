# walk

**Find the shortest path to a broken invariant.**

Walk is a small finite-state model checker written in Go. Give it a JSON model
of your states, actions, and invariants. It explores every reachable state
within the configured limit and returns a shortest counterexample for each
broken rule.

```text
claim → lease expires → another worker takes over → old worker reports
                                                   ^ whose result wins?
```

Queues, leases, retries, and stale workers are hard to reason about in your
head. A small model makes the possible interleavings explicit—and gives you a
trace you can turn into a regression test.

[Try it](#try-it) · [Break a model](#make-it-fail) · [Model guide](docs/model-guide.md) · [Implementation](check.go)

## Try it

Requires **Go 1.26 or newer**. No services, API keys, or dependencies outside the
Go standard library.

```sh
git clone https://github.com/itsHabib/walk.git
cd walk
go build -o walk ./cmd/walk
./walk check models/fleet-jobs.json
```

```text
fleet-jobs: 43 states, 70 transitions, depth 6
clean
```

The example models two workers, expiring leases, two attempt tokens, and an
explicit acceptance step. “Clean” means its invariants hold for every reachable
state in this finite model.

For an installed CLI, use `go install github.com/itsHabib/walk/cmd/walk@latest`.
The example models live in this repository; installation alone does not copy them.

## Make it fail

The model refuses a completion from the wrong worker or attempt. Remove those
checks in a scratch copy and keep the phase and lease checks. This example also
needs Python 3:

```sh
python3 - <<'PYTHON'
import json
from pathlib import Path
model = json.loads(Path("models/fleet-jobs.json").read_text())
model.pop("pin", None)
for action in model["actions"]:
    if action["name"] == "complete_w1":
        action["guard"] = ["and", ["==", "phase", "claimed"],
                           ["==", "lease", "live"]]
Path("unfenced-job.json").write_text(json.dumps(model, indent=2) + "\n")
PYTHON
./walk check unfenced-job.json
```

The command exits **1** and reports a broken `report_matches_attempt` invariant.
One short path allows completion without ever giving worker 1 a token; the
[worked example](docs/model-guide.md#worked-example-durable-jobs) also explains
the stale-worker interleaving. Walk reports the shortest witness it finds, not
necessarily the scenario you had in mind.

Delete the scratch file when finished: `rm unfenced-job.json`.

## What you can model

| Question | Example |
|---|---|
| Can stale work be accepted? | [Durable jobs](models/fleet-jobs.json): claims, expiry, retries, and attempt fencing |
| Can a bounded workflow get stuck? | [Review and merge](models/gate-autonomous.json): revisions, review results, and exhausted fix rounds |
| Did an edit silently remove interleavings? | A `pin` records the expected reachable-state count |

A model declares finite variable domains, an initial state, guarded actions,
and invariants. Optional terminal conditions distinguish legitimate endings
from unexpected dead ends. The [model guide](docs/model-guide.md) covers the
JSON format, expressions, simultaneous assignments, and both worked examples.

## Use it in a check

```sh
./walk check -json -limit 100000 models/fleet-jobs.json
./walk states models/fleet-jobs.json
./walk pin models/fleet-jobs.json
```

Flags go **before the model path**. `pin` updates the model file; `check` and
`states` only read it.

| `check` exit code | Meaning |
|---|---|
| `0` | Complete exploration within the limit; no findings |
| `1` | Invariant, dead-end, pin, or exploration-limit finding |
| `2` | Invalid model, arguments, or input error |

## How it works

Breadth-first search visits each reachable state once. A predecessor link keeps
the shortest route to each state; a failed invariant reconstructs that route.
The implementation is small enough to read in an afternoon:

1. [model.go](model.go) — decode and validate the finite model.
2. [expr.go](expr.go) — evaluate guards and invariants.
3. [states.go](states.go) — represent states and apply effects.
4. [check.go](check.go) — search, check rules, reconstruct traces.

```sh
go test -race ./...
go vet ./...
```

## Scope

This checks a **model**, not the source code of the system it describes. The
shipped models transcribe designs; a clean result does not establish that a
running implementation follows them. Finite bounds can miss behavior outside
those bounds, and the state space grows with the product of variable domains.

Walk has no temporal logic, fairness analysis, or refinement checker. Reaching
the state limit is a finding, never a clean result. Use it when a small explicit
model and a concrete trace can settle a design question.

## License

[MIT](LICENSE).
