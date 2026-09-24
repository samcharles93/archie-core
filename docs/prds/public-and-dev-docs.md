# Public and development documentation -- design

**Status:** Draft
**Date:** 2026-09-22

## Problem

One published set carries both kinds of document. A reader who wants to know
what Archie is and how to point an event at it lands in a sidebar that also
holds the repository organisation rules, the migration decision register, the
package review procedure and five PRDs. The PRDs are there for a reason that
has nothing to do with the reader: settled architecture cites them, and the
generator refuses a link whose target it holds back, so publishing them was the
only way to keep those citations alive.

Mixing them is not an exposure problem. Every one of these documents can be
public. It is a navigation problem: a customer reading the site meets
contributor material with no signal that it is not for them.

## Decision

One source tree, two published sets, two artifacts, two URL bases.

| Set         | Holds                    | Served under | Artifact                            |
| ----------- | ------------------------ | ------------ | ----------------------------------- |
| Public      | `index.md`, `guides/`    | `/docs/`     | `docs/data/generated/docs.json`     |
| Development | `architecture/`, `prds/` | `/dev/`      | `docs/data/generated/dev-docs.json` |

`archive/`, `inspiration/`, `news/` and `github-token.md` stay out of both, for
the reasons the generator already records.

The two artifacts have the same shape. A renderer reads a second file and
serves it under a second base; it needs no new field and no set discriminator,
because the file a page arrived in is the set it belongs to.

## Consequences the design depends on

**Every PRD publishes.** Selective PRD publication existed to keep citations
from settled architecture working. With both cited and citing documents in the
development set, that pressure is gone, and the rule that produced it goes with
it: `prds/` is a development tree, not a list of exceptions.

**Cross-set links resolve.** A link from a public page to an architecture page
rewrites to the development set's absolute path, and the reverse works the same
way. Both sets are generated in one pass over one url map, so a link and its
target cannot disagree about where the target lives. The generator's refusal
still fires, now only for a target genuinely in neither set.

**Assets keep the public base.** `docs/public/` is one file set, referenced
from either set, and serving one file under two paths would invent a second
location for it.

**The sidebar stays derived from the directory layout**, per set. A page added
under a directory appears without a second edit, which is the property the
current generator has and the reason its tree mirrors the tree on disk.

## The public set is thin, and that is the honest state

Two pages: the landing page and the playbook guide. Nothing is lost by the
split, because a reader was never served by the seventeen architecture pages
next to them. The pages a public site needs (installation, configuration
reference, the event sources, the actions a playbook can run) are written into
`guides/` and a `reference/` tree as they are written, and each lands in the
public set with no generator change.

## Verification

- `task docs:artifact` writes both files; `task docs:artifact:check` fails on a
  stale copy of either, without touching the tree.
- A link from the public set to a development page, and a link the other way,
  each resolve to the other set's base path in the generated body.
- A link to a page in neither set fails the generator, naming the file, the
  line and the target.
- `docsgen` accounts for both artifacts, so neither tool reports the other's
  file as unowned.
