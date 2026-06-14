---
title: Architecture Overview
description: How Gas City's six primitives — Agent, Bead, Formula, Rig, Pack, and Event — hang together to route and process multi-agent work, and the machinery that runs them.
---

Gas City is an orchestration-builder SDK: a toolkit for composing multi-agent
coding workflows. This page gives you the mental model you need before diving
into the [Tutorials](/tutorials/index) — what the major parts are, how work
travels through the system, and how agents get spawned and talk to each other.

The deepest, per-concept companion to this page is the
[Primitives Reference](/concepts/primitives) — read it first if you want the
authoritative model. Everything here is built on its **six primitives**: Agent
(WHO), Bead (WHAT), Formula (HOW), Rig (WHERE), Pack (CONFIGURES), and Event
(OBSERVE). You do **not** need to read any internal engineering notes to follow
along; everything here maps onto commands you can run with `gc`.

## The core idea: work is the primitive

The single most important thing to understand about Gas City is that
**orchestration is a thin layer on top of work tracking**.

The system does not hardcode any roles — there is no built-in "manager" or "reviewer" baked into
the binary. Instead, every role is supplied as configuration through a **Pack**,
and the SDK provides only the **infrastructure** — the role-agnostic machinery
that every orchestration needs no matter what the agents are actually *for*. That
machinery is:

- a place to store work — the **bead store**, where every bead lives
- a way to run agents — **sessions**, the live process behind a running agent
- a way to fire activity outward so others can watch — the **event bus**
- an engine that keeps them all in sync — the **controller**.

None of this machinery knows or cares what your agents do. That is what we mean
throughout this page by "infrastructure": the plumbing the SDK owns, as opposed
to the role behavior you supply as configuration. The machinery exists to serve
the **six primitives** — Agent, Bead, Formula, Rig, Pack, and Event — which are
what you actually reason about, and what the rest of this page is organized
around.

## The six primitives

Everything you reason about in Gas City is one of six primitives. Packs
**configure** agents, formulas, and orders; the local pack is your City. A
**Formula** is the method applied *over* work: it loops over a convoy of
**Beads** and fans each one out to an **Agent**, which executes those beads
*in* a **Rig**. As all of this happens, the city fires **Events** so humans and
agents can watch.

