# Archie

Event Driven Agentic Automation.

Archie is an agent platform that reacts to events. A forge issue, a signed
webhook from any app you run, a message in a chat channel: each arrives as an
event, a playbook decides what a matching event sets off, and the work happens
in an isolated git worktree. GitHub and Gitea issues were the first case. A
forge is now one event source among several, and a pull request one outcome
among several.

Nothing about that path is wired to a particular forge, channel, or sender.
Sources, the fields they carry, and the workflows they reach are configuration
you add without touching code or restarting anything.

## Start here

- [First playbook](guides/first-playbook.md) points an external app's webhook
  at archie, maps the fields of a real payload, and binds them to a workflow.
- [Architecture](architecture/index.md) is the authority for settled design:
  the domain boundaries, the workflow engine, the task lifecycle, and the
  invariants that hold the whole thing together.

## What runs where

A deployment is a set of services, each owning one typed contract. The Gateway
serves conversation turns and sessions, the Messaging Service runs the
channels, the State Store owns the database outright, the dashboard is its own
process, and `archied` admits work and drives each task through its workflow.
gRPC carries the synchronous calls between them; NATS carries asynchronous
fan-out. Agents execute in containers that receive a task-scoped credential,
issued per task and revoked when the worktree is handed back.

Configuration lives at `${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`.
Nothing in a working tree is load-bearing at runtime.

## A note on these docs

This site is generated from the `docs/` directory of the
[archie-core repository](https://github.com/samcharles93/archie-core) on every
change to `main`. The repository is the source; this site is a view of it. If
the two ever disagree, the repository is right.
