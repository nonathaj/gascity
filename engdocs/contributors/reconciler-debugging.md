---
title: Reconciler Debugging
description: How to use gc trace when the session reconciler behaves unexpectedly.
---

## When To Use This

Use this workflow when the session reconciler does something unexpected:

- a template does not start when you expect it to
- a session drains, restarts, or quarantines unexpectedly
- a config change appears to be ignored
- acceptance or integration tests fail in controller or lifecycle paths

The trace stream is persisted locally under `.gc/runtime/session-reconciler-trace/`.

If you see `gc convoy control --serve` warning about a legacy control-dispatcher
trace path at `${GC_CITY}/control-dispatcher-trace.log`, treat it as a rollout
action item, not just a symptom: any long-lived control-dispatcher session that
still carries that baked-in `GC_WORKFLOW_TRACE` must be restarted or recycled
after the upgrade so it picks up the watcher-safe default under
`.gc/runtime/control-dispatcher-trace.log`.

## Fast Incident Workflow

From the city root, start detail tracing on the exact normalized template:

```bash
gc trace start --template repo/polecat --for 20m
```

If you want live visibility while reproducing:

```bash
gc trace tail --template repo/polecat --since 5m
```

After the bug happens, collect the high-signal summary first:

```bash
gc trace status
gc trace reasons --template repo/polecat --since 20m
gc trace show --template repo/polecat --since 20m --type cycle_result --json
```

From the suspicious `cycle_result`, grab the `tick_id`, then dump the full cycle and the full time window:

```bash
gc trace cycle --tick <tick_id> > /tmp/polecat-cycle.json
gc trace show --template repo/polecat --since 20m --json > /tmp/polecat-trace.json
```

When you are done:

```bash
gc trace stop --template repo/polecat
```

## What To Send An Agent

Point the next agent at these artifacts:

- city path
- exact normalized template, for example `repo/polecat`
- what you expected and what actually happened
- approximate UTC time window
- `gc trace reasons --template <template> --since <window>`
- `/tmp/<template>-trace.json` from `gc trace show ... --json`
- suspicious `tick_id`
- `/tmp/<template>-cycle.json` from `gc trace cycle --tick ...`
- controller stdout or stderr for the same window
- `.gc/events.jsonl` for the same window
- anything under `.gc/runtime/session-reconciler-trace/quarantine/` if it exists

If a real session existed and the bug crossed into runtime behavior, also include the relevant session or provider logs.

Drain-ack stop completion is event-first: newer controllers record
`SessionStopped` with message `drain acknowledged by agent` instead of relying
on the old `Stopped drain-acked session` stdout line. Use `.gc/events.jsonl`
and the trace `operation` records as the durable signal, with stdout/stderr as
supporting diagnostics only.

## Measuring The Drain-Ack Rate

`session.drain_acked_with_assigned_work` fires when a session acknowledges a
drain while a bead was still assigned to it: that work is stranded and has to be
recovered by another observer. One event is a curiosity. A sustained *rate* is
the claim/drain loop — sessions handed work and drained faster than they can run
it — so the distinction only shows up as a rate, and a fix for that loop can
only be demonstrated by measuring one.

`gc doctor` reports it as a number:

```bash
gc doctor --drain-ack-window 30m | grep drain-ack-rate
```

```
✓ drain-ack-rate — drain-ack rate: 0.00/h (count=0, window=30m0s, observed=30m0s)
```

The window is caller-specified (`--drain-ack-window`, default 30m) and the check
reads the append-only event log, walking rotation archives, so windows longer
than the active segment still resolve.

Read `observed` before trusting the rate. It is the span the event log actually
covers inside the requested window, and it is what the rate is divided by. When
the log is younger than the window — a fresh city, a rotated-away history — the
result says `partial coverage` and the number describes only the observed span:

```
⚠ drain-ack-rate — drain-ack rate: 1.89/h (count=615, window=500h0m0s, observed=324h34m30s)
  — partial coverage: the event log does not reach back the full window, so this
  is not evidence for the whole window
```

A zero over a partially covered window is not evidence that the window was
clean; it is evidence that nothing was observed for as long as you asked. To
show a loop is fixed, confirm `observed` equals the window you claim.

The check is advisory and never gates a doctor run. To inspect the underlying
events — each payload carries session, bead, template, and bead status at drain
time — list them directly:

```bash
gc events --type session.drain_acked_with_assigned_work --since 30m
```

## Rig-Scoped Convergence Rollback

Before rolling back a release that has created rig-scoped convergence loops,
stop active loops in each affected rig:

```bash
gc --rig <rig-name> converge list
gc --rig <rig-name> converge stop <bead-id>
```

Older controllers only watch the city/HQ convergence store. If rollback happens
with active rig-scoped convergence beads still present, those loops become
crash-orphans until a controller with rig-scoped convergence support runs again.

## How To Read The Trace

These record types are usually the fastest path to the bug:

- `cycle_result`: per-tick rollup, dropped records, reason and outcome counts
- `template_tick_summary`: why a template did or did not produce work
- `template_config_snapshot`: effective config and provenance for the tick
- `decision`: branch choices inside the reconciler
- `operation`: scale check, start, interrupt, and drain boundary calls
- `mutation`: bead or runtime writes that actually landed

## Acceptance And Integration Failures

For acceptance or integration failures, keep baseline tracing as-is and collect trace artifacts on failure. Prefer template-scoped detail tracing only for tests that intentionally exercise reconciler or lifecycle behavior.

On failure, collect at least:

```bash
gc trace status
gc trace reasons --since 15m
gc trace show --since 15m --type cycle_result --json
gc trace show --since 15m --json
```

For tests that know the target template ahead of time, arm tracing in setup:

```bash
gc trace start --template repo/polecat --for 15m
```

Then dump the template-scoped window on failure:

```bash
gc trace show --template repo/polecat --since 15m --json
```
