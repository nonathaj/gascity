---
title: Gastown on Gas City — Config Recipes
description: Task-oriented config overrides for running the Gastown pack on Gas City — register rigs, scale pools, swap providers, patch agents, and tweak prompts.
---

This page collects the common config edits for the Gastown pack — the changes
you reach for *while editing files*. The conceptual migration story, including
how Gas Town roles and mechanisms map onto Gas City primitives, lives in
[Coming from Gas Town](/getting-started/coming-from-gastown).

## Common Gastown Overrides

If you are using the Gastown pack, these are the most common local changes.

### Register a rig

Import the Gastown pack in the root pack, then bind rigs in `city.toml` and with `gc rig add`:

```toml
# pack.toml
[pack]
name = "my-city"
schema = 2

[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs/tree/main/gastown"
version = "sha:d3617d1319a1206ac85f69ba024ec395c49c6f4b"
```

```toml
# city.toml
[[rigs]]
name = "myproject"

[rigs.imports.gastown]
source = "https://github.com/gastownhall/gascity-packs/tree/main/gastown"
version = "sha:d3617d1319a1206ac85f69ba024ec395c49c6f4b"
```

```bash
gc rig add /path/to/myproject --name myproject
```

### Increase or shrink scalable polecat sessions

This is the cleanest answer to "I want more or fewer polecats for this rig."

```toml
# city.toml
[[rigs]]
name = "myproject"

[rigs.imports.gastown]
source = "https://github.com/gastownhall/gascity-packs/tree/main/gastown"
version = "sha:d3617d1319a1206ac85f69ba024ec395c49c6f4b"

[[rigs.patches]]
agent = "gastown.polecat"

[rigs.patches.pool]
max = 10
```

### Change the provider for one rig's polecats

```toml
# city.toml
[[rigs]]
name = "myproject"

[rigs.imports.gastown]
source = "https://github.com/gastownhall/gascity-packs/tree/main/gastown"
version = "sha:d3617d1319a1206ac85f69ba024ec395c49c6f4b"

[[rigs.patches]]
agent = "gastown.polecat"
provider = "codex"
```

You can combine that with session scale overrides, env, prompt changes, or hook changes on the same override block.

### Change a city-scoped Gastown agent

City-scoped agents such as `mayor`, `deacon`, and `boot` are easiest to tweak with patches:

```toml
[[patches.agent]]
name = "gastown.mayor"
provider = "codex"
idle_timeout = "2h"
```

Use patches when the target is already a concrete city-scoped agent. Use `[[rigs.patches]]` when the target is a pack agent stamped per rig.

### Add a named crew agent

Crew is usually city-specific, so it often belongs in the root city pack rather than in the shared Gastown pack:

```text
agents/wolf/
├── agent.toml
└── prompt.template.md
```

```toml
# agents/wolf/agent.toml
scope = "rig"
nudge = "Check your hook and mail, then act accordingly."
work_dir = ".gc/worktrees/myproject/crew/wolf"
idle_timeout = "4h"
```

That keeps the shared pack generic while still letting your city have named long-lived workers.

### Change a prompt, overlay, or timeout without forking the pack

This is what rig overrides are for:

```toml
# city.toml
[[rigs]]
name = "myproject"

[rigs.imports.gastown]
source = "https://github.com/gastownhall/gascity-packs/tree/main/gastown"
version = "sha:d3617d1319a1206ac85f69ba024ec395c49c6f4b"

[[rigs.patches]]
agent = "gastown.refinery"
idle_timeout = "4h"
```

For prompt or overlay replacement, patch the imported agent from your root city pack rather than editing the shared pack in place.

If that change turns out to be broadly useful across cities, that is when it should move into the pack.
