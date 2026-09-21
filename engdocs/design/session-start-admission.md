---
title: "City Session Start Admission"
---

Pool planning alone cannot enforce the workspace concurrency ceiling: named and
manual sessions enter downstream of pool allocation. All launches therefore
share `session.StartAdmission` through the worker factory and the documented API
manager adapter. No runtime wrapper is used, preserving optional provider
interfaces. The policy does not choose work or kill sessions.

`workspace.max_active_sessions` is the launch ceiling. Nil or negative means
unlimited. `workspace.session_limit_exempt_templates` lists exact qualified
templates for non-model infrastructure. A command string or provider name never
implies exemption. Unrecognized live runtimes count conservatively.

Admission observes the city's composed runtime provider, not bead lifecycle
states. Runtime-list errors, including partial results, refuse new launches.
An optional runtime admission census includes pending Kubernetes pods and lets
tmux prove a named server is safely absent using the existing socket observer.
An unreachable live server is never treated as an empty city. Auto/hybrid
routers preserve this census across their backends.
Exemptions require an error-free session-store read. A conflicting session name
is exempt only if all its open records are exempt.

The city-local advisory `session-start-admission.lock` serializes runtime census
through completion of provider Start, across controller/API/CLI processes. It
stores no state; process death releases the lock. This deliberately serializes
cold launches to count in-flight starts without stale persisted reservations.
Waiting honors the launch context. The tradeoff is lower startup throughput,
not reduced capacity. A failed start releases the fence; any surviving runtime
is counted by the next observation. Existing runtimes are never stopped here.

This is a **runtime admission fence**, not the identifier/write fence in
`session-store-fences.md`: it never acquires identifier locks or writes session
metadata. Existing identity fences and incarnation checks remain unchanged.
Provider Start implementations must publish their runtime in the admission
census before returning, and must not synchronously request a nested city launch.
There are no counters, leases, or cleanup files to reconstruct on restart.

`ErrStartDeferred` is backpressure. The reconciler releases its in-flight wake
marker and leaves the pending session available for the next tick, without
rollback, crash accounting or quarantine. Callers receive an explicit error.

The contract is cooperative: all city start processes must run the patched
binary with the same city path/config and a city-wide provider view. Direct
external tmux/process creation cannot be prevented by this fence. Runtime
sessions that already exceed a lowered limit remain untouched; additional
non-exempt launches wait until enough sessions exit. Deployment must replace
the controller and CLI together before claiming enforcement.

Proof owners: `internal/session/start_admission_test.go` covers concurrent
admission, the OS-process lock boundary, cancellation, failed starts, census
errors, exemptions, unknown runtimes and over-limit preservation. CLI controller
and API tests prove their factory/manager wiring with fake providers.
