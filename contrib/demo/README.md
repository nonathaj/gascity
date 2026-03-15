# Gas City Lifecycle Demo

Deterministic three-act demo showcasing Gas City's architecture built on
the best of Erlang/OTP, Kubernetes, and OCI. All agents are bash scripts —
no Claude API calls, fully reproducible.

**Audience**: The Gas Town team. They wrote it, they use it daily.
**Thesis**: Everything hardcoded in Gas Town is a swappable interface in Gas City.

## Quick start

```bash
DEMO_SCRIPTS=~/lifecycle-demo/demo-repo/.claude/worktrees/demo-updates/contrib/demo

# Full 3-act run:
$DEMO_SCRIPTS/run-lifecycle-demo.sh all

# Individual acts:
$DEMO_SCRIPTS/run-lifecycle-demo.sh act1   # Pack escalation
$DEMO_SCRIPTS/run-lifecycle-demo.sh act2   # Provider swap (local → Docker)
$DEMO_SCRIPTS/run-lifecycle-demo.sh act3   # EKS scale-up
```

## Three Acts

### Act 1: Pack Escalation

**Script**: `act1-pack-escalation.sh`

Three manual edits to city.toml, three capability levels on a running city.
The daemon live-reconciles on every save via fsnotify.

1. **Wasteland-feeder only** — no rigs, no agents, just a global inference order
2. **Uncomment `[[rigs]]` block** — coder pool + merger appear (swarm pack)
3. **Change `swarm-lifecycle` → `lifecycle`** — polecats + refinery (full Gas Town)

### Act 2: Provider Swap

**Script**: `act2-provider-swap.sh`

Same lifecycle pack from Act 1, swapped from local tmux to Docker
containers. Uncomment the `[session]` block in city.toml, save, restart
the daemon, and agents move from tmux sessions into Docker containers.
Same beads, same pack — filesystem mounts keep everything in sync.

### Act 3: EKS Scale-Up

**Script**: `act3-eks-scaleup.sh`

Start with 5 agents on EKS. New project approved, new budget. Edit
`pool.max` from 5 to 200 in city.toml. Save. Watch 200 pods materialize
and drain the queue. One number change.

**Prerequisite**: K8s cluster with `gc` namespace.

Set `GC_HYPERSCALE_MOCK=true` (default) to avoid Claude API costs.

## Helper scripts

| Script | Purpose |
|--------|---------|
| `narrate.sh` | Source for `narrate()`, `pause()`, `step()`, `countdown()` |
| `progress.sh` | Live bead completion counter for a pool label |
| `seed-hyperscale.sh` | Seeds N work beads for the hyperscale pool |
| `peek-cycle.sh` | "Security camera" view cycling through agent sessions |

## Presenter notes

See [SCRIPT.md](SCRIPT.md) for the full presenter script with talking
points, transitions, and the Erlang/K8s/OCI framing.

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GC_SRC` | `/data/projects/gascity` | Path to gascity source tree |
| `DEMO_CITY` | `~/demo-city` | City directory for acts 1-2 |
| `ACT2_TIMEOUT` | `120` | Auto-teardown seconds for act 2 |
| `ACT3_TIMEOUT` | `300` | Auto-teardown seconds for act 3 |
| `GC_DOCKER_IMAGE` | `gc-agent:latest` | Docker image for act 2 (needs bash/git/tmux) |
| `GC_K8S_NAMESPACE` | `gc` | K8s namespace for act 3 |
| `GC_HYPERSCALE_MOCK` | `true` (via orchestrator) | Use shell mock for hyperscale |
| `EDITOR` | `nano` | Editor for live city.toml edits |
