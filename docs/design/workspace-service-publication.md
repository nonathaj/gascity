# Workspace Service Publication

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-03-14 |
| Author(s) | Codex |
| Issue | N/A |
| Supersedes | N/A |

## Summary

Gas City adds a first-class workspace service publication layer so a
tenant workspace can expose HTTP APIs and web apps at stable internet
URLs without manually wiring reverse proxies, K8s routes, or firewall
rules. A city declares publishable listeners with `[[service]]` in
`city.toml`; the machine supervisor owns everything else: hostname
allocation, TLS, tenant auth, abuse controls, health checks, and route
reconciliation. `gastown-hosted` uses a shared edge plus
per-workspace network-namespace relays under the machine supervisor.
Kubernetes and multi-node hosted publication are not part of the
approved v0 data plane in this document; instead, this design names the
prerequisites companion designs must satisfy before publication can ship
there. Existing cities are unchanged until they opt in. The existing
`[api]` controller listener remains a separate feature and is not part
of tenant app publication.

## Motivation

Today Gas City can bind its own controller API to a TCP listener, but it
cannot safely expose arbitrary tenant workloads from a workspace to the
internet.

Concrete gaps in the current tree:

1. The native Kubernetes runtime creates and manages pods, then execs
   into them, but it does not create `Service`, `Ingress`, `HTTPRoute`,
   or `Gateway` objects. The K8s examples in-tree only expose internal
   `ClusterIP` services for Dolt and MCP mail.

2. The current API bind knobs are about the controller, not the tenant
   workspace. Setting `[api] bind = "0.0.0.0"` exposes the GC API
   server, not an app listening on port 3000 inside a workspace.

3. A hosted multi-tenant platform needs much more than a bound socket:
   DNS, TLS, auth, quotas, revocation, auditability, and hard guarantees
   that workspaces cannot bypass the shared edge.

Simple pain case:

- A tenant launches a web preview on port 3000 in a hosted workspace and
  wants a stable URL. Today GC has no first-class mechanism for that. On
  K8s, the operator must manually build `Service` and route objects
  outside GC. On a hosted machine, the operator must manually manage a
  reverse proxy and hope two tenants do not collide.

Why the naive fix does not work:

- "Just let the workspace bind `0.0.0.0`" is not a platform. It gives GC
  no route ownership, no auth boundary, no TLS guarantee, no abuse
  controls, and no clean way to revoke access.

What VPS providers do:

- At the infrastructure level, a VPS provider either gives the customer a
  public IP and firewall controls, or places a shared edge proxy / load
  balancer in front of workloads. Gas City should follow the shared-edge
  model because hosted workspaces and K8s cells are multi-tenant
  control-plane problems, not "open a random host port" problems.

Design-principle alignment:

- **NDI:** service routes converge from desired declarations to actual
  edge state on every reconcile.
- **SDK self-sufficiency:** publication is a platform capability, not a
  prompt recipe asking an agent to edit proxy config.
- **Bitter Lesson:** better models do not remove the need for DNS, TLS,
  trust boundaries, or route ownership.

## Non-Goals

- Replacing the current controller API transport or security model.
- Retroactively treating the current per-agent K8s pod path as a stable
  workspace runtime.
- Supporting raw TCP publication in v0.
- Supporting custom domains in v0.
- Publishing directly from standalone per-city controllers with no hosted
  control plane or machine supervisor.
- Shipping multi-node hosted publication from this design.
- Finalizing the Kubernetes publication backend in this document. This
  doc names the required K8s prerequisites and integration points, but
  hosted publication is the only approved v0 backend here.
- Preventing arbitrary user-owned egress tunnels from inside a
  workspace. This design governs GC-managed ingress only.
- Inferring exposed ports by scraping process tables or logs.

## Guide-Level Explanation

### Tenant config versus platform config

Tenant workspaces declare *what* they want to publish. The machine
supervisor declares *how* publication is implemented and constrained.

City-level config:

```toml
[workspace]
name = "demo-app"
provider = "codex"

[[service]]
name = "web"
port = 3000
protocol = "http"
visibility = "public"
hostname = "web"
health_path = "/healthz"
allow_websockets = true

[[service]]
name = "admin"
port = 8080
protocol = "http"
visibility = "tenant"
hostname = "admin"
health_path = "/readyz"
```

Machine / hosted operator config:

```toml
[publication]
provider = "hosted"
tenant_slug = "acme"
public_base_domain = "apps.example.com"
tenant_base_domain = "tenant.apps.example.com"

[publication.tenant_auth]
policy_ref = "platform-sso"

[publication.limits]
max_services_per_workspace = 8
max_services_per_tenant = 64
max_reserved_hostnames_per_tenant = 64
tenant_requests_per_second_total = 500
public_requests_per_second = 100
tenant_requests_per_second = 50
per_source_ip_requests_per_second = 20
max_concurrent_requests_per_service = 200
max_body_bytes = 10485760
max_websocket_connections = 100
request_timeout = "30s"
idle_timeout = "5m"
hostname_tombstone_duration = "72h"
rate_limit_burst_factor = 2
abuse_quarantine_threshold = 2000
abuse_quarantine_window = "1m"
abuse_quarantine_duration = "10m"

[publication.hosted]
network_mode = "netns-relay"
edge_class = "shared"
```

