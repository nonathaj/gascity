---
title: "Understanding Packs"
description: Learn what packs are, how imports work, and how Gas City turns reusable pack files into city behavior.
---

# Understanding Packs

Every reusable capability in Gas City comes from a pack. A pack tells Gas City
what can be loaded: agents, named sessions, formulas, skills, commands, MCP
configuration, defaults, and the files those definitions need while running.
We will look at the two main parts of a pack: the files a pack author writes
and the loading rules Gas City uses when a city imports the pack.

Keep in mind that a pack is not a running thing by itself. A pack is input to
the pack loader. The loader reads `pack.toml`, discovers definitions from
well-known pack directories, follows imports, applies patches and defaults, and
produces the effective city configuration that the controller runs.

The [pack contract specification](/specs/pack-spec) is the public source
of truth for the exact format. This guide explains the same model in a more
tutorial style.

## The Pack Directory

A pack is a directory with a `pack.toml` file. Only `pack.toml` is required,
but most useful packs also include agents, prompt templates, scripts, formulas,
or other support files.

Here is a small pack:

```text
review-pack/
  pack.toml
  agents/
    reviewer/
      agent.toml
      prompt.md
  formulas/
    review.toml
```

The `pack.toml` file names the pack:

```toml
[pack]
name = "review-pack"
schema = 2
version = "1.0.0"
```

The agent definition lives in `agents/reviewer/agent.toml`:

```toml
scope = "city"
provider = "codex"
default_sling_formula = "review"
```

The agent directory gives the agent its local name, `reviewer`. Because the
directory contains `prompt.md`, the loader discovers that prompt by convention.
If another city imports this pack, it does not need to copy `prompt.md`; the
file still belongs to the pack that declared it.

## Pack Identity

Every pack has identity metadata in `[pack]`.

| Field | Meaning |
|---|---|
| `name` | The pack product name and provenance label. |
| `schema` | The pack file-format version. |
| `version` | Optional pack release metadata. |

If the loader cannot understand the schema, it stops. That is deliberate. It is
better for Gas City to reject a pack whose format it does not know than to load
only part of it and leave the city in a surprising state.

Every city also has a root pack. The root pack is the pack rooted at the city
directory: the directory that contains `city.toml` and `pack.toml`. The root
pack is where the city keeps its own reusable definitions, imports, and local
pack metadata.

## Registries

Registries are new in Gas City. They solve the problem that appears as soon as
packs become shareable: people need a way to find a pack, inspect what it is,
and copy the right durable source coordinate without memorizing repository
layout.

A registry is a catalog of pack records. A registry record can tell `gc` the
pack name, summary, version metadata, and durable source. Commands such as
`gc pack registry search` and `gc pack registry show` read that catalog.

Registry handles are command-time lookup handles. A registry command can show a
handle such as `main:gastown`, but checked-in TOML should store the durable
`source` that registry entry points to. The registry helps you choose the
source; the source is what makes the city portable.

Registry commands help you discover and inspect packs. They do not change the
authored import by themselves; the import remains ordinary TOML that names a
durable source.

## Imports

Packs can depend on other packs. An import is a named dependency from one pack
or city to another pack source.

The smallest import names a durable source:

```toml
[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
```

The table name `gastown` is the local binding. The `source` is the durable
locator for the pack. A source string is interpreted by the pack resolver; it is
not necessarily a browser-dereferenceable URL. For Git-backed pack sources, the
source identifies both the repository and, when needed, the subdirectory that
contains the pack root. In the example above, the `//gastown` suffix is part of
the Gas City resolver coordinate; it tells `gc` which pack directory to use
inside the Git repository.

| Field | Required | Meaning |
|---|---|---|
| `source` | yes | Durable pack source. |
| `version` | no | Compatibility range or exact pin for versioned sources. |

## Pack Imports And Rig Imports

An import can be loaded at the city level or for a specific rig.

An import that belongs to the importing pack appears at the top level of that
pack's `pack.toml`:

```toml
[imports.review]
source = "../packs/review"
```

This local-directory source is a reference to a directory on the filesystem,
not a stable shared package coordinate. It is useful for local development or
for packs that live together in the same repository. If another machine does
not have the same relative directory layout, this import will not resolve
there.

If that pack defines a city-scoped agent named `reviewer`, the runtime agent is
named `reviewer`:

```text
reviewer
```

Older city configs may still have top-level imports in `city.toml`. The current
loader accepts that for compatibility, but when the city has a root
`pack.toml`, new imports belong in `pack.toml`.

A rig-level import appears under the `[[rigs]]` table that needs it:

```toml
[[rigs]]
name = "checkout-service"
path = "../checkout-service"

[rigs.imports.review]
source = "../packs/review"
```

If that same pack defines a rig-scoped agent named `reviewer`, the runtime
agent is stamped with the rig name:

```text
checkout-service/reviewer
```

