# Archie

Event Driven Agentic Automation.

**archied** is a resident daemon. It polls a forge (GitHub, Gitea) for issues
assigned or labelled to it, works each one in an isolated git worktree through a
routed workflow, and opens a pull request for human review.

Work arrives as events. Each task runs in its own sandbox with a task-scoped
credential, and the daemon performs all git operations.

## Start here

- [First playbook](guides/first-playbook.md) walks through getting one piece of
  work from an issue to a pull request.
- [Architecture](architecture/index.md) is the authority for settled design:
  the package map, the workflow engine, the task lifecycle, and the invariants
  that hold the whole thing together.

## What runs where

A deployment is one daemon plus the processes it talks to. The State Store is a
standalone process and owns the database outright; the daemon and gateway never
open it directly. Agents execute in containers that receive a task-scoped
credential, issued per task and revoked when the worktree is handed back.

Configuration lives at `${XDG_CONFIG_HOME:-~/.config}/archie/config.toml`.
Nothing in a working tree is load-bearing at runtime.

## A note on these docs

This site is generated from the `docs/` directory of the
[archie-core repository](https://github.com/samcharles93/archie-core) on every
change to `main`. The repository is the source; this site is a view of it. If
the two ever disagree, the repository is right.