Only the hosted machine-supervisor backend is approved in v0. Kubernetes
and multi-node hosted publication are future follow-on work.

### What the user sees

When publication is configured in the machine supervisor, `gc status`
shows published services:

```text
SERVICES
NAME   PORT  VISIBILITY  STATE    REASON             URL
web    3000  public      ready    route_active       https://web--demo-app--acme--9q2ms4r8.apps.example.com
admin  8080  tenant      ready    route_active       https://admin--demo-app--acme--m1n8c4p2.tenant.apps.example.com
```

If the workspace is suspended:

```text
SERVICES
NAME   PORT  VISIBILITY  STATE     REASON              URL
web    3000  public      standby   standby_suspended   https://web--demo-app--acme--9q2ms4r8.apps.example.com
```

The URL stays stable. The edge returns a platform-owned `503 Service
Unavailable` page until the workspace becomes active again.

### This is not `[api] bind`

Publication is a different feature from exposing the GC controller API.

| Feature | Purpose | Config owner | Audience | Example |
|---|---|---|---|---|
| `[api]` bind | expose the GC controller API | city / operator | operators and GC clients | `http://127.0.0.1:9443/v0/status` |
| `[[service]]` publication | expose a tenant app running inside the workspace | tenant declares service, platform owns route policy | tenant end users or public users | `https://web--demo-app--acme--9q2ms4r8.apps.example.com` |

### V0 scope

V0 supports:

- `protocol = "http"`
- `visibility = "public"` or `visibility = "tenant"`
- platform-owned domains only
- platform-owned standby `503`
- WebSocket upgrade only when `allow_websockets = true`
- machine-supervisor ownership only
- hosted backend only

V0 does not support:

- custom domains
- raw TCP
- standalone city-controller publication
- final K8s publication backend

## Reference-Level Explanation

### 1) Model Overview

The architecture is:

```text
city.toml [[service]]                    supervisor.toml / hosted config
          |                                           |
          +---------------- desired spec -------------+
                              |
                              v
                  publication controller (single writer)
                              |
                              +-- hostname registry
                              +-- edge policy + auth + TLS
                              +-- backend target resolver
                              +-- health observer
                              +-- status / events / audit
```

The key rule is:

- **Service publication is workspace-scoped and control-plane-owned.**

It is not agent-scoped. It is not a `bind = "0.0.0.0"` shortcut. It is
not a standalone per-city background job. Publication is owned by the
machine supervisor in v0.

### 2) New Configuration

#### 2.1 City config

`city.toml` gains service declarations only. Platform knobs do not live
in tenant city config.

```go
// Package config
type ServiceConfig struct {
	Name            string `toml:"name"`
	Port            int    `toml:"port"`
	Protocol        string `toml:"protocol,omitempty"`         // default "http"; only supported value in v0
	Visibility      string `toml:"visibility"`                 // required: "tenant" or "public"
	Hostname        string `toml:"hostname,omitempty"`         // default service.name
	HealthPath      string `toml:"health_path,omitempty"`      // default "/healthz"
	AllowWebSockets bool   `toml:"allow_websockets,omitempty"` // default false
}
```

Validation:

- `service.name` must be unique within the workspace and match DNS-label
  rules.
- `port` must be 1-65535.
- `protocol` defaults to `http` and any other value is rejected in v0.
- `visibility` is required. There is no default. Missing visibility is a
  validation error.
- `hostname` defaults to `service.name` if omitted and must be a single
  DNS label if set.
- `health_path` must be an absolute path, must not contain a scheme or
  host, and defaults to `/healthz`.

#### 2.2 Platform config

The machine supervisor owns publication policy.

