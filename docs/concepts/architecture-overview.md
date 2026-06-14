---
title: Architecture Overview
description: How Gas City's six primitives — Agent, Bead, Formula, Rig, Pack, and Event — hang together to route and process multi-agent work, and the machinery that runs them.
---

Gas City is an orchestration-builder SDK for composing multi-agent coding
workflows. This page is the mental model to carry into the
[Tutorials](/tutorials/index): the major parts, how work travels, and how agents
get spawned and talk. Everything maps onto commands you can run with `gc` — no
internal engineering notes required. For the authoritative per-concept model,
read the [Primitives Reference](/concepts/primitives) first.

## What Gas City does

Gas City **orchestrates fleets of coding agents** through real engineering work.
You write a **formula** — a method for how a job gets done — and the
**controller** runs it as a graph: it decomposes the job into beads, fans the
ready ones out to as many agents as the work allows, holds each step back until
its dependencies close, retries what fails, drains convoys in parallel, and
drives the whole graph to completion *outside your session*.
[Orders](/tutorials/07-orders) trigger formulas on a schedule or an event;
health patrol keeps the fleet alive. **This orchestration is the point.**

What makes it an *SDK* and not one fixed orchestrator: the controller hardcodes
**zero roles** — no built-in "manager" or "reviewer." Every role is
configuration supplied through a **Pack**, and the whole orchestration is
composed from the six primitives, so the same engine becomes Gas Town, Ralph, or
whatever you configure. The SDK owns only the role-agnostic **infrastructure**
the orchestrator runs on:

| Machinery | What it is |
| --- | --- |
| **Bead store** | where every bead (unit of work) lives — and survives an agent crash, so the controller always has ground truth to resume from |
| **Sessions** | the live process behind a running agent |
| **Event bus** | fires activity outward so humans and agents can watch |
| **Controller** | the orchestrator — runs formulas, drives the graph, keeps the fleet in sync |

None of this machinery knows what your agents do; it's the plumbing the SDK owns,
serving the six primitives you actually reason about.

## The six primitives

![The six Gas City primitives and how they relate: a Pack configures Agents,
Formulas, and Orders (the City is your local pack); a Formula operates over a
convoy of Beads; an Agent executes work in a Rig; and the whole system fires
Events for humans and agents to observe.](../diagrams/excalidraw-rendered/primitives.svg)

| Primitive | Role | Is | Key idea |
| --- | --- | --- | --- |
| **Agent** | WHO | a configured worker — name, provider (e.g. `claude`), prompt template, routing query, plus operational knobs ([Config reference](/reference/config)) | configuration, so define as many as you like; the SDK assumes none exists |
| **Bead** | WHAT | one unit of work — ID, title, status, type | the universal substrate: tasks, mail, sessions, convoys are all beads differing only by `type` |
| **Formula** | HOW | a reusable, written-down method applied over work | applying it *produces* work: `formula.toml` materializes as beads that outlive the file and any session |
| **Rig** | WHERE | an external project (usually a git repo) registered with the city | each rig gets its own beads namespace, isolated by `issue_prefix` |
| **Pack** | CONFIGURES | the unit of configuration — declares agents, formulas, orders | the City *is* a pack: the one rooted at this deployment |
| **Event** | OBSERVE | an outbound notification fired by activity | *fired, not polled*; follow live with `gc events --follow` |

### Agent — WHO

A **session** is a single *running* instance of an agent — a live process (a
`tmux` pane by default) the SDK can start, stop, prompt, and observe. Sessions
are derived from the Agent primitive, not their own primitive: one agent can run
as many sessions (a *pool*). Sessions are ephemeral; the work they did survives
them, because work lives in beads.

<Accordion title="Controller actions on sessions: adoption and scaling">
- **Adoption.** If the controller restarts and finds agent processes still
  running, it *adopts* them — reconnecting to live panes and recording a session
  bead each — instead of killing and respawning. Nothing is lost across a
  controller restart.
- **Scaling.** A pool agent is sized to demand each tick via its `scale_check`
  query: more pending work spawns more sessions (up to a max), idle capacity
  retires (down to a min).
</Accordion>

### Bead — WHAT

A **convoy** is a container bead grouping related beads so you track a batch as a
unit; **dependencies** are edges between beads. Both derive under the Bead
primitive. Because work persists in beads, not sessions, the system converges: if
an agent dies, its beads stay open and a fresh agent's hooks pick up the same work.

