---
title: "Using Packs"
description: Find reusable packs with gc, inspect registry records, and turn registry results into durable PackV2 imports.
---

# Using Packs

The [Understanding Packs](/guides/understanding-packs) guide describes the pack
format and loading rules. This guide starts with the workflow: how you find a
pack with `gc`, inspect it, and decide what durable import to commit.

Registries are new in Gas City. They are catalogs for reusable packs: a place
to search, inspect metadata, and find the durable source you should put in
checked-in TOML.

A registry uses short names to refer to packs in the registry, such as
`main:gastown`. When you import the pack by writing an `[imports.<binding>]`
directive by hand, the registry record gives you the durable `source` and
optional `version` to put in TOML. Once you write the import, the registry is
no longer involved in finding your imported pack.

The important distinction is this:

```text
registry handle  -> command input
durable source   -> checked-in TOML
```

You use a registry handle while talking to `gc`. You commit a durable `source`
after choosing the pack. When you need a compatible range or exact pin, you can
also commit `version`.

The `gc pack` surface is registry-only: `gc pack registry add`, `list`,
`remove`, `refresh`, `search`, and `show`. Installing, repairing, and checking
authored imports belongs to `gc import install` and `gc import check`.

## Search For A Pack

Use search when you know the kind of capability you want but not the exact pack
name.

```text
$ gc pack registry search gastown
```

Example output:

```text
PACK          VERSION  SUMMARY
main:gastown  1.0.0    Gas Town agents, sessions, and coordination defaults
```

The `PACK` column is a command handle. The part before `:` names the local
registry. The part after `:` names the pack record in that registry.

Search looks at registry metadata. It does not fetch every pack and search
inside the pack's prompts, scripts, or formulas.

If you do not know exactly what you are looking for, list the registry instead
of searching it:

```text
$ gc pack registry show main
```

Example output:

```text
PACK          VERSION  SUMMARY
main:gastown  1.0.0    Gas Town agents, sessions, and coordination defaults
main:review   1.0.0    Review agents, prompts, and formulas
main:triage   1.0.0    Issue and work-queue triage helpers
```

## Inspect A Registry Record

Before you import a pack, inspect the record.

```text
$ gc pack registry show main:gastown
```

Example output:

```text
Pack: main:gastown
Version: 1.0.0
Source: https://github.com/gastownhall/gascity-packs.git//gastown
Summary: Gas Town agents, sessions, and coordination defaults
```

The `Source` line is the durable source coordinate. It is interpreted by the
pack resolver, so it is not necessarily a browser-dereferenceable URL. In this
example, the `//gastown` suffix is part of the coordinate and tells `gc` which
pack directory to use inside the Git repository. That is the value a checked-in
PackV2 import should use.

If a pack name is unique across configured registries, `gc pack registry show`
can show it without the registry prefix. If the name is ambiguous, qualify it
with the registry name.

## Write The Import

After choosing a pack, commit a PackV2 import with `source`.

```toml
[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
```

The table name `gastown` is the local import binding inside this pack or city.
It does not become part of runtime agent names.

The registry handle stays out of the file. If your machine calls the registry
`main` and another machine calls it `first-party`, both machines can still load
the same committed import because the committed import uses the durable source.

## Versioning

The import may also include `version`. Use it when you want a compatible range
or exact pin instead of whichever revision the lockfile already selected.

```toml
[imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
version = "^1"
```

Use a constraint when you want the city to stay on a compatible release line.
Here, `^1` accepts compatible `1.x` releases from the registry record while
avoiding an automatic move to a future incompatible major version.

Exact resolved revisions belong in `packs.lock` or cache metadata. Authored
TOML expresses the constraint you want to keep.

## Use The Imported Pack

Imported agents use normal runtime names. Import bindings are not runtime
namespaces.

For a city-scoped agent:

```text
$ gc session attach reviewer
$ gc sling reviewer <bead-id>
```

For a rig-scoped agent:

```text
$ gc sling checkout-service/reviewer <bead-id>
```

The `checkout-service` part comes from the rig `name`, not from the import
binding and not from the rig filesystem `path`.

## Install Or Check Imports

After changing remote imports, install or repair the imported pack cache:

```text
$ gc import install
```

That command resolves the declared imports, writes or repairs `packs.lock`, and
materializes the imported packs in the local cache. It is the current
bootstrap and repair command for authored PackV2 imports.

The lockfile exists so the city can keep a stable resolved dependency graph.
The authored import says what you want, such as a source and optional version
constraint. `packs.lock` records what that resolved to on disk, so later loads
and checks can detect missing, stale, or changed imported pack state.

Use `gc import check` when you want a read-only validation pass:

```text
$ gc import check
```

`gc import check` reports missing, stale, or uncached import state and points
back to `gc import install` when repair is needed. Registry commands remain
discovery commands; they do not install or sync the authored import graph.

## Validate The City

After install/check succeeds, validate the composed configuration.

```text
$ gc config show --validate
```

Then inspect the part of the city you expect the pack to provide. For example,
if the pack provides a city-scoped `reviewer` agent:

```text
$ gc config show | rg 'reviewer'
```

If the imported pack provides rig-scoped agents, make sure the rig that needs
them imports the pack.

```toml
[[rigs]]
name = "checkout-service"
path = "../checkout-service"

[rigs.imports.gastown]
source = "https://github.com/gastownhall/gascity-packs.git//gastown"
```

That rig import makes the pack's rig-scoped definitions available to the
`checkout-service` rig.

## Check Your Registries

Registries are local machine configuration. A normal Gas City installation
already has the public `main` registry configured. Start by listing the
registries this machine knows about:

```text
$ gc pack registry list
```

Example output:

```text
NAME  SOURCE
main  https://github.com/gastownhall/gascity-packs.git
```

The name `main` is local. Another machine could call the same registry
`first-party`, `work`, or anything else. That is why registry names do not
belong in `pack.toml`.

The registry source points at a catalog, conventionally `registry.toml` in the
registry repository. That catalog is where pack records live: names, summaries,
versions, and durable sources.

Refresh cached registry records:

```text
$ gc pack registry refresh main
```

If you omit the registry name, `gc` refreshes every configured registry.

If you need to add a private team registry, give it a local name:

```text
$ gc pack registry add team https://example.com/team-packs.git
```

After that, `gc pack registry list` shows both registries:

```text
NAME  SOURCE
main  https://github.com/gastownhall/gascity-packs.git
team  https://example.com/team-packs.git
```

## Keep Registry State Local

Registry commands manage local discovery state. Pack imports manage shared city
state.

| Task | Command or file |
|---|---|
| Add a catalog to this machine | `gc pack registry add` |
| See configured catalogs | `gc pack registry list` |
| Refresh cached catalog records | `gc pack registry refresh` |
| Search for a reusable pack | `gc pack registry search` |
| Inspect a registry record | `gc pack registry show` |
| Share a chosen dependency with the team | `[imports.<binding>]` in checked-in TOML |
| Install or repair authored imports | `gc import install` |
| Check installed import state without mutating | `gc import check` |
| Validate the composed city | `gc config show --validate` |

This separation keeps local discovery flexible without making shared config
depend on the names or cache layout of one machine.