```go
// Package supervisor
type PublicationConfig struct {
	Provider         string                 `toml:"provider"` // must be "hosted" in v0
	TenantSlug       string                 `toml:"tenant_slug,omitempty"` // required in single-machine mode
	PublicBaseDomain string                 `toml:"public_base_domain,omitempty"`
	TenantBaseDomain string                 `toml:"tenant_base_domain,omitempty"`
	TenantAuth       TenantAuthConfig       `toml:"tenant_auth,omitempty"`
	Limits           PublicationLimits      `toml:"limits,omitempty"`
	Hosted           HostedPublicationConfig `toml:"hosted,omitempty"`
}

type TenantAuthConfig struct {
	PolicyRef string `toml:"policy_ref,omitempty"` // required if any tenant-visible services exist
}

type PublicationLimits struct {
	MaxServicesPerWorkspace       int    `toml:"max_services_per_workspace,omitempty"`        // default 8
	MaxServicesPerTenant          int    `toml:"max_services_per_tenant,omitempty"`           // default 64
	MaxReservedHostnamesPerTenant int    `toml:"max_reserved_hostnames_per_tenant,omitempty"` // default 64
	TenantRequestsPerSecondTotal  int    `toml:"tenant_requests_per_second_total,omitempty"`  // default 500
	PublicRequestsPerSecond       int    `toml:"public_requests_per_second,omitempty"`        // default 100
	TenantRequestsPerSecond       int    `toml:"tenant_requests_per_second,omitempty"`        // default 50
	PerSourceIPRequestsPerSecond  int    `toml:"per_source_ip_requests_per_second,omitempty"` // default 20
	MaxConcurrentRequestsPerSvc   int    `toml:"max_concurrent_requests_per_service,omitempty"` // default 200
	MaxBodyBytes                  int64  `toml:"max_body_bytes,omitempty"`                    // default 10 MiB
	MaxWebSocketConnections       int    `toml:"max_websocket_connections,omitempty"`         // default 100
	RequestTimeout                string `toml:"request_timeout,omitempty"`                   // default 30s
	IdleTimeout                   string `toml:"idle_timeout,omitempty"`                      // default 5m
	HostnameTombstoneDuration     string `toml:"hostname_tombstone_duration,omitempty"`       // default 72h
	RateLimitBurstFactor          int    `toml:"rate_limit_burst_factor,omitempty"`           // default 2
	AbuseQuarantineThreshold      int    `toml:"abuse_quarantine_threshold,omitempty"`        // default 2000
	AbuseQuarantineWindow         string `toml:"abuse_quarantine_window,omitempty"`           // default 1m
	AbuseQuarantineDuration       string `toml:"abuse_quarantine_duration,omitempty"`         // default 10m
}

type HostedPublicationConfig struct {
	NetworkMode string `toml:"network_mode,omitempty"` // v0 must be "netns-relay"
	EdgeClass   string `toml:"edge_class,omitempty"`   // operator-selected shared edge class
}
```

Validation:

- Publication is only active when a machine supervisor is configured.
  Standalone city controllers do not publish internet routes.
- `publication.tenant_slug` is required in single-machine supervisor
  mode until the full tenant model lands.
- `visibility = tenant` requires `publication.tenant_auth.policy_ref`.
- `visibility = public` requires `publication.public_base_domain`.
- `visibility = tenant` requires `publication.tenant_base_domain`.
- `publication.provider` must be `hosted` in v0.
- `publication.hosted.network_mode` must be `netns-relay` in v0.
- `request_timeout`, `idle_timeout`, `hostname_tombstone_duration`,
  `abuse_quarantine_window`, and `abuse_quarantine_duration` use Go
  `time.ParseDuration` syntax.
- If `[[service]]` exists but no machine supervisor publication backend
  is configured, `gc status` must print `SERVICES: publication requires
  a machine supervisor`.

#### 2.3 FQDN derivation

FQDN derivation is normative:

- public: `{hostname-label}--{workspace-name}--{tenant-slug}--{hash8(canonical-tuple)}.{public_base_domain}`
- tenant: `{hostname-label}--{workspace-name}--{tenant-slug}--{hash8(canonical-tuple)}.{tenant_base_domain}`

`hostname-label` defaults to `service.name`. `workspace-name` comes from
the existing `[workspace].name`. `tenant-slug` comes from machine
supervisor config in v0 and becomes hosted-platform identity in a future
multi-tenant design. This prevents cross-tenant hostname squatting and,
with delegated publication suffixes, gives each route its own browser
site. The hash suffix binds the hostname to the canonical tuple
`(hostname-label, workspace-uid, tenant-uid, visibility)` so different
name combinations cannot collide on the same route label. `hash8(...)`
is the first 8 lowercase base32hex characters of
`SHA-256(canonical-tuple-bytes)`. See
`docs/design/workspace-publication-edge-security.md`.

### 3) New Types and Interfaces