The five remaining pieces below are primitives; the [machinery](#the-machinery)
section that follows — bead store, event bus, controller — is the plumbing that
runs them.

### Agent — WHO does the work

An **agent** is a configured worker — the *who* of an orchestration. Agents are
pure configuration:

- a name
- the provider that backs them (for example `claude`)
- a prompt template that defines their behavior
- a query that says which work routes to them
- and many more operational knobs (see [Config reference](/reference/config) for the full list)...

Because agents are configuration, you can define as many as you like, and the
SDK never assumes any particular one exists.

A **session** is a single *running* instance of an agent — a live process (by
default a `tmux` pane) that the SDK can start, stop, prompt, and observe.
Sessions are derived from the Agent primitive, not a primitive of their own:
one agent can be instantiated into many running sessions (a *pool*). Sessions
are ephemeral — they come and go, and the work they were doing survives them,
because work lives in beads, not in the session.

Two particular controller actions on sessions are worth spelling out:

- **Adoption.** If the controller restarts and finds agent processes still
  running from before, it *adopts* them — reconnecting to the live panes and
  recording a session bead for each — instead of killing and respawning them.
  Nothing is lost across a controller restart.
- **Scaling up and down.** An agent can be configured as a *pool*. On each tick
  the controller runs the agent's `scale_check` query to size the pool to
  demand: more pending work spawns more sessions (up to a configured max), and
  idle capacity is retired (down to a configured min).

### Bead — WHAT the work is

A **bead** is a single unit of work — the *what*. It has an ID, a title, a
status, and a type. Beads are also the universal substrate for domain state:
tasks, mail messages, sessions, and convoys are all beads that differ only by
their `type`. A **convoy** is a container bead that groups related beads so you
can track a batch as a unit, and **dependencies** are edges between beads; both
are derived under the Bead primitive, not separate primitives.

Everything an agent executes is a bead, and the work persists in beads — not in
any session. That is why the system converges to correct outcomes even as
sessions churn: if an agent dies, its beads stay open, and a fresh agent's hooks
discover the same work and pick up where it left off.

### Formula — HOW the work gets done

A **formula** is *how* the work is carried out — a reusable, written-down method
applied *over* work. A formula is not the work, and it is not a grouping of
work: it loops over the beads of a convoy and fans each one out to an agent,
defining its own steps along the way. Applying a formula is what *produces*
work: the `formula.toml` compiles into a recipe that is materialized as beads in
the store, and those beads are the work that outlives both the file and any
session.

You apply a formula in a few ways, all derived under this one primitive:

- **Sling** (`gc sling`) creates *and* routes work in one motion: it resolves the
  target agent or pool, instantiates the formula, routes each bead, and nudges
  the agent to start.
- **Cook** (`gc formula cook`) creates the work without routing it.
- An **Order** automates *when* a formula runs. An `order.toml` pairs a trigger
  (`cooldown`, `cron`, `condition`, `event`, or `manual`) with an action, and
  when the trigger fires the controller instantiates the formula and routes the
  instance to the order's pool — no human runs a verb. **Health Patrol** is one
  kind of order-driven controller work: each tick the controller evaluates the
  due order triggers and fires them.

### Rig — WHERE the work happens

A **rig** is an external project registered with the city — the *where*. It is
usually a git repository you want agents to work in.

A rig's directory can live anywhere on
disk; it does **not** have to sit inside the city directory. You register one
with `gc rig add <path>`, which records the rig by its absolute path.

Each rig gets its own beads namespace and routing context, so work slung inside
one rig stays logically isolated from the others. That isolation is by
`issue_prefix`, not by a separate database: the city and all its rigs share one
underlying store, and `bd` filters every read and write to the current scope's
prefix. See [Beads Storage Topology](/reference/internal/beads-topology) for the details.

### Pack — what CONFIGURES everything

A **pack** is the unit of configuration: it declares the agents, formulas, and
orders an orchestration uses. The City is the local (root) pack; it imports
shared packs. So a **city** is not a thing apart from packs — it *is* a pack,
the one rooted at this deployment. Concretely, a city is a directory on disk
that contains:

- a `pack.toml` / `city.toml` config file declaring the local pack's agents,
  formulas, and orders
- a `.gc/` directory of runtime state
- a `.beads/` directory holding the city's own work store.

It also keeps track of the rigs you have registered — but a rig's own directory
can live anywhere on disk, inside or outside the city directory (see
[Rig](#rig--where-the-work-happens)).

You create a city with `gc init`. A city has exactly one long-running
**controller** process that keeps everything reconciled. Because the City is
just the local pack, an imported pack's agents, formulas, and orders read
exactly like the ones declared locally.

### Event — how you OBSERVE

An **event** is an outbound notification fired by activity, so humans and agents
can watch what the system is doing. Beads fire `bead.created` / `bead.closed`;
sessions fire `session.woke` / `session.crashed`; convoys fire
`convoy.created` / `convoy.closed`; order-driven runs fire `order.fired` /
`order.completed`. You follow them live with `gc events --follow`.

Events are *fired*, not polled: the primitive is the notification itself. The
**event bus** beneath them is just the delivery machinery — an append-only
pub/sub log that carries each fired event to whoever is watching. (Coordination
between the parts of the system happens through shared bead state, which the
controller reads each tick; events are how *observers* keep up.)

## The machinery

Three pieces of plumbing run the primitives above. None of them is something you
configure a role around — they are the SDK's infrastructure.

### Bead store

The **bead store** is the universal persistence substrate beneath the Bead
primitive: every bead is a row in it.

It holds tasks, mail messages, sessions, and convoys alike, and offers a single
interface — create, read, update, close, list, query by label, and walk
parent/child relationships.

By default it is backed by Dolt through the `bd` CLI. Physically there is **one** Dolt server per city.
The city root and every rig each hold a `.beads/` configuration directory, but they all resolve to
that single server, and their data is kept logically separate by `issue_prefix`.

Because all domain state flows through one interface, the system converges to correct
outcomes even as sessions churn. See
[Beads Storage Topology](/reference/internal/beads-topology) for where the files live and
how the prefix scoping works.

### Event bus

The **event bus** is the delivery machinery beneath the Event primitive: an
append-only pub/sub log that carries each fired event to whoever is watching.

It has two tiers:

- critical events on a bounded queue for infrastructure
- optional fire-and-forget events for audit.

Observers watch the bus reactively rather than polling. The bus only *delivers*
events; the parts of the system coordinate with each other through shared bead
state, not by reading the bus.

### Controller

The **controller** is the per-city reconciliation runtime — the engine that drives all infrastructure.

On a steady ticker (every 30 seconds by default), and
immediately whenever `city.toml` changes, it compares the running sessions
against the state your config *declares* and drives reality toward that
declaration:

- spawning missing sessions
- scaling agent pools
- firing due orders so their formulas run
- garbage-collecting expired ephemeral work
- restarting stalled sessions.

There is no separate "desired state" file you maintain. The declaration **is**
the local pack's `city.toml` — which agents should exist, and how many instances
each pool should run — and reconciliation is simply how the controller keeps the
live system matching it.

Crucially, the controller can do all of this with no
user-configured agent running: keeping the infrastructure healthy is the SDK's
job, and user agents only execute work.

## How the pieces fit together

Structurally, the local pack (your city) wraps a controller and a bead store,
registers one or more rigs, and runs agents as live sessions. The event bus
sits alongside as the channel each fired event flows out on so observers can
watch.

![Structural diagram of a Gas City: the city wraps the controller, beads store,
and event bus, with rigs and live agent sessions inside it. Config declares the
desired state to the controller; the controller reconciles sessions — spawning,
stopping, and restarting them — and reads/writes the store and event bus;
sessions create, claim, and update work in the store and emit activity to the
event bus. No arrow runs from a session back to the controller: agents signal it
only indirectly, through the store and event
bus.](../diagrams/excalidraw-rendered/architecture-structure.svg)

Notice what the diagram does *not* contain: any specific role.

The controller reconciles whatever agents the config declares. Remove an agent from
`city.toml` and the infrastructure keeps working — only that agent's work stops
flowing.

Notice, too, that **no arrow runs from a session back to the controller.** Agents
never call the controller directly. They influence it only by writing to the
beads store and event bus, which the controller reads on its next tick — the
loop closes through shared state, not direct calls. That is why the controller
can keep running even while every agent comes and goes.

## End-to-end: the life of a piece of work

Here we trace the life of **one single bead** — the simplest unit of work — from
the command line to a finished result: (after the diagram below is a list of some
more complex ways work enters the system)

1. **You sling.** `gc sling <agent> "<description>"` kicks off the work from the
   command line.
2. **The beads store records and routes.** A work bead is created and routed by
   running the target agent's routing query (which typically just assigns or
   labels the bead).
3. **The controller reconciles.** On its next reconciliation tick, the
   controller sees ready work routed to an agent that has no live session, and
   spawns one through its runtime provider.
4. **The session receives its prompt.** The new session is handed its rendered
   priming prompt and, following the system's "if it's on your hook, run it"
   principle, queries the beads store for the work hooked to it.
5. **The agent executes.** It does the work — editing files in the rig, running
   commands, and so on — emitting events on the bus as it goes so observers
   (including you, via `bd show --watch`) see the live state.
6. **The bead is updated and closed.** The agent records progress and closes the
   bead when done. The session may shut down or stay warm for the next item;
   either way the result persists in the store.

![Lifecycle diagram of one piece of work: you sling it, the beads store records
and routes the bead, the controller reconciles and spawns a session, the session
receives its rendered priming prompt and queries its hooked work, the agent
executes in the rig, and the bead is updated and closed. Each step is recorded
on the event bus, and you watch live status with bd show
--watch.](../diagrams/excalidraw-rendered/work-lifecycle.svg)

A lone bead _is not_ the only way work enters the system. It is just the
clearest place to start: the same infrastructure carries the richer shapes.
For instance you can also:

- sling a **formula** — a reusable *method* — which Gas City applies over a
  convoy of beads, materializing its steps as beads (a root bead plus one bead
  per step) and fanning them out to agents; the formula is the *how*, the beads
  are the work
- group related work into a **convoy**.

## Agent spawning, lifecycle, and communication

### Spawning

Agents are never started by name in Go code. The controller spawns
a session when reconciliation determines one is needed — for a fixed agent
declared in config, or for an additional pool instance when an agent's
`scale_check` query reports more work.

The prompt template is rendered at spawn
time and is the entire behavioral specification for that session.

### Lifecycle

Sessions are designed to be disposable.

The controller probes
them for liveness, and if one stalls it can restart it with backoff. If a
session crashes, the controller can replace it. If the
controller crashes and a session is alive and well, the controller can adopt it.

Because the work is a
bead and the assignment is a hook on that bead, nothing is lost when a session
dies — a fresh session picks up exactly where the work record says to.

### Communication

Agents coordinate without any new primitive — the two ways they talk fold back
into ones you already met:

- **Mail** is just a **bead** with a `message` type. An agent's inbox is a query
  for open message beads addressed to it; archiving a message is closing that
  bead.
- **Nudge** is a session-layer operation under the **Agent** primitive: text
  typed directly into a running agent's session to prod it. It is
  fire-and-forget.

There is nothing else to learn: mail is a bead, nudge is an operation on a
running agent.

## A runnable example

<Warning>
You will need to [install Gas City](/getting-started/installation) before running
this example.

If `gc` opens a git commit editor instead of running, see the Oh My
Zsh note in
[Troubleshooting](/getting-started/troubleshooting#oh-my-zsh-git-plugin-hides-gc).
</Warning>

Everything above is reachable from a handful of commands. This is the smallest
end-to-end path — create a city, register a rig, route work, and watch it run:

```bash
# 1. Create and start a city (controller comes up automatically)
gc init ~/bright-lights
cd ~/bright-lights

# 2. Register a project directory as a rig
mkdir ~/hello-world && cd ~/hello-world && git init && cd -
gc rig add ~/hello-world

# 3. Sling a work item to an agent — this creates a bead and routes it
cd ~/hello-world
gc sling claude "Create a script that prints hello world"

# 4. Watch the work bead progress as the agent executes it
bd show <bead-id> --watch
```

## Where to go next

- [Primitives Reference](/concepts/primitives) — the deeper, per-concept
  reference for the six primitives (Agent, Bead, Formula, Rig, Pack, Event)
  introduced above.
- [Quickstart](/getting-started/quickstart) — the same path above, in a few
  minutes.
- [Tutorial 01: Cities and Rigs](/tutorials/01-cities-and-rigs) — start the
  guided, end-to-end walkthrough that teaches the full user model.
- [Tutorial 06: Beads](/tutorials/06-beads) — go deeper on the work store that
  underpins everything here.
- [Beads Storage Topology](/reference/internal/beads-topology) — how a city and its rigs
  share one store under the hood.
- [Reference](/reference/index) — command, config, formula, and provider lookup.
