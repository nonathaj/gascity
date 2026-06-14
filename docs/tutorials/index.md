---
title: Tutorials
description: Hands-on guides for learning Gas City's core concepts.
---

These hands-on guides build step by step toward what Gas City is for:
orchestration. You'll write a formula — a method for how a job gets done — and
watch the controller run it as a graph: decomposing the job into beads, fanning
ready ones out to many agents at once, gating each step on its dependencies, and
driving the whole thing to completion outside your session. The early tutorials
(cities, agents, sessions) get you to a working agent; the later ones (formulas,
orders) hand the wheel to the controller.

For the conceptual model underneath, start with the [six
primitives](/concepts/primitives).

## Tutorials

| Tutorial                     | Description                         |
| ---------------------------- | ----------------------------------- |
| [Cities and Rigs](/tutorials/01-cities-and-rigs) | Creating and managing a workspace   |
| [Agents](/tutorials/02-agents)          | Configuring agent templates         |
| [Sessions](/tutorials/03-sessions)      | Running and interacting with agents |
| [Communication](/tutorials/04-communication) | Agent-to-agent coordination    |
| [Formulas](/tutorials/05-formulas)      | A method for a job; the controller runs it as a graph, fanning ready steps out in parallel |
| [Beads](/tutorials/06-beads)            | The universal work primitive        |
| [Orders](/tutorials/07-orders)          | Scheduled and event-driven dispatch |

Follow them in order — each builds on the last. By the end you'll dispatch a
formula and watch the controller drive the whole graph to completion across your
fleet, on its own.