### Formula — HOW

A formula is a written-down method for getting a job done. Apply one and the
controller compiles it into a graph of beads, fans the ready steps out to as many
agents as the dependencies allow, holds each step back until its inputs close,
retries what fails, and drives the graph to completion — all outside your session.
You set that graph running three ways, all derived under this primitive:

```bash
gc sling <agent> "<work>"    # create AND route in one motion
gc formula cook <formula>    # create the work without routing it
# Order — automate WHEN a formula runs (no human runs a verb)
```

An **Order** pairs a trigger (`cooldown`, `cron`, `condition`, `event`, or
`manual`) with an action in `order.toml`; when it fires, the controller
instantiates the formula and routes the instance to the order's pool. **Health
Patrol** is order-driven controller work: each tick the controller evaluates due
order triggers and fires them.

### Rig — WHERE

A rig's directory lives anywhere on disk — it need not sit inside the city.
Register one with `gc rig add <path>` (recorded by absolute path). Isolation is
by `issue_prefix`, not a separate database: the city and all its rigs share one
underlying store, and `bd` filters every read and write to the current scope's
prefix. See [Beads Storage Topology](/reference/internal/beads-topology).

### Pack — CONFIGURES

The City is the root pack and imports shared packs, so an imported pack's agents,
formulas, and orders read exactly like locally declared ones. A city is a
directory created with `gc init`:

```text
my-city/
├── pack.toml / city.toml   # local pack: agents, formulas, orders
├── .gc/                    # runtime state
└── .beads/                 # the city's own work store
```

