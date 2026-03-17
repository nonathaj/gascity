---
title: Design Docs
description: Forward-looking proposals and historical design context for Gas City.
---

Design docs describe how Gas City should work in the future. Current behavior
lives in the [Architecture](../architecture/README) section.

## Status Meanings

- `Draft`: open for discussion
- `Accepted`: approved direction
- `Implemented`: code landed, doc kept for context
- `Obsolete`: superseded or intentionally retired

## Current Design Set

| Document | Status | Notes |
|---|---|---|
| `machine-wide-supervisor-v0` | Accepted | Current supervisor direction |
| `unified-sessions` | Draft | Proposed session model consolidation |
| `session-identity-and-dispatch` | Draft | Companion to unified sessions |
| `session-waits` | Draft | Durable wait subsystem for blocked continuations |
| `async-nudge-delivery` | Draft | Safe queued delivery at idle boundaries |
| `workspace-service-publication` | Draft | Hosted publication model |
| `workspace-publication-edge-security` | Draft | Edge security companion doc |
| `gc-apiserver-v0` | Draft | API server proposal |
| `api-ops-design` | Implemented | State-mutation API surface |
| `agent-pools` | Implemented | Feature shipped before the current template existed |
| `city-layout-audit` | Draft | Repository and runtime layout cleanup |
| `chat-sessions` | Obsolete | Superseded by `unified-sessions` |

## Writing New Design Docs

- Start from [TEMPLATE.md](TEMPLATE).
- Use status tables consistently.
- Update the status when the proposal lands or is superseded.
