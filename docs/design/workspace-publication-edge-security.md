# Workspace Publication Edge Security

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-03-14 |
| Author(s) | Codex |
| Issue | N/A |
| Supersedes | N/A |
| Companion | `docs/design/workspace-service-publication.md` |

## Summary

This document is the normative edge-security profile for hosted
workspace publication. The companion publication design defines the
service model, lifecycle, and operator UX; this document defines the
security envelope that makes public and tenant-visible routes safe to
ship on a shared machine. The approved v0 scope is still single-machine
`gastown-hosted` publication only. Kubernetes, multi-node hosted
publication, custom domains, and identity propagation to applications
remain follow-on work.

The main decisions are:

- every published route is a separate browser site under a
  publication-only suffix
- the shared edge is the only GC-managed internet entrypoint
- the edge parses and reserializes HTTP before the relay hop; no raw
  client bytes are tunneled to the backend except upgraded WebSocket
  streams after policy checks
- relay admission is route-bound and short-lived, not just "UID plus a
  socket path"
- tenant auth is route gating only, with explicit cache and revocation
  semantics
- TLS keys, auth cookies, and audit identifiers are platform-owned and
  never become workspace-controlled data

## Motivation

The companion publication design was converged enough to specify the
hosted control plane, but it still left one review lane blocked:
security details that should not be left to implementation discretion.
The unresolved questions were concrete:

- how route hostnames avoid sibling-site cookie and origin bleed
- how the edge and relay mutually prove route identity
- where TLS private keys live, who can read them, and what happens on
  renewal failure or compromise
- what the auth cache and revocation window actually are
- what exact anti-smuggling and request-normalization contract the edge
  enforces
- how request IDs line up across edge, auth, relay, and audit records

This document answers those questions for the hosted v0 backend.

## Goals

- Make shared-domain publication safe enough for hostile tenants and
  hostile internet traffic.
- Fail closed on auth, TLS, and route-identity uncertainty.
- Prevent workspace apps from forging platform trust signals.
- Give operators concrete certificate, revocation, and audit behavior.
- Keep the hosted v0 slice implementable without pretending K8s and
  multi-node are already solved.

## Non-Goals

- Designing the Kubernetes publication security model.
- Supporting custom domains in v0.
- Forwarding trusted end-user identity into tenant applications in v0.
- Preventing arbitrary user-managed outbound tunnels from inside a
  workspace.
- Treating a compromise of `gc-edge` or the machine supervisor as
  recoverable inside this design. Those are trusted-computing-base
  failures.

## Threat Model

### Assets

- route ownership: which workspace and service a public hostname reaches
- auth decisions for `visibility = "tenant"` routes
- TLS private keys for publication base domains
- audit integrity for publish, revoke, and quarantine actions
- tenant boundary between workspace apps on the same machine

### Adversaries

- arbitrary internet clients attempting request smuggling, cache
  confusion, auth bypass, or abuse
- a malicious workspace app attempting to impersonate platform headers,
  consume sibling cookies, or reach another workspace's published route
- a compromised workspace process attempting to discover or connect to
  relay sockets
- operational drift such as stale routes, stale auth cache, or expired
  certificates

### Trusted Computing Base

Hosted v0 treats these components as trusted:

- machine supervisor publication controller
- `gc-edge`
- `gc-prober`
- `gc-certd` or the supervisor certificate-management helper
- the machine OS, kernel, and systemd unit boundary enforcing user,
  cgroup, and filesystem separation

If one of those components is compromised, the platform is compromised.
The design therefore focuses on isolating workspaces and internet
traffic from the TCB, not on sandboxing the TCB from itself.

## 1) Route Identity And Browser Isolation

### 1.1 Dedicated publication suffixes

`public_base_domain` and `tenant_base_domain` must be dedicated to
workspace publication only. They must not host login pages, product
marketing pages, operator dashboards, or other unrelated surfaces.

### 1.2 Public suffix requirement

