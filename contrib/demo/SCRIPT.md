# Gas City Demo — Presenter Script

> **Audience**: The Gas Town team (they wrote it, they use it daily).
> **Duration**: ~10 minutes, interactive.
> **Thesis**: Everything hardcoded in Gas Town is a swappable interface in
> Gas City. Three config changes go from empty to full Gas Town — the
> daemon live-reconciles on every save.

---

## Before You Start

```bash
DEMO_SCRIPTS=~/lifecycle-demo/demo-repo/.claude/worktrees/demo-updates/contrib/demo

# Full 3-act run:
$DEMO_SCRIPTS/run-lifecycle-demo.sh all

# Or individual acts:
$DEMO_SCRIPTS/run-lifecycle-demo.sh act1
```

Each act script handles its own city setup, daemon start, and teardown —
no manual `gc daemon` commands needed.

The demo uses deterministic bash agents throughout — no Claude API calls,
fully reproducible, zero cost. Every agent is a `start_command` script.

---

## Act 1 — "Simple to Advanced" (4 min)

**Script**: `act1-pack-escalation.sh`

**Goal**: Three config changes, three capability levels. Start from nothing,
end at full Gas Town. The daemon live-reconciles on every save.

### The Progression

**Step 1: Empty city + wasteland-feeder** (no rigs, no agents)
```toml
[workspace]
name = "my-city"
includes = ["packs/wasteland-feeder"]
```
Just an order. Polls Wasteland, contributes inference. Anyone can join.

**Step 2: Uncomment the `[[rigs]]` block** (swarm pack)
```toml
[[rigs]]
name = "demo-repo"
path = "/path/to/demo-repo"
includes = ["packs/swarm-lifecycle"]
```
Now you have a coder pool + merger. Flat peers, shared dir, batch commits.
The daemon detects the city.toml change and spins up agents.

**Step 3: Change `swarm-lifecycle` → `lifecycle`**
```
includes = ["packs/swarm-lifecycle"]  →  includes = ["packs/lifecycle"]
```
Save. Daemon tears down coders + merger, spins up polecats + refinery.
Branches, worktrees, merge handoff. Full Gas Town.

### Screen Layout

```
┌──────────────────────┬──────────────────────┐
│ city.toml (editor)   │ gc status (live)     │
├──────────────────────┼──────────────────────┤
│ gc events --follow   │ peek-cycle.sh        │
└──────────────────────┴──────────────────────┘
```

### What to Say

> "Step 1: six lines of TOML. This city has no agents — just a wasteland
> order. It polls the global pool and contributes inference. Anyone
> can set this up in 30 seconds."

> "Step 2: I uncomment the rigs block. Save. The daemon detects
> the city.toml change, reconciles, and now I have five coders and a merger.
> Flat peers, shared directory, batch commits."

> "Step 3: I change one line — the pack reference. Save. The daemon
> tears down the swarm agents and spins up polecats and a refinery.
> Feature branches, worktrees, merge coordination. **Full Gas Town.
> Three config changes from zero.**"

Show `gc config explain` at each step — provenance shifts from
`swarm-lifecycle/pack.toml` to `lifecycle/pack.toml`.

**[PAUSE — the jaw-drop: three saves, zero to Gas Town]**

---

## Act 2 — "Same Pack, Different Infrastructure" (2 min)

**Script**: `act2-provider-swap.sh`

**Goal**: The audience just saw lifecycle running on local tmux. Now show
the same pack in Docker containers — same beads, same gc commands,
different provider config. One config line change.

### Screen Layout

```
┌──────────────────────┬──────────────────────┐
│ city.toml (editor)   │ gc status (live)     │
├──────────────────────┼──────────────────────┤
│ gc events --follow   │ docker ps (live)     │
└──────────────────────┴──────────────────────┘
```

- Bottom-right shows `docker ps --filter label=gc` — empty until Docker enabled.

### The Swap

1. City starts with local providers — tmux sessions, bd beads, file events.
2. Presenter uncomments the `[session]` block in city.toml. Saves.
3. Presenter runs `gc stop` — the auto-restart loop picks up the Docker provider.
4. Agents restart inside Docker containers. Same pack, same beads.

### What to Say

> "You just saw lifecycle running on tmux. Same pack, same beads.
> Now watch — I uncomment one config line. The session provider."

> "Save. Stop the daemon. It auto-restarts with the Docker provider.
> Same polecat pool, same refinery, same merge coordination —
> now running in Docker containers with mounted work directories.
> **The pack doesn't know or care what's underneath.**"

> "Same pack, tmux to Docker — one config line."