```go
package publish

type Visibility string

const (
	VisibilityPublic Visibility = "public"
	VisibilityTenant Visibility = "tenant"
)

type ServiceState string

const (
	ServicePending    ServiceState = "pending"
	ServiceAllocating ServiceState = "allocating"
	ServiceReady      ServiceState = "ready"
	ServiceDegraded   ServiceState = "degraded"
	ServiceStandby    ServiceState = "standby"
	ServiceFailed     ServiceState = "failed"
	ServiceDeleting   ServiceState = "deleting"
)

type ServiceReason string

const (
	ReasonRouteActive         ServiceReason = "route_active"
	ReasonStandbySuspended    ServiceReason = "standby_suspended"
	ReasonBackendUnresolvable ServiceReason = "backend_unresolvable"
	ReasonEdgeApplyFailed     ServiceReason = "edge_apply_failed"
	ReasonGatewayPending      ServiceReason = "gateway_pending"
	ReasonDNSPending          ServiceReason = "dns_pending"
	ReasonCertPending         ServiceReason = "cert_pending"
	ReasonCertFailed          ServiceReason = "cert_failed"
	ReasonProbeFailed         ServiceReason = "probe_failed"
	ReasonAuthUnavailable     ServiceReason = "auth_unavailable"
	ReasonQuotaExceeded       ServiceReason = "quota_exceeded"
	ReasonRouteConflict       ServiceReason = "route_conflict"
	ReasonAbuseQuarantined    ServiceReason = "abuse_quarantined"
	ReasonTenantBudgetLimited ServiceReason = "tenant_budget_limited"
	ReasonRouteRateLimited    ServiceReason = "route_rate_limited"
	ReasonConcurrencyLimited  ServiceReason = "concurrency_limited"
	ReasonRouteQuarantined    ServiceReason = "route_quarantined"
	ReasonTenantUnderAttack   ServiceReason = "tenant_under_attack"
	ReasonUnsupportedBackend  ServiceReason = "unsupported_backend"
)

type ServiceSpec struct {
	WorkspaceUID     string
	TenantUID        string
	Name             string
	Port             int
	Visibility       Visibility
	HostnameLabel    string
	HealthPath       string
	AllowWebSockets  bool
	DesiredFQDN      string
	DesiredSpecHash  string
}

type ServiceStatus struct {
	State              ServiceState
	Reason             ServiceReason
	URL                string
	ObservedGeneration int64
	LastError          string
	HealthSummary      string
	LastTransition     time.Time
}

type HostedTarget struct {
	RelaySocketPath string // supervisor-owned unix socket
	NetworkNS       string
	Port            int
}

type StandbyTarget struct {
	HandlerID string // shared edge-owned 503 handler
}

type BackendTarget struct {
	Hosted  *HostedTarget
	Standby *StandbyTarget
}

type AllocationRecord struct {
	WorkspaceUID       string
	ServiceName        string
	FQDN               string
	Visibility         Visibility
	Generation         int64
	Phase              string
	RouteHandle        string
	RouteEpoch         uint64
	CertRef            string
	BackendFingerprint string
	TombstoneUntil     time.Time
	IdempotencyToken   string
	RelayToken         string
}

type PublicationStore interface {
	Get(workspaceUID, serviceName string) (AllocationRecord, bool, error)
	Put(record AllocationRecord) error
	Delete(workspaceUID, serviceName string) error
	ReserveHostname(fqdn, workspaceUID, serviceName string) error
	TombstoneHostname(fqdn, workspaceUID, serviceName string, until time.Time) error
}

type BackendResolver interface {
	ResolveActiveTarget(ctx context.Context, ws WorkspaceRef, svc ServiceSpec) (BackendTarget, error)
	ResolveStandbyTarget(ctx context.Context, ws WorkspaceRef, svc ServiceSpec) (BackendTarget, error)
}

type RouteReconciler interface {
	EnsureRoute(ctx context.Context, svc ServiceSpec, target BackendTarget, current AllocationRecord) (AllocationRecord, ServiceStatus, error)
	DeleteRoute(ctx context.Context, svc ServiceSpec, current AllocationRecord) error
}
```

### 4) State Machine

```text
             desired service declared
                    |
                    v
                 pending
                    |
                    v
                allocating
              /    |    |    \
             /     |    |     \
            v      v    v      v
         ready  standby failed deleting
           |       ^      ^       |
           |       |      |       |
           v       |      |       v
        degraded --+------+----> absent
```

Rules:

- `standby` means the route stays allocated but points at a platform-owned
  standby handler, not a sleeping backend.
- `failed` is coarse state only. Operators and order must use
  `reason` for actionability.
- `generation` increments when the normalized desired service spec hash
  changes.
- Health observers may update `state` and `reason`, but only if their
  probe generation matches the stored generation.

### 5) Security and Trust Contract

This section is the minimum security contract for v0. The normative
hosted edge-security profile lives in
`docs/design/workspace-publication-edge-security.md`.

#### 5.1 Shared-edge exclusivity

- The edge is the only GC-managed public network entrypoint for
  published services.
- Hosted workspaces do not receive public host ports.
- Hosted product egress restrictions are a separate control plane. This
  document does not claim to stop arbitrary user-owned reverse tunnels
  over outbound egress; it claims that GC publication does not create
  alternate inbound paths.

#### 5.2 Trusted header contract

The edge must unconditionally remove any inbound occurrence of these
headers before forwarding and then overwrite them with edge-derived
values:

- `X-GC-Route-ID`
- `X-GC-Probe`
- `X-Forwarded-For`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Forwarded-Port`
- `Forwarded`
- `X-Real-IP`
- `X-GC-Request-ID`
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

No GC-provided header is a security boundary in v0. `tenant` visibility
is enforced entirely at the edge. Applications must not authorize based
on `X-GC-*`, `X-Forwarded-*`, or `Forwarded` values.

Request normalization rules:

- duplicate `Host` headers are rejected
- SNI / `Host` mismatch is rejected
- absolute-form requests are rejected
- upstream `Host` is always rewritten to the published FQDN for the route
- conflicting `Content-Length` / `Transfer-Encoding` is rejected
- multiple `Content-Length` is rejected
- invalid or underscore header names are rejected
- total request headers are capped at 16 KiB
- when `allow_websockets = false`, `Upgrade` requests are rejected before
  backend dial
- platform-auth cookies or auth headers consumed by the edge are not
  forwarded upstream
- the edge derives client IP from either its own ingress socket or an
  allowlisted PROXY protocol v2 frontend and writes a single normalized
  `X-Forwarded-For`; no application-layer proxy chain is trusted in v0
- the edge-to-relay hop is normalized to one request per connection over
  HTTP/1.1; the relay hop does not use HTTP/2 multiplexing

Response normalization rules:

- the edge strips or rewrites backend-supplied `Connection`,
  `Transfer-Encoding`, `Upgrade`, `Alt-Svc`, `Trailer`, `Proxy-*`,
  `Via`, `Server`, and `X-Powered-By`
- `Strict-Transport-Security`, `X-Content-Type-Options`, `Server`, and
  other platform security headers authored by the edge are not
  overridable by backend responses. See
  `docs/design/workspace-publication-edge-security.md`.
- tenant-route responses, standby responses, and auth-error responses are
  emitted with `Cache-Control: no-store`
- v0 does not enable shared caching for published routes

#### 5.3 Tenant auth

- `tenant` visibility is only valid when a platform auth policy is
  configured.
- The edge must not forward unauthenticated tenant-route requests.
  Hosted v0 uses an allowlisted `303 See Other` redirect to the auth
  origin for interactive login, and `401` or `403` for invalid grants,
  explicit auth failure, or auth-backend outage.
- Auth failure or auth-provider outage must fail closed and surface
  `reason = auth_unavailable` plus corresponding events.
- v0 does not forward a trusted end-user identity assertion to the
  application. Edge auth is route gating only.
- for WebSockets, tenant auth must complete before `101 Switching
  Protocols`; if a route is quarantined or suspended, upgraded
  connections are closed by the edge

#### 5.4 TLS policy

- External publication is HTTPS-only.
- HTTP is redirected to HTTPS.
- plaintext listeners terminate only for redirect and never forward to
  the backend.
- TLS 1.2+ is required.
- ALPN is limited to `h2` and `http/1.1`.
- unknown SNI is rejected at handshake time; there is no catch-all
  default certificate route for published services.
- wildcard certificates are scoped per base domain and never shared
  across unrelated platform domains.
- Certificate issuance or renewal failure prevents `ready` and yields
  `reason = cert_failed` or `reason = cert_pending`.
- HSTS is enabled for platform-owned domains.

#### 5.5 Health probe constraints

- Health probes are `GET` requests to `health_path`.
- Probes do not carry tenant identity headers.
- Probes identify themselves with `X-GC-Probe: 1`.
- Probe timeout is 2s.
- Redirects are not followed.
- Response bodies over 64 KiB are ignored and treated as failure.
- The probe target must be the published backend only, never an external
  URL.

### 6) `gastown-hosted` Backend

V0 hosted publication uses a concrete network primitive:

- every workspace runs in a supervisor-managed network namespace
- every published service gets a supervisor-managed relay socket at
  `/run/gc-publish/<workspace-uid>/<service-name>.sock`
- the relay enters the workspace network namespace and forwards to
  `127.0.0.1:<port>`
- the shared edge dials the relay socket, not an arbitrary host port

Isolation rules:

- default-deny nftables rules on the supervisor-managed workspace bridge
  block east-west traffic between workspaces
- only the shared edge and platform health-probe components may open the
  relay socket
- workspaces cannot dial each other's relay sockets
- suspension detaches the active relay target and swaps the route to the
  shared standby handler
- relay sockets live outside the workspace mount namespace; workspaces do
  not see `/run/gc-publish`
- relay directories are `0710`, owned by `gc-edge:gc-publish`; relay
  sockets are `0660`, owned by `gc-edge:gc-publish`
- `gc-prober` runs under a distinct UID and joins `gc-publish` only for
  socket access; relay audience and peer-credential checks still
  distinguish `gc-edge` from `gc-prober`
- the relay verifies peer credentials with `SO_PEERCRED` and accepts only
  `gc-edge` and `gc-prober` UIDs
- peer credentials are checked on every accepted connection
- the shared edge terminates TLS; relay and app hops are cleartext over
  unix domain sockets / loopback only
- the edge strips any backend `Set-Cookie` carrying a `Domain`
  attribute, strips backend cookies in the reserved `__Host-gc_route`
  namespace, and never forwards standby-handler cookies
- backend `Set-Cookie` is rejected on platform domains unless `Secure`
  is set and the cookie is host-only
- `gc-edge` is part of the platform trusted computing base; compromise of
  that process is equivalent to compromise of the shared edge itself
- `gc-prober` is also part of the platform trusted computing base but may
  only issue probe requests; it never receives tenant traffic, and the
  relay synthesizes the fixed health probe request rather than trusting
  arbitrary HTTP bytes from `gc-prober`
- when a service enters suspend, the edge first drains it: no new
  requests, HTTP keep-alives closed, WebSockets terminated after a 5s
  grace, then route target swapped to standby
- every relay is single-service and maintains an in-memory connection
  registry keyed by connection ID
- once drain begins, the relay rejects new accepts immediately, marks all
  existing connections draining, allows in-flight HTTP responses up to
  30s, and then force-closes remaining sockets
- each edge-to-relay connection begins with a route preface carrying a
  short-lived signed route capability plus the current `route_epoch`;
  the relay rejects stale, mismatched, or expired capabilities before
  forwarding any request bytes
- route capabilities are bound to route handle, workspace UID, service
  name, generation, route epoch, audience, and expiry; they rotate on
  generation change, suspend, delete, signer rotation, or explicit
  repair. See `docs/design/workspace-publication-edge-security.md`.
- planned suspend disables active health probing before drain starts and
  transitions directly to `standby`; it must not bounce through
  `degraded`
- active target resolution is by immutable workspace UID to supervisor
  runtime registry entry, including the network-namespace inode; workspace
  name reuse does not reuse relay identity
- v0 does not prove a particular process owns `127.0.0.1:<port>` inside
  the workspace namespace; publication targets the declared loopback port
  for that workspace and treats any responder there as workspace-owned
  traffic. This accepted limitation must be surfaced in operator docs and
  doctor output.
- while `phase = deleting` or `phase = reserved`, the active-target
  reconciler may not restore the route to the backend

This gives a stable backend target without exposing raw host networking.

### 7) Kubernetes Prerequisites And Follow-On Design

Kubernetes publication is explicitly out of v0 scope here. The items
below are required inputs to a companion design before K8s publication
can be approved:

- stable workspace workload contract owned by GC, separate from the
  current per-agent pod runtime
- valid Kubernetes label keys such as
  `gc.gascity.dev/workspace-id` and `gc.gascity.dev/service-name`
- exact `Gateway` / `HTTPRoute` / `ReferenceGrant` / `allowedRoutes`
  contract against Gateway API v1
- ownerReference and finalizer rules for ordered cleanup
- a concrete standby backend object model
- admission / RBAC model that prevents alternate public exposure through
  unmanaged `Ingress`, `HTTPRoute`, `NodePort`, or `LoadBalancer`
  resources
- exact backend-authentication and identity-propagation story for the
  gateway-to-workload hop

The machine supervisor may validate that a city declares services
incompatible with the current K8s runtime and return
`state = failed, reason = unsupported_backend`, but it does not publish
them.

### 8) Ownership, Persistence, and Recovery

#### 8.1 Authoritative owner

In v0, publication is owned by the machine supervisor. Future multi-node
hosted publication will define its own authority model in a companion
design.

Standalone per-city controllers do not own internet publication. This
avoids dual-writer ambiguity.

#### 8.2 Persistent state

The authoritative publication store is machine-scoped:

- single-machine: `DefaultHome()/supervisor/publications.json`
- v0 has no multi-node publication mode

The store contains both allocation records and hostname reservations. No
second authoritative per-city store exists.

Write rules:

- phase changes are persisted under the supervisor publication lock using
  temp file + fsync + atomic rename
- edge mutation happens outside the lock
- finalize writes re-check generation and idempotency token under the
  lock before committing
- corruption forces read-only degraded mode until repaired; no new routes
  are allocated while the store is unreadable and no tombstones are
  released automatically

City unregistration must call `DrainPublication(city)` before removing
the city from the registry.

#### 8.3 Generation model

- each service identity is `(workspace-uid, service-name)`
- the controller computes a normalized spec hash
- if the spec hash changes, `generation++`
- `ObservedGeneration` on status and health updates must match the stored
  generation or the write is discarded

#### 8.4 Rediscovery contract

Backend resources must carry enough metadata to rebuild live status.

Hosted relay metadata:

- workspace UID
- tenant UID
- service name
- generation
- desired FQDN
- route handle

Durable edge route inventory must also be queryable by route handle and
return FQDN, workspace UID, service name, and generation for status
recovery.

Future K8s publication will define its own rediscovery metadata in its
companion design.

#### 8.5 Recovery matrix

| Scenario | Expected recovery |
|---|---|
| controller restart | reload store, rediscover live hosted relays and edge routes, reconcile to desired state |
| publication-store loss | enter read-only degraded mode; live routes may be listed for status, but hostnames are not reassigned and tombstones are not released until operator repair |
| partial backend drift | leave last known good route in place, reconcile missing pieces, emit drift event |
| workspace suspend | swap active target to standby handler, keep hostname reservation |
| workspace delete | remove edge route, remove backend objects, tombstone hostname for non-reuse window |

#### 8.6 Transaction model

Allocation is two-phase under one publication authority:

1. under lock, persist `phase = reserved`, `idempotency_token`, and
   hostname reservation
2. release lock and create or update edge route
3. reacquire lock and persist `phase = attached`, route handle, and
   generation if the idempotency token still matches

Delete is also phased:

1. under lock, persist `phase = deleting`
2. release lock and remove edge route plus relay state
3. reacquire lock and persist hostname tombstone via
   `TombstoneHostname(...)`

If the controller crashes in phase 1 or 2, recovery examines the stored
phase and the durable edge route inventory:

- `reserved` with no edge route: keep reservation, retry attach, do not
  reassign hostname
- `attached` with matching edge route: restore status
- missing or unreadable store: enter read-only degraded mode and require
  operator repair before reassigning hostnames

Hostname tombstones default to `72h` and count against tenant hostname
limits until expiry.

Every finalize step must compare both `generation` and
`idempotency_token`. Stale workers may not attach, detach, or tombstone a
route if either value no longer matches the authoritative record.

Every edge route mutation also increments `route_epoch`. A suspend or
delete cutover is only committed once edge workers have acknowledged the
new epoch; stale workers attempting relay dials with an old epoch are
rejected by the relay preface check.

### 9) Quotas, Abuse Controls, and Telemetry

These are part of Phase 1, not follow-on work.

#### 9.1 Required limits

Default operator limits:

- max services per workspace: 8
- max services per tenant: 64
- max reserved hostnames per tenant: 64
- aggregate tenant requests per second across all routes: 500
- public requests per second per service: 100
- tenant requests per second per service: 50
- per-source-IP requests per second: 20
- max concurrent requests per service: 200
- max body size: 10 MiB
- max WebSocket connections per service: 100
- request timeout: 30s
- idle timeout: 5m
- rate-limit burst factor: 2x the steady-state rate
- abuse quarantine threshold: 2000 requests within 1 minute after
  rate-limit enforcement
- abuse quarantine duration: 10m
- hostname tombstone duration: 72h

Quota or rate-limit failures surface as `state = failed` or
`state = degraded` with `reason = quota_exceeded` or an edge-enforced
rate-limit event, depending on when the violation occurs.

Rate-limit semantics:

- per-route and per-source-IP limits use token buckets
- burst capacity is `rate_limit_burst_factor * steady_state_rate`
- over-limit requests receive `429 Too Many Requests` and `Retry-After: 1`
- repeated over-limit traffic past the abuse threshold enters temporary
  source-IP quarantine
- quarantine uses normalized client IP after proxy-chain processing; the
  v0 implementation keys by full IP address
- per-tenant aggregate budget is enforced before per-route budget; a hot
  route may consume a tenant's aggregate budget in v0
- aggregate tenant shedding uses `reason = tenant_budget_limited`
- per-route shedding uses `reason = route_rate_limited`
- concurrency shedding uses `reason = concurrency_limited`
- abuse quarantine uses `reason = abuse_quarantined`
- persistent abuse or auth hammering may escalate to route quarantine or
  tenant-wide emergency freeze, producing `reason = route_quarantined` or
  `reason = tenant_under_attack`

Enforcement matrix:

| Limit / control | Enforced at | Failure surface |
|---|---|---|
| max services per workspace | reconciler | validation error / `quota_exceeded` |
| max services per tenant | reconciler | validation error / `quota_exceeded` |
| max reserved hostnames per tenant | reconciler | `quota_exceeded` |
| aggregate tenant RPS | edge | `429`, `tenant_budget_limited` |
| per-service RPS | edge | `429`, `route_rate_limited` |
| per-source-IP RPS | edge | `429`, abuse quarantine event |
| max concurrent requests | edge | `503` or `429`, `concurrency_limited` |
| max WebSocket connections | edge | `429`, `concurrency_limited` |
| request/body size timeout | edge | `4xx`, `5xx`, structured logs |
| route quarantine | edge + supervisor | `route_quarantined` |
| tenant emergency freeze | edge + supervisor | `tenant_under_attack` |

Minimal-viable operator config:

- required: `provider`, `tenant_slug`, `public_base_domain`,
  `tenant_base_domain` when used, `tenant_auth.policy_ref` when used
- optional with safe defaults: all `publication.limits` fields

#### 9.2 Required metrics

Minimum metrics:

- `gc_publication_requests_total{workspace,service,visibility,code_class}`
- `gc_publication_auth_rejects_total{workspace,service}`
- `gc_publication_rate_limited_total{workspace,service}`
- `gc_publication_standby_responses_total{workspace,service}`
- `gc_publication_active_routes`
- `gc_publication_reserved_hostnames`
- `gc_publication_active_websockets`
- `gc_publication_reconcile_duration_seconds`
- `gc_publication_reconcile_queue_depth`
- `gc_publication_probe_latency_seconds`
- `gc_publication_cert_failures_total`
- `gc_publication_cert_issuance_duration_seconds`
- `gc_publication_edge_apply_failures_total`
- `gc_publication_route_program_results_total{reason}`
- `gc_publication_auth_backend_latency_seconds`
- `gc_publication_relay_dial_failures_total`
- `gc_publication_tenant_budget_shed_total`
- `gc_publication_concurrency_shed_total`
- `gc_publication_quarantine_entries_total`
- `gc_publication_quarantine_active`
- `gc_publication_route_quarantine_total`
- `gc_publication_tenant_freeze_total`

Metrics keyed by workspace and service are operator-only and may be
sampled or aggregated before export. High-cardinality forensic detail
belongs in structured logs and audit events, not always-on dashboards.

#### 9.3 Events and audit

New events:

- `service.publication.requested`
- `service.route.ready`
- `service.route.failed`
- `service.health.degraded`
- `service.health.restored`
- `service.auth.failure`
- `service.auth.restored`
- `service.route.deleted`
- `service.quota.exceeded`
- `service.rate_limited`
- `service.tenant_budget.exhausted`
- `service.concurrent_limit.hit`
- `service.quarantine.entered`
- `service.quarantine.cleared`
- `service.route.quarantined`
- `service.tenant.frozen`

Audit records must include tenant UID, workspace UID, service name,
route FQDN, actor, reason, and correlation ID.

### 10) API and UX

Read APIs:

```text
GET /v0/services
GET /v0/service/{name}
```

Response sketch:

```json
{
  "name": "web",
  "port": 3000,
  "visibility": "public",
  "state": "ready",
  "reason": "route_active",
  "url": "https://web--demo-app--acme--9q2ms4r8.apps.example.com",
  "health_summary": "200 /healthz",
  "last_error": "",
  "observed_generation": 4
}
```

Operator UX rules:

- `gc status` shows URL only for `public` and `tenant` services.
- `gc validate` must surface enum choices and expected duration syntax.
- validation errors must name the service and field, for example:
  `city.toml: [[service]] "web": visibility is required`
- `gc service doctor <name>` must show state, reason, FQDN, backend
  target class, gateway or edge handle, and last error
- `gc status` must show `reason = abuse_quarantined` when source-IP
  mitigation is the dominant failure mode
- the machine supervisor must expose an operator-only unquarantine action
  for mistakenly quarantined source IPs

Example:

```text
$ gc service doctor web
Name:            web
Port:            3000
Visibility:      public
State:           standby
Reason:          standby_suspended
URL:             https://web--demo-app--acme--9q2ms4r8.apps.example.com
Generation:      4
Backend:         hosted relay /run/gc-publish/ws_123/web.sock
Route Handle:    edge:shared:web--demo-app--acme--9q2ms4r8.apps.example.com
Last Error:      -
Health:          standby handler active
```

### 11) Backward Compatibility

- Existing cities without `[[service]]` are unchanged.
- Existing manual K8s routes remain valid but unmanaged.
- Existing `[api]` behavior is unchanged.
- Publication is unavailable in standalone city-controller mode.

Deprecation:

- None. This is additive.

## Primitive Test

Not applicable. This proposal adds hosted-platform control-plane
behavior, config, and backend-specific reconciliation. It does not add a
new Gas City primitive or derived mechanism.

## Drawbacks

1. This adds a real edge control plane with state, quotas, and security
   policy. That is operationally heavier than the current runtime-only
   model.

2. Publication becomes dependent on the machine supervisor, not on a
   single city controller running in isolation.

3. Kubernetes and multi-node hosted publication are explicitly deferred
   until companion designs exist.

4. V0 intentionally excludes raw TCP and custom domains, so some users
   will still need manual infrastructure.

## Alternatives

### 1) Expose raw host ports directly

Advantages:

- Minimal platform code.

Rejected because:

- Unsafe for multi-tenant hosted deployments.
- No auth, TLS, quota, or audit boundary.

### 2) Keep all publication config inside `city.toml`

Advantages:

- Fewer config files.

Rejected because:

- Base domains, auth policies, quotas, and gateway refs are platform
  policy, not tenant choice.
- Letting tenants select those values weakens the trust boundary.

### 3) Use K8s-only `Service` plus `Ingress`

Advantages:

- Fastest path for one backend.

Rejected because:

- The user problem is cross-backend.
- `Ingress` equivalence is too weak for v0.
- Hosted backends would still need a second design.

### 4) Use reverse tunnels per workspace

Advantages:

- Easy to prototype.

Rejected because:

- Weak fit for durable, auditable multi-tenant operations.
- Pushes route state into each workspace instead of the control plane.

## Rollout Plan

### Phase 1a: Hosted publication

- land `[[service]]` parsing and validation
- land machine-supervisor publication config and store
- land shared-edge hosted datapath using `netns-relay`
- land quotas, rate limits, status reasons, events, and doctor output
- support `public` and `tenant` HTTP services with standby `503`
- land abuse controls, tombstones, and doctor output
- document safe-repair flow for unreadable `publications.json`

### Phase 1b: K8s publication

- write and approve a companion design for stable workspace workloads on
  K8s
- write and approve a companion design for K8s publication against
  Gateway API v1
- land route-conflict prevention, admission constraints, and exact
  gateway/workload ownership rules

### Phase 1c: Multi-Node Hosted

- write and approve a companion design for multi-node publication
  authority, placement, and per-machine relay hops

### Phase 2

- custom domains
- internal/private service mesh visibility
- on-demand resume on first request
- raw TCP only if still justified after HTTP publication ships