Hosted multi-tenant publication requires browser site isolation at the
domain boundary. Therefore each publication base domain must be a
delegated public suffix, either by inclusion in the Public Suffix List
or by an equivalent mechanism with documented browser behavior. If the
operator cannot provide that, hosted public or tenant publication is not
supported and supervisor config validation must fail.

This requirement is what prevents one tenant route from being same-site
with another tenant route merely because both end in `example.com`.

### 1.3 Canonical hostname synthesis

The publication controller does not create hostnames by stacking
multiple labels such as `web.demo-app.acme.apps.example.com`.

Instead, every route gets one synthesized left-most label:

- public: `{route-label}.{public_base_domain}`
- tenant: `{route-label}.{tenant_base_domain}`

Where:

- `route-label = {hostname-label}--{workspace-slug}--{tenant-slug}--{hash8(canonical-tuple)}`
- each input is normalized to lowercase LDH labels
- if the result would exceed 63 octets, the controller truncates the
  human-readable prefix and appends a stable hash suffix
- the canonical tuple hashed into the suffix is
  `(hostname-label, workspace-uid, tenant-uid, visibility)`
- `hash8(...)` means the first 8 lowercase base32hex characters of
  `SHA-256(canonical-tuple-bytes)`
- `route-label` is reserved and tombstoned as one atomic unit

Examples:

- `web--demo-app--acme--9q2ms4r8.apps.example.com`
- `admin--demo-app--acme--m1n8c4p2.tenant.apps.example.com`

Under a delegated public suffix, each route is a separate browser site.
That sharply reduces sibling-route cookie and same-site leakage.

### 1.4 Cookie scope rules

- platform login cookies must live on a dedicated auth origin outside
  the publication base domains, for example `login.example.com`
- published app responses may only set host-only cookies; any backend
  `Set-Cookie` containing a `Domain` attribute is stripped by the edge
- backend cookies on published routes must include `Secure`
- backend cookies attempting to use the reserved
  `__Host-gc_route` namespace are stripped
- platform route-grant cookies, when used, must be `__Host-` prefixed,
  `Secure`, `HttpOnly`, `Path=/`, and have no `Domain` attribute

This keeps platform auth state off shared publication domains and
prevents one app from planting cookies for sibling routes.

## 2) TLS And Certificate Custody

### 2.1 TLS policy

- external publication is HTTPS-only
- plaintext HTTP exists only to redirect to HTTPS
- TLS 1.2 and TLS 1.3 are allowed; lower versions are rejected
- ALPN is limited to `h2` and `http/1.1`
- unknown SNI is rejected during handshake; there is no default
  certificate route for publication domains
- `Strict-Transport-Security` is always emitted as
  `max-age=31536000; includeSubDomains`
- v0 does not send the `preload` token

### 2.2 Certificate shape

- each base domain has its own certificate lineage and private key
- wildcard coverage such as `*.apps.example.com` is allowed only for
  that base domain and must not be reused for unrelated product domains
- `public_base_domain` and `tenant_base_domain` must use separate
  lineages if they are separate suffixes

### 2.3 Key custody

- private keys are generated on the host by the publication certificate
  manager
- active key material is stored outside tenant-visible filesystems under
  `DefaultHome()/supervisor/tls/<base-domain>/`
- on-disk key files are `0600` and root-owned
- `gc-edge` receives the active keypair through a root-managed
  credential handoff or equivalent memory-only load path; workspace
  processes and tenant-owned services never read publication private keys
- only the certificate manager and `gc-edge` may access active key
  material

### 2.4 Renewal and rotation

- renewal starts no later than 30 days before expiry
- the certificate manager retries failed renewals with backoff and emits
  operator events on first failure, repeated failure, and recovery
- `gc-edge` keeps old and new certificate bundles in memory during
  rotation and swaps the SNI map atomically for new handshakes
- once the new bundle is active, the old bundle is removed from memory
  after in-flight handshakes complete

### 2.5 Failure and compromise

- if certificate issuance has not completed yet, affected routes remain
  `reason = cert_pending` and do not become `ready`
- if renewal fails and no still-valid certificate exists, handshake
  fails closed and affected routes surface `reason = cert_failed`
