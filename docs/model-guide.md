# Model guide

Commands in this guide run from the repository root.

## The model

```json
{
  "name": "toggle",
  "vars": { "light": ["off", "on"] },
  "init": { "light": "off" },
  "actions": [
    { "name": "flip_on",  "guard": ["==", "light", "off"], "effect": {"light": "on"} },
    { "name": "flip_off", "guard": ["==", "light", "on"],  "effect": {"light": "off"} }
  ],
  "invariants": [
    { "name": "light_is_known", "expr": ["in", "light", "off", "on"] }
  ]
}
```

- **vars** declare every variable and the complete set of values it can hold.
  The state space is the product of these, so keep the domains small.
- **init** is the single starting assignment and must set every variable.
- **actions** are the possible steps. Any action whose guard holds may run;
  when several hold, the walk explores all of them. An action with no guard is
  always enabled. Every assignment reads the state as it was *before* the
  action, so two variables can trade values in one step.
- **invariants** must hold in every reachable state, including the first.
- **terminal** (optional) marks states allowed to have nothing left to do. A
  dead end that does not match it is reported, because silently stopping is how
  a queue loses work. Omit it to require that every state can take a step.
- **pin** (optional) is the expected number of reachable states. See below.

### Expressions

An expression is either a bare string or an array whose first element is an
operator: `==`, `!=`, `not`, `and`, `or`, `in`.

```json
["and", ["==", "phase", "claimed"], ["!=", "token", "none"]]
["in", "phase", "reported", "accepted"]
```

A bare string is a variable when it names a declared one and a value literal
otherwise, which is what lets `["==", "w1tok", "attempt"]` compare two
variables while `["==", "phase", "queued"]` compares one against a constant.
A model where a value collides with a variable name is refused at load rather
than guessed at.

## The pin

`pin` records how many states the model reaches. When it is set and the walk
disagrees, that is a finding.

This turns the size of the state space into a regression test. Adding a guard
clause that quietly removes half the interleavings, or a variable that doubles
them, stops being invisible. Update it deliberately:

```sh
walk pin models/fleet-jobs.json
```

`pin` edits that one number and leaves the rest of the file byte for byte
alone, so a hand-formatted model stays hand-formatted.

## Worked example: durable jobs

`models/fleet-jobs.json` models the job lifecycle in
[workbench](https://github.com/itsHabib/workbench)'s `fleet job`: a queue with
renewable claims, lease expiry, attempt tokens that fence late results, and
explicit acceptance. Two workers, two attempt tokens, 43 reachable states.

The invariants are the acceptance criteria from that subsystem's own
documentation, restated as things that must hold everywhere:

| Invariant | What it means |
|---|---|
| `one_live_attempt` | two workers never hold the current attempt at once |
| `report_matches_attempt` | a reported result came from the attempt that is current |
| `accepted_was_reported` | nothing is accepted that was never reported |
| `claimed_has_holder` | a claimed job has a holder and a token |
| `queued_is_unheld` | a queued job holds no lease, holder or token |

As documented, the model is clean.

Its documentation also names the defect it is guarding against: *old tokens
cannot complete or renew a replacement attempt.* Remove the holder and attempt-token checks from
`complete_w1`'s guard, keeping the phase and lease checks, and the model admits this
four-step failure (the CLI may report an even shorter witness):

```
 0. init             attempt=none done=none holder=none lease=none  minted=none phase=queued   w1tok=none w2tok=none
 1. claim_a1_w1      attempt=a1   done=none holder=w1   lease=live    minted=a1   phase=claimed  w1tok=a1   w2tok=none
 2. lease_expires    attempt=a1   done=none holder=w1   lease=expired minted=a1   phase=claimed  w1tok=a1   w2tok=none
 3. claim_a2_w2      attempt=a2   done=none holder=w2   lease=live    minted=a2   phase=claimed  w1tok=a1   w2tok=a2
 4. complete_w1      attempt=a2   done=a1   holder=w2   lease=live    minted=a2   phase=reported w1tok=a1   w2tok=a2
```

Worker 1's lease lapses, worker 2 takes the job over with a fresh token, and
worker 1 reports anyway. The job is now marked reported carrying a result from
an attempt nobody is waiting on.

`TestUnfencedCompletionIsCaught` applies that mutation to the real model file
rather than to a copy, so the test and the model cannot drift apart. If the
guard is ever loosened for real, the test fails.

To be clear about what this does and does not show: the model is a
transcription of documented behavior, not of the Go source, so a clean walk
says the design holds, not that the implementation matches it. Finding a real
defect would mean modelling the code path by path.

## Second example: how far an agent gets alone

`models/gate-autonomous.json` asks a different question. Not "is this design
sound" but "where does autonomy run out". It models a pull request moving
through the review panel and the merge gate, with only the moves an agent can
make itself: trigger the panel, run the gate, push a fix, merge on a pass. The
operator's judgment is deliberately absent from the action list.

Eighteen states, and the safety rules hold everywhere:

| Invariant | What it means |
|---|---|
| `merge_needs_a_passing_run` | nothing merges without a passing gate run |
| `merge_is_pinned_to_the_reviewed_head` | the run that passed judged the head being merged |
| `a_run_names_the_head_it_judged` | no run exists without the head it was made against |

The walk finds exactly one ending that is not a merge, nine steps deep:

```
 1. panel_finds_something   head=h1
 2. gate_park_c0            head=h1  parked
 3. fix_round_1             head=h2
 4. panel_finds_something   head=h2
 5. gate_park_c1            head=h2  parked
 6. fix_round_2             head=h3
 7. panel_finds_something   head=h3
 8. gate_park_c2            head=h3  parked, cycles exhausted
```

Every path where the panel eventually finds nothing reaches a merge. The single
path where it finds something on *every* head does not, and cannot: both fix
rounds are spent and the cycle cap is reached.

The same model settles a second question. The grant's cycle cap of three is
documented as matching two fix rounds. The walk shows the correspondence is
tighter than roughly equal: **no reachable state is ready to gate while standing
at the cap.** After the third run the pull request is parked or passed, and
getting back to a gateable position costs a fix round, which by then is spent.
Three is the exact bound for two rounds, never slack and never the binding
constraint. So widening a grant's ceiling cannot rescue a parked pull request,
because the ceiling was never in the way.

That result contradicted the guess that prompted the test, which is the point of
writing the model down rather than reasoning about it.

The dead end is a liveness result, not a safety one. The
merge gate is not broken. It is working exactly as designed, and its design
makes the operator the only way out of that corner. The model declares that
state terminal, which is the claim that ending there is correct rather than a
bug. `TestGateRunsOutOfAutonomousMoves` withdraws the declaration and asserts
the dead end reappears, so the claim stays honest.

## Bounds and honesty

- The two-token bound is a modelling choice. Once both tokens are minted the
  queue cannot be claimed again, so that state is declared terminal. This result applies to the chosen bound; a larger domain must be checked separately.
- `-limit` caps the walk. Hitting it is reported as a finding, never as a
  clean result, because a small confident wrong answer is worse than none.
- An invariant is reported once, at the shortest path that breaks it. A second
  state breaking the same rule is almost always the same defect reached another
  way, and one trace is what actually gets debugged.

## Commands

```
walk check  <model.json>   walk the model and report what breaks
walk pin    <model.json>   record the current reachable-state count
walk states <model.json>   list every reachable state

  -json    write the report as JSON
  -limit   stop after this many states
```

`check` exits 0 when clean, 1 when it found something, 2 when the model itself
could not be read, which makes it usable as a CI step.