The rig `name` becomes the identity prefix. The rig `path` is the filesystem
location of the project. These are different pieces of information.

## Agent Scope

An agent in a pack can say where it is allowed to load.

```toml
scope = "city"
provider = "codex"
prompt_template = "prompt.md"
```

The `scope` field has three useful states.

| Scope | Meaning |
|---|---|
| omitted | The agent is eligible for city-level and rig-level loading. |
| `city` | The agent loads only when the pack is imported at the city level. |
| `rig` | The agent loads only when the pack is imported for a rig. |

The scope says where the definition is available. It does not name a particular
rig. A rig-scoped agent becomes part of a rig only when that rig imports the
pack.

This distinction matters for root packs. The root pack is implicitly available
to the city, but rig-scoped definitions still need a rig import or default-rig
import rule before they become rig agents.

## Names

Agent names are local names. Import bindings do not become part of runtime
agent names.

If a city imports this dependency:

```toml
[imports.review_tools]
source = "../packs/review"
```

and the imported pack defines `agents/reviewer/agent.toml`, the runtime name is:

```text
reviewer
```

Gas City uses the binding to find and order dependencies while loading config.
It does not use the binding as a runtime namespace.

If two packs define city-level agents with the same name, config load fails.
The same rule applies inside a single rig. Give one of the agents a different
public name, or avoid importing both definitions onto the same surface.

## Defaults

Defaults fill in blanks after packs have loaded. They are city policy, so
`[agent_defaults]` belongs in `city.toml`.

```toml
[agent_defaults]
default_sling_formula = "review"
```

This default applies only to agents whose `default_sling_formula` is still
blank. If a pack explicitly sets the field on an agent, the explicit value
wins.

Use defaults when the city wants a broad local policy. Use patches when you
want to change one named definition.

## Patches

A patch changes an agent that already exists. It does not create a new agent.

Here is a city-level patch:

```toml
[[patches.agent]]
name = "reviewer"
provider = "codex"
session_setup_append = ["tmux set status-left '[review]'"]
```

The `name` field selects the local agent name. For a rig-scoped agent, use
`dir` to select the rig identity prefix:

```toml
[[patches.agent]]
dir = "checkout-service"
name = "reviewer"
provider = "codex"
```

Here, `dir` is the rig name, not the rig path.

Pack-level patches should patch definitions the pack can actually see while it
is loading. A reusable pack should not guess the names of consumer rigs.

## Loading Order

The loader applies packs, patches, and defaults in a deterministic order. The
details matter when two layers set the same field.

In simplified form, loading works like this:

```text
1. Read city.toml and the root pack.
2. Load imported packs.
3. Apply pack-level agent patches inside each pack load.
4. Load city-level imports.
5. Apply city-level patches.
6. Load rig-level imports and stamp rig agents.
7. Apply rig overrides.
8. Apply pack globals.
9. Apply city agent defaults to fields that are still blank.
```

The later operation wins for replacement-style fields. Defaults are last, but
they only fill blanks, so they do not override explicit values from earlier
layers.

## Versioning And Locking

The `[pack].version` field is pack metadata. Import version selection is
controlled by the importing file and by the lockfile, not by comparing
`[pack].version` directly during load.

With no `version` field, the import says "use this source" and leaves the exact
selected revision to the installer and lockfile:

```toml
[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
```

A semver-style constraint says which compatible releases are acceptable:

```toml
[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
version = "^1"
```

An exact SHA pin says which revision must be used:

```toml
[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
version = "sha:d3617d1319a1206ac85f69ba024ec395c49c6f4b"
```

The meaning of `version` depends on the source and registry record. After you
edit a remote import, run `gc import install` to write or repair `packs.lock`
and materialize the local cache. Use `gc import check` for a read-only cache
and lockfile validation pass.

The authored import expresses the source and optional constraint. The lockfile
records the exact resolved dependency state. Once the cache and lockfile are
current, normal city loading uses the local resolved pack instead of
re-fetching the remote source on every load.

## Choosing Where To Put A Change

When you customize a pack, choose the narrowest place that expresses what you
mean.

| If you want to... | Put it here |
|---|---|
| Reuse another pack | `[imports.<binding>]` with `source` and optional `version`. |
| Make a city-wide local policy | `city.toml` defaults or patches. |
| Change one city-level imported agent | `city.toml` `[[patches.agent]]`. |
| Change one rig-level imported agent | The rig's `[[rigs.overrides]]` or a targeted city patch with `dir`. |
| Ship reusable behavior | The pack's own definitions and support files. |
| Pin an exact resolved dependency | The lockfile, not the authored import. |

If you are unsure, start by importing the pack unchanged and validating the
composed city:

```text
$ gc config show --validate
```

Then add the smallest default, patch, or rig override that describes the local
customization. A reusable pack is easier to upgrade when local policy stays in
the city that owns it.