**[PAUSE — familiar pattern, unfamiliar flexibility]**

---

## Act 3 — "EKS Scale-Up" (3 min)

**Script**: `act3-eks-scaleup.sh`

**Goal**: Start with 5 agents on EKS. New project approved, new budget.
Edit pool.max from 5 to 200. Save. Watch 200 pods materialize and
drain the queue. One number change.

### Screen Layout

```
┌─────────────────────────────┬─────────────────────────────┐
│ Controller logs             │ kubectl get pods -w          │
│                             │                              │
│ Shows reconciliation,       │ Wall of pods materializing   │
│ pool scaling decisions.     │ when you scale up.           │
├─────────────────────────────┼─────────────────────────────┤
│ gc events --follow          │ mcp-mail dashboard           │
│                             │                              │
│ Events flooding in as       │ Inter-agent mail traffic     │
│ agents claim + close.       │ at scale.                    │
├─────────────────────────────┴─────────────────────────────┤
│ Progress: 42/200 complete  ████████░░░░░░░░  Open: 158   │
└───────────────────────────────────────────────────────────┘
```

### The Scale Moment

1. Show 5 agents working through a small backlog — familiar, comfortable.
2. "New project just came in. Budget approved."
3. Seed 200 work beads: `seed-hyperscale.sh 200`
4. Open city.toml. Change `max = 5` to `max = 200`. Save.
5. Watch the pods pane. 200 pods materialize. Progress bar climbs.

### What to Say

> "Five agents on EKS. Working through a small backlog. Normal Tuesday."

> "New project came in. Budget approved. I need 200 workers."

> "I change one number in city.toml. pool.max: 5 → 200. Save."

> [Point at the pods pane]

> "Watch. The daemon detected the change. It's reconciling. Pods are
> materializing. 10... 50... 100... 200 workers. All claiming beads,
> all processing, all reporting through mcp-mail. The progress bar is
> draining the queue."

> "One number. That's the difference between a 5-person team and a
> 200-person team. **No deploy. No pipeline. No ticket. One number.**"

**[PAUSE — the pods materializing IS the jaw-drop]**

---

## Closing — "Where This Goes" (1 min)

> "Three acts. Three things you couldn't do with Gas Town."

> "Act 1: Three edits, zero to Gas Town. The pack layer is composable.
> Wasteland federation is a one-line opt-in. Development patterns are
> config, not code."

> "Act 2: Same pack, different infrastructure. The provider layer is
> pluggable. New provider? Config change. Custom exec adapter? Write a
> script. If it proves itself, promote it to a built-in."

> "Act 3: One number change, 5 to 200 agents. The scale layer is
> declarative. The daemon reconciles desired state. Kubernetes-style.
> Erlang-style self-healing underneath."

> "We didn't rebuild Gas Town. We decomposed it into stable interfaces.
> The interfaces are stable. The implementations evolve. That's Gas City."

---

## Quick Reference — What We Borrowed

| Source | Gas City Concept | What It Gives Us |
|--------|-----------------|------------------|
| **Erlang/OTP** | Convoys (supervision trees), drain/resume, mail (message passing), max_restarts + restart_window | Self-healing, graceful lifecycle, crash isolation |
| **Kubernetes** | Daemon (controller), patrol loop (reconciliation), pools (ReplicaSets), packs (Helm charts), fsnotify config watch, `gc config explain` (provenance) | Declarative desired-state convergence, live reconciliation |
| **OCI** | Formulas (portable work units), `gc build-image` (container images), remote packs (registries) | Portable, versioned, shareable work definitions |

---

## Scripts

| Script | Purpose |
|--------|---------|
| `run-lifecycle-demo.sh` | Top-level 3-act orchestrator |
| `act1-pack-escalation.sh` | Act 1: Pack escalation (wasteland → swarm → lifecycle) |
| `act2-provider-swap.sh` | Act 2: Provider swap (local → Docker) |
| `act3-eks-scaleup.sh` | Act 3: EKS scale-up, 5 → 200 agents |
| `narrate.sh` | Source for `narrate()`, `pause()`, `step()`, `countdown()` |
| `progress.sh` | Live bead completion counter |
| `peek-cycle.sh` | "Security camera" cycling through agent sessions |
| `seed-hyperscale.sh` | Seeds N work beads for hyperscale pool |

---

## Troubleshooting

```bash
# Daemon not running:
gc daemon start --city ~/demo-city

# Agents stuck:
gc restart --city ~/demo-city

# Daemon logs:
gc daemon logs --city ~/demo-city

# Event history:
gc events --city ~/demo-city

# Check health:
gc doctor --city ~/demo-city
```