- there is no HTTP downgrade or plaintext backend fallback
- operator compromise response is: mint a new lineage, deploy it,
  verify the new lineage is active on all edge workers, invalidate any
  route-grant cookies, then revoke the old lineage
- emergency key rotation must force a fresh edge-generation epoch so
  stale workers cannot continue serving the old bundle silently

## 3) Request Parsing And Anti-Smuggling Contract

### 3.0 Client IP derivation

Client IP for rate limits, abuse quarantine, and audit is derived from
exactly one of these sources:

- the direct remote socket address when `gc-edge` is internet-facing
- a PROXY protocol v2 envelope from an explicitly allowlisted
  operator-managed L4 frontend

`X-Forwarded-For` and other application-layer proxy headers are never
trusted for client identity in hosted v0.

### 3.1 Ingress parser

The edge is the sole HTTP parser for published routes. The relay does
not forward opaque client bytes to the backend. The edge must fully
parse the request line and headers before route lookup, auth, or relay
dial.

Unsupported at ingress:

- `CONNECT`
- `TRACE`
- `h2c`
- HTTP/0.9
- absolute-form and authority-form request targets
- duplicate `Host`
- multiple `Content-Length`
- conflicting `Content-Length` and `Transfer-Encoding`
- any `Transfer-Encoding` other than exactly `chunked`
- obsolete line folding
- header names containing underscores or invalid bytes
- total request headers above 16 KiB

### 3.2 Canonical relay serialization

After validation, the edge reserializes the request for the relay hop in
a canonical form:

- one request per relay connection
- origin-form request target only
- `Host` set to the published route FQDN
- hop-by-hop headers removed
- trusted forwarding headers rewritten from edge-owned state
- exactly one framing mechanism: either one `Content-Length` or
  `Transfer-Encoding: chunked`, never both
- no request bytes are sent to the relay until route authorization and
  auth checks have passed

No client header ordering, spacing, or transfer-coding ambiguity is
preserved across the relay hop. That is the key anti-smuggling
guarantee.

### 3.3 Trusted header contract

The edge must strip inbound instances of and then overwrite these
headers with edge-owned values when forwarding:

- `X-GC-Route-ID`
- `X-GC-Probe`
- `X-GC-Request-ID`
- `X-Forwarded-For`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Forwarded-Port`
- `Forwarded`
- `X-Real-IP`
- `Traceparent`
- `Tracestate`
- `X-Request-ID`
- `X-Correlation-ID`
- `Connection`
- `Proxy-Connection`
- `Keep-Alive`
- `TE`
- `Trailer`
- `Transfer-Encoding`
- `Upgrade`
- `Proxy-Authenticate`
- `Proxy-Authorization`
- `Via`

Applications must treat all `X-GC-*`, forwarding, and trace headers as
observability metadata, not authorization signals.

### 3.4 Response security headers

The edge always owns these response headers for published routes:

- `Strict-Transport-Security`
- `X-Content-Type-Options: nosniff`
- `Server`

`Server` is overwritten with a platform value. v0 does not force
`Content-Security-Policy`, `X-Frame-Options`, or `Cross-Origin-Opener-Policy`
on tenant responses because those change application behavior; those
remain application-owned unless a future profile opts into stricter
defaults.

## 4) Auth Contract And Revocation

### 4.1 Route-gating model

`visibility = "tenant"` is enforced entirely at the edge. The
application does not receive a trusted end-user identity assertion in
v0. The platform auth flow is:

1. unauthenticated request hits a tenant route
2. edge redirects to the operator auth origin
3. auth origin establishes the platform session on its own host
4. edge receives an auth callback or token exchange result and grants the
   specific route
5. edge forwards traffic only after an allow decision exists

The auth origin is configured by operator policy, not by tenant input.
Hosted v0 redirects must use a fixed allowlisted auth origin, `303 See
Other`, and an edge-generated `state` nonce bound to the target route.

### 4.2 Route grant representation

When the platform uses cookies for route grants, the cookie must be
route-local:

- name prefix `__Host-gc_route`
- bound to exactly one published route host
- `Secure`, `HttpOnly`, `Path=/`, no `Domain`
- opaque value carrying only a route-grant handle, not raw user identity

The edge consumes the route grant and does not forward it upstream.

### 4.3 Auth backend response

The auth adapter must return a normalized decision with:

- route handle
- tenant UID
- decision: allow or deny
- credential fingerprint or grant handle hash
- session ID hash
- revocation epoch
- hard expiry time
- cache TTL hint within platform maximums

### 4.4 Cache and revocation semantics

- positive auth cache entries may live for at most 5 seconds and never
  past the decision hard-expiry
- negative entries may live for at most 5 seconds
- revocation events must fan out to all edge workers within 5 seconds
- if an edge worker loses revocation-stream freshness for more than
  5 seconds, it must stop honoring positive cache entries and fail
  closed for new tenant-route requests
- auth backend unavailability fails closed for new requests and surfaces
  `reason = auth_unavailable`
- upgraded WebSocket connections are indexed by `session_id_hash` and
  route handle; a revocation event for that session must close matching
  upgraded connections within 5 seconds
- route quarantine or suspension closes upgraded connections even
  without a session-specific revocation event

This yields an explicit stale-allow bound: end-to-end revoked access may
persist for at most 10 seconds in the healthy case (5 seconds fan-out
plus 5 seconds cached allow), and workers fail closed within 5 seconds
of revocation-stream loss. Cache TTL enforcement must use monotonic
time, not wall-clock time.

## 5) Edge-To-Relay Authorization

### 5.1 Relay placement

- relays live outside the workspace mount namespace at
  `/run/gc-publish/<workspace-uid>/<service>.sock`
- relay directories are `0710`
- relay sockets are `0660`
- workspace processes never see or own these paths

### 5.2 Caller identity

The relay admits connections only if all of these checks pass:

1. `SO_PEERCRED` reports an expected UID
2. the peer PID belongs to the expected supervisor-managed unit or
   cgroup for `gc-edge` or `gc-prober`
3. the first frame carries a valid route capability for this relay

UID alone is not sufficient.

Hosted v0 runs `gc-edge` and `gc-prober` as distinct service accounts.
Relay admission therefore uses an explicit mapping:

- `audience = edge` requires peer UID `gc-edge`
- `audience = prober` requires peer UID `gc-prober`

### 5.3 Route capability

The publication controller mints a short-lived signed capability for the
relay hop. It contains:

- route handle
- workspace UID
- service name
- generation
- route epoch
- audience: `edge` or `prober`
- peer PID
- issued-at
- expires-at
- nonce
- key ID
- signature by the supervisor-held relay-signing key

Rules:

- lifetime is at most 60 seconds
- the relay maintains a per-route replay cache for `(key-id, peer-pid,
  nonce)` over the capability lifetime and rejects reuse
- the relay verifies signature, audience, expiry, generation, and route
  epoch before it reads the forwarded HTTP request
- the relay requires exact `route_epoch == current`
- if the relay sees an old epoch, it rejects the connection and the edge
  retries after refreshing route state
- `gc-prober` capabilities do not forward arbitrary HTTP bytes; the
  relay synthesizes a fixed `GET` to the configured health path itself
- capabilities rotate on generation change, suspend, delete, signer
  rotation, explicit repair, and edge-worker resubscribe

`peer-pid` is a second factor alongside live `SO_PEERCRED` and cgroup
checks. PID reuse is therefore not a standalone authorization bypass,
but it remains an acknowledged implementation edge case within the
60-second capability lifetime.

This is the route-bound identity proof for the edge-to-relay hop.

### 5.4 Relay-signing key custody

- the route-capability signing key is an Ed25519 keypair generated by
  the machine supervisor
- the private key is stored root-owned at
  `DefaultHome()/supervisor/publication-signing/active.key` with mode
  `0600`
- only the publication controller reads the private key; relays receive
  public verification keys only
- key rotation uses an overlap set of `{active, previous}` verification
  keys for at most 10 minutes; new capabilities are minted only by the
  active key. This overlap window applies only to scheduled rotation
- emergency rotation removes the compromised key from the verification
  set immediately, bumps route epoch, and forces capability refresh

### 5.5 Backend targeting

- the relay enters exactly one workspace network namespace
- it dials only `127.0.0.1:<declared-port>` in that namespace
- active target selection is by immutable workspace UID plus namespace
  identity, not by workspace name
- the design does not prove which process listens on that loopback port;
  it proves only that the traffic goes to the declared workspace
  namespace and declared service port
- this is an accepted v0 limitation and must be surfaced in operator
  documentation and doctor output as "workspace port ownership is not
  cryptographically verified"

## 6) Response Handling And Browser-Facing Policy

### 6.1 Response normalization

The edge parses backend responses before returning them to the client.
If the backend emits an invalid or ambiguous response, the edge returns
`502 Bad Gateway` and records a structured event.

Before forwarding, the edge strips or rewrites:

- `Connection`
- `Transfer-Encoding`
- `Upgrade`
- `Alt-Svc`
- `Trailer`
- `Proxy-*`
- `Via`
- `Server`
- `X-Powered-By`

Shared caching is disabled in v0. Standby pages, auth errors, and other
platform-generated responses are emitted with `Cache-Control: no-store`.

### 6.2 Cookie policy

- backend cookies must be host-only and `Secure`
- backend cookies with `Domain` are stripped
- backend cookies using the reserved `__Host-gc_route` name space are
  stripped
- standby handlers and auth-error handlers never forward backend cookies
- platform auth cookies do not live on publication domains

### 6.3 WebSocket policy

- WebSocket upgrade is allowed only when the service declaration opts in
- all route lookup, auth, rate-limit, and relay-capability checks finish
  before `101 Switching Protocols`
- once the upgrade completes, the connection becomes a byte stream
- the edge keeps an in-memory index from upgraded connection ID to route
  handle and `session_id_hash` when tenant auth is in use
- revocation for a session closes matching upgraded connections within
  5 seconds
- suspend, route quarantine, tenant emergency freeze, or drain closes
  upgraded connections with the same control-plane decisions used for
  HTTP routes

## 7) Correlation, Logging, And Audit

The edge generates a fresh correlation identifier per request before
forwarding. It must:

- strip inbound `Traceparent`, `Tracestate`, `X-Request-ID`,
  `X-Correlation-ID`, and `X-GC-Request-ID`
- generate a new W3C `traceparent`
- generate a matching `X-GC-Request-ID`
- write that identifier into edge logs, auth decisions, relay logs,
  health-probe logs, and audit events

The application may log these headers, but they are not trusted identity
or authorization inputs.

Audit records for route create, delete, suspend, quarantine, auth
backend failure, and certificate rotation must include:

- tenant UID
- workspace UID
- service name
- route FQDN
- route handle
- actor or subsystem
- reason
- correlation ID

## 8) Fail-Closed Matrix

| Condition | Required behavior |
|---|---|
| auth backend unavailable | deny new tenant-route requests, surface `auth_unavailable` |
| revocation stream stale > 5s | stop honoring positive auth cache, deny new tenant-route requests |
| relay capability invalid or expired | reject relay connection before request bytes, emit route event |
| certificate pending | route never reaches `ready` |
| certificate expired with no valid replacement | TLS handshake fails closed |
| route generation mismatch | relay rejects connection, reconciler refreshes capability |
| route suspended or quarantined | new requests blocked, upgraded connections drained and closed |

## 9) Implementation Constraints For Hosted V0

Hosted publication may not ship unless all of the following are true:

- publication base domains are delegated public suffixes
- route FQDN synthesis uses the single-label scheme in this document
- the edge reserializes requests and responses instead of tunneling raw
  bytes
- relay admission uses route capabilities, not only socket permissions
- platform auth cookies live on a dedicated auth origin outside the
  publication suffixes
- certificate custody and rotation follow the host-owned model above

Kubernetes and multi-node hosted backends must produce companion
security designs that satisfy equivalent controls before they can reuse
the `[[service]]` publication API.
