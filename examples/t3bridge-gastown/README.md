# T3 Bridge Gas Town Example

This example shows the Gas Town pack shape we use to exercise the `t3bridge`
session provider against a T3Code fork.

The important pieces are:

- `city.toml` sets `[session].provider = "t3bridge"`.
- Agent templates still use normal GC provider selection, for example
  `provider = "codex"`.
- The pack defines rig-scoped Gas Town workers and their worktree layout.
- T3 runtime data is discovered by the bridge from `T3_WS_URL`, `T3_HOME`, or
  T3Code runtime state files.

## Test With A T3Code Fork

From the T3Code fork:

```sh
cd /data/projects/t3code
git checkout codex/sidebar-pool-followup
bun dev
```

This example was validated against `sfncore/t3code` branch
`codex/sidebar-pool-followup`.

If the T3Code server does not publish runtime state, export the WebSocket URL
explicitly before starting GC:

```sh
export T3_WS_URL=ws://127.0.0.1:3773/ws
```

From this repository:

```sh
go install ./cmd/gc
tmp="$(mktemp -d)"
cp -R examples/t3bridge-gastown "$tmp/city"
```

Edit `$tmp/city/city.toml` and set the sample rig path to a local checkout you
want agents to work in:

```toml
[[rigs]]
name = "gascity"
path = "/data/projects/gascity"
```

Then start the city:

```sh
gc start "$tmp/city"
gc status "$tmp/city"
gc session list --city "$tmp/city"
```

Expected behavior:

- GC creates sessions through T3Code, not tmux.
- T3Code threads receive GC metadata and startup envelope fields.
- Pool workers such as `gascity/polecat` use the configured worktree path.
- Legacy `session = "exec:...gc-session-t3"` configs still route to the native
  bridge provider, but new configs should prefer `[session].provider =
  "t3bridge"`.