It also tracks the rigs you register (whose directories live anywhere — see
[Rig](#rig--where)). A city has exactly one long-running **controller** that
keeps everything reconciled.

### Event — OBSERVE

Different activity fires different events:

| Source | Events |
| --- | --- |
| Beads | `bead.created` / `bead.closed` |
| Sessions | `session.woke` / `session.crashed` |
| Convoys | `convoy.created` / `convoy.closed` |
| Order runs | `order.fired` / `order.completed` |

Parts of the system coordinate through shared bead state (read by the controller
each tick); events are how *observers* keep up. The delivery mechanism — the
event bus — is [machinery](#the-machinery), below.

## The machinery

Three pieces of plumbing run the primitives. You configure no role around any of
them.

**Bead store** — the universal persistence substrate beneath the Bead primitive;
every bead is a row. One interface: create, read, update, close, list, query by
label, walk parent/child. Backed by Dolt through the `bd` CLI, with **one** Dolt
server per city: the city root and every rig hold a `.beads/` directory but all
resolve to that single server, kept logically separate by `issue_prefix`. See
[Beads Storage Topology](/reference/internal/beads-topology).

**Event bus** — the delivery machinery beneath the Event primitive. Two tiers:
critical events on a bounded queue for infrastructure, and optional
fire-and-forget events for audit. Observers watch reactively. The bus only
delivers; coordination happens through shared bead state, not by reading the bus.

**Controller** — the per-city reconciliation runtime that drives all
infrastructure. On a 30-second ticker (and immediately when `city.toml` changes),
it compares running sessions against what your config declares and drives reality
toward it:

- spawn missing sessions
- scale agent pools
- fire due orders so their formulas run
- garbage-collect expired ephemeral work
- restart stalled sessions

There is no separate "desired state" file: the declaration **is** the local
pack's `city.toml`. The controller does all of this with no user-configured agent
running — keeping infrastructure healthy is the SDK's job; user agents only
execute work.

## How the pieces fit

The local pack (your city) wraps a controller and a bead store, registers rigs,
and runs agents as live sessions. The controller is the orchestrator: it drives
each formula's bead graph forward — fanning ready steps to agents, gating on
dependencies, retrying failures — while reconciling the sessions that do the work.
The event bus sits alongside as the channel fired events flow out on.

![Structural diagram of a Gas City: the city wraps the controller, beads store,
and event bus, with rigs and live agent sessions inside it. Config declares the
desired state to the controller; the controller reconciles sessions — spawning,
stopping, and restarting them — and reads/writes the store and event bus;
sessions create, claim, and update work in the store and emit activity to the
event bus. No arrow runs from a session back to the controller: agents signal it
only indirectly, through the store and event
bus.](../diagrams/excalidraw-rendered/architecture-structure.svg)

The diagram contains no specific role. Remove an agent from `city.toml` and the
infrastructure keeps working — only that agent's work stops flowing. And **no
arrow runs from a session back to the controller**: agents never call it
directly; they influence it only by writing to the store and event bus, which the
controller reads next tick. The loop closes through shared state, which is why the
controller keeps running as agents come and go.

## The life of a piece of work

Tracing **one bead** from the command line to a finished result:

1. **You sling.** `gc sling <agent> "<description>"` kicks it off.
2. **The store records and routes.** A work bead is created and routed by running
   the target agent's routing query (typically just assign or label).
3. **The controller reconciles.** Next tick, it sees ready work routed to an
   agent with no live session and spawns one through its runtime provider.
4. **The session gets its prompt.** Handed its rendered priming prompt, it follows
   "if it's on your hook, run it" and queries the store for hooked work.
5. **The agent executes.** Editing files in the rig, running commands — emitting
   events as it goes so observers (including you, via `bd show --watch`) see live
   state.
6. **The bead is updated and closed.** The agent records progress and closes the
   bead. The session may stop or stay warm; either way the result persists.

![Lifecycle diagram of one piece of work: you sling it, the beads store records
and routes the bead, the controller reconciles and spawns a session, the session
receives its rendered priming prompt and queries its hooked work, the agent
executes in the rig, and the bead is updated and closed. Each step is recorded
on the event bus, and you watch live status with bd show
--watch.](../diagrams/excalidraw-rendered/work-lifecycle.svg)

### The characteristic shape: a formula as a graph

That trace is the simplest path: one bead, one agent. The characteristic Gas City
job is bigger — you sling a **formula** and the controller compiles it into a
graph, materializes a root bead plus one bead per step, then fans the ready steps
out to many agents at once, gating each on its dependencies and retrying failures
until the whole graph closes. Same machinery, many agents, driven outside your
session. Related work can also be grouped into a **convoy**.

## Spawning, lifecycle, and communication

**Spawning.** Agents are never started by name in Go. The controller spawns a
session when reconciliation decides one is needed — for a fixed agent in config,
or for an extra pool instance when `scale_check` reports more work. The prompt
template, rendered at spawn time, is that session's entire behavioral spec.

**Lifecycle.** Sessions are disposable. The controller probes liveness; it
restarts a stall with backoff, replaces a crash, and adopts a live session if the
controller itself restarted. Because the work is a bead and the assignment is a
hook on it, nothing is lost when a session dies — a fresh one resumes exactly
where the record says.

**Communication.** Agents coordinate with no new primitive:

| Mechanism | Folds into | What it is |
| --- | --- | --- |
| **Mail** | Bead | a bead with `message` type; an inbox is a query for open message beads addressed to the agent; archiving is closing the bead |
| **Nudge** | Agent (session op) | fire-and-forget text typed into a running agent's session to prod it |

## A runnable example

<Warning>
[Install Gas City](/getting-started/installation) before running this example.

If `gc` opens a git commit editor instead of running, see the Oh My Zsh note in
[Troubleshooting](/getting-started/troubleshooting#oh-my-zsh-git-plugin-hides-gc).
</Warning>

The smallest end-to-end path — create a city, register a rig, route work, watch
it run:

```bash
# 1. Create and start a city (controller comes up automatically)
gc init ~/bright-lights
cd ~/bright-lights

# 2. Register a project directory as a rig
mkdir ~/hello-world && cd ~/hello-world && git init && cd -
gc rig add ~/hello-world

# 3. Sling a work item — creates a bead and routes it
cd ~/hello-world
gc sling claude "Create a script that prints hello world"

# 4. Watch the work bead progress as the agent executes it
bd show <bead-id> --watch
```

## Where to go next

- [Primitives Reference](/concepts/primitives) — the deeper per-concept reference
  for the six primitives.
- [Quickstart](/getting-started/quickstart) — the path above, in a few minutes.
- [Tutorial 01: Cities and Rigs](/tutorials/01-cities-and-rigs) — the guided
  end-to-end walkthrough.
- [Tutorial 06: Beads](/tutorials/06-beads) — the work store that underpins
  everything here.
- [Beads Storage Topology](/reference/internal/beads-topology) — how a city and
  its rigs share one store.
- [Reference](/reference/index) — command, config, formula, and provider lookup.
