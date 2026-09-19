#!/usr/bin/env python3
"""reachaudit -- report completion state, not just intent.

Trackers record intention ("build a cron job store"). Nothing records whether
that intention is already written but executing nowhere. This walks the tree,
finds code that no shipped binary can reach, and attributes each finding to its
package with provenance and tracker status, so "is it implemented?" and "is it
reachable?" are separate answerable questions.

Categories, deliberately distinct because their actions differ:

  unreachable-tracked    planned and partially built -- a status, not a bug
  unreachable-untracked  genuinely dead, and invisible to every tracker
  unreachable-dynamic    invisible to static analysis by design (yaegi or
                         reflection), do not delete
  reachable              in a shipped binary

Usage:
  python3 reachaudit.py [--repo PATH] [--json OUT] [--md OUT]

Advisory only. It never proposes deletion; it reports state.
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

# Modules that load code at RUNTIME, so static reachability cannot see them.
# Findings inside these are labelled, never presented as dead.
DYNAMIC_MARKERS = ("yaegi", "go:linkname", "reflect.ValueOf")

# Tracker terms that mean "this subsystem is planned". Matched case-insensitively
# against open issue titles. Coverage is an INPUT to classification, not a
# conclusion -- tracked-and-unreachable is a legitimate status.
TRACKER_HINTS = {
    "cronstore": ["cron"],
    "domain/scheduling": ["cron", "schedul"],
    "servicediscovery": ["service discovery", "servicediscovery"],
    "memory/scrubber": ["scrub", "replay", "transcript"],
    "pairing": ["pairing", "device auth"],
    "domain/sampling": ["sampling", "embedding"],
    "configuration/tomlwrite": ["setup", "toml", "config"],
    "configuration/setup": ["setup", "first-run", "config generation"],
    "terminalprompt": ["setup", "prompt"],
    "domain/image": ["image"],
    "ratelimit": ["rate limit", "ratelimit", "throttl"],
}


def run(cmd: list[str], cwd: Path) -> str:
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True).stdout.strip()


def cmd_closure(repo: Path) -> set[str]:
    """Packages compiled into a shipped binary. A package absent from this set
    cannot execute in production, whatever deadcode says about its functions."""
    out = run(["go", "list", "-deps", "./cmd/..."], repo)
    prefix = "github.com/samcharles93/archie-core/"
    return {l[len(prefix):] for l in out.splitlines() if l.startswith(prefix)}


def deadcode_findings(repo: Path) -> list[str]:
    """Run deadcode, or reuse a cached run. Falls back to an empty list with a
    loud warning rather than silently reporting zero findings."""
    head = run(["git", "rev-parse", "HEAD"], repo) or "unknown"
    dirty = "-dirty" if run(["git", "status", "--porcelain"], repo) else ""
    cache = Path(f"/tmp/reachaudit-deadcode-{head[:12]}{dirty}.txt")
    if cache.exists() and not dirty:
        return cache.read_text().splitlines()
    print("running deadcode (this compiles the module, may take a minute)...", file=sys.stderr)
    out = subprocess.run(
        ["go", "run", "golang.org/x/tools/cmd/deadcode@latest", "./..."],
        cwd=repo, capture_output=True, text=True,
    ).stdout
    if not out.strip():
        print("WARNING: deadcode returned nothing; report will be empty", file=sys.stderr)
    cache.write_text(out)
    return out.splitlines()


def open_tracker_titles(repo: Path) -> list[str]:
    titles = []
    gh = run(["gh", "issue", "list", "--state", "open", "--limit", "400",
              "--json", "title", "--jq", ".[].title"], repo)
    titles += [t for t in gh.splitlines() if t]
    bd = run(["bd", "list", "--status", "open"], repo)
    titles += [t for t in bd.splitlines() if t]
    return titles


def dynamic_packages(repo: Path) -> set[str]:
    """Packages that reference runtime-loaded code."""
    out = run(["grep", "-rl", "-E", "|".join(DYNAMIC_MARKERS),
               "--include=*.go", "internal/", "cmd/"], repo)
    return {f.rsplit("/", 1)[0] if "/" in f else f for f in out.splitlines()}


def tracked_terms(short_pkg: str) -> list[str]:
    for key, terms in TRACKER_HINTS.items():
        if key in short_pkg:
            return terms
    tail = short_pkg.split("/")[-1]
    return [tail]


def provenance(repo: Path, pkg: str) -> dict:
    added = run(["git", "log", "--diff-filter=A", "--format=%ad", "--date=short", "--", pkg], repo)
    first = added.splitlines()[-1] if added else "?"
    last = run(["git", "log", "-1", "--format=%ad|%h|%s", "--date=short", "--", pkg], repo)
    parts = last.split("|") if last else ["?", "?", "?"]
    return {"added": first, "last_date": parts[0], "last_sha": parts[1],
            "last_subject": parts[2][:70] if len(parts) > 2 else ""}


def loc(repo: Path, pkg: str) -> int:
    out = run(["bash", "-c", f"find {pkg} -maxdepth 1 -name '*.go' -exec cat {{}} + 2>/dev/null | wc -l"], repo)
    try:
        return int(out)
    except ValueError:
        return 0


def consumers(repo: Path, pkg: str) -> tuple[int, int]:
    """(non-test importers, test importers) outside the package itself."""
    out = run(["grep", "-rl", f"archie-core/{pkg}\"", "--include=*.go", "."], repo)
    files = [f for f in out.splitlines() if f and f.lstrip("./").rstrip("/") != pkg]
    files = [f for f in files if not f.startswith(f"./{pkg}/")]
    nt = [f for f in files if not f.endswith("_test.go")]
    return len(nt), len(files) - len(nt)


def classify(repo: Path, pkg: str, titles: list[str], dyn: set[str]) -> tuple[str, list[str]]:
    """Shared classification: tracked / untracked / dynamic, plus the tracker
    titles that matched. Used for both whole-package and in-package findings
    so a symbol inside a live package is judged by the same evidence as a
    whole dead one."""
    terms = tracked_terms(pkg)
    hits = [t for t in titles if any(term.lower() in t.lower() for term in terms)]
    is_dynamic = any(pkg.startswith(d) or d.startswith(pkg) for d in dyn)
    if is_dynamic:
        return "dynamic", hits
    if hits:
        return "tracked", hits
    return "untracked", hits


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default=str(Path(__file__).resolve().parents[2]))
    ap.add_argument("--json")
    ap.add_argument("--md")
    ap.add_argument("--refresh", action="store_true", help="re-run deadcode instead of using the cache")
    args = ap.parse_args()

    repo = Path(args.repo)
    if args.refresh:
        Path("/tmp/reachaudit-deadcode.txt").unlink(missing_ok=True)

    closure = cmd_closure(repo)
    if not closure:
        print("ERROR: go list returned no packages; is this a Go module?", file=sys.stderr)
        return 1
    findings = deadcode_findings(repo)
    titles = open_tracker_titles(repo)
    dyn = dynamic_packages(repo)

    by_pkg_syms: dict[str, list[str]] = defaultdict(list)
    for line in findings:
        m = re.match(r"^(.+?):\d+:\d+: unreachable func: (.+)$", line)
        if m:
            by_pkg_syms[str(Path(m.group(1)).parent)].append(m.group(2))
    if not by_pkg_syms:
        print("WARNING: no findings parsed; deadcode output format may have changed", file=sys.stderr)

    # Whole packages absent from the cmd closure: the package cannot execute
    # at all, whatever deadcode says about individual functions inside it.
    rows = []
    for pkg, syms in sorted(by_pkg_syms.items(), key=lambda kv: -len(kv[1])):
        if pkg in closure:
            continue
        category, hits = classify(repo, pkg, titles, dyn)
        nt, tt = consumers(repo, pkg)
        rows.append({
            "package": pkg, "findings": len(syms), "loc": loc(repo, pkg),
            "category": f"unreachable-{category}",
            "non_test_importers": nt, "test_importers": tt,
            "tracker_hits": hits[:3],
            **provenance(repo, pkg),
        })

    # Functions unreachable INSIDE a package that IS in the closure: the
    # package ships, but this symbol is the unwired tail of a feature --
    # the ChatCourier/Dispatch shape, not a dead whole package. This is the
    # half-built-feature case the whole-package report structurally cannot
    # see, since the package itself looks perfectly live.
    in_pkg_rows = []
    for pkg, syms in sorted(by_pkg_syms.items(), key=lambda kv: -len(kv[1])):
        if pkg not in closure:
            continue
        category, hits = classify(repo, pkg, titles, dyn)
        in_pkg_rows.append({
            "package": pkg, "symbols": sorted(syms),
            "category": f"in-package-{category}",
            "tracker_hits": hits[:3],
        })

    order = {"unreachable-untracked": 0, "unreachable-dynamic": 1, "unreachable-tracked": 2}
    rows.sort(key=lambda r: (order.get(r["category"], 9), -r["loc"]))
    in_pkg_order = {"in-package-untracked": 0, "in-package-dynamic": 1, "in-package-tracked": 2}
    in_pkg_rows.sort(key=lambda r: (in_pkg_order.get(r["category"], 9), -len(r["symbols"])))

    total = sum(r["loc"] for r in rows)
    lines = []
    lines.append(f"packages reachable from shipped binaries: {len(closure)}")
    lines.append(f"deadcode findings total: {len(findings)}")
    lines.append(f"unreachable packages: {len(rows)}")
    lines.append(f"unreachable LOC: {total}")
    lines.append("")
    for cat in ("unreachable-untracked", "unreachable-dynamic", "unreachable-tracked"):
        group = [r for r in rows if r["category"] == cat]
        if not group:
            continue
        lines.append(f"== {cat} ({len(group)}) ==")
        for r in group:
            lines.append(f"  {r['loc']:>6} LOC  {r['package']}")
            for hit in r["tracker_hits"] or ["NONE"]:
                lines.append(f"      {hit}")
        lines.append("")
    for cat in ("in-package-untracked", "in-package-dynamic", "in-package-tracked"):
        group = [r for r in in_pkg_rows if r["category"] == cat]
        if not group:
            continue
        lines.append(f"== {cat} ({len(group)}) -- package ships, these symbols do not ==")
        for r in group:
            tr = "; ".join(r["tracker_hits"]) or "NONE"
            lines.append(f"  {r['package']} ({tr})")
            for sym in r["symbols"]:
                lines.append(f"      {sym}")
        lines.append("")
    print("\n".join(lines))

    if args.json:
        Path(args.json).write_text(json.dumps({
            "rows": rows, "in_package_rows": in_pkg_rows,
            "total_loc": total, "closure_size": len(closure),
        }, indent=2))
        print(f"wrote {args.json}")

    if args.md:
        md = ["# reachaudit", "", "```", *lines, "```", ""]
        Path(args.md).write_text("\n".join(md))
        print(f"wrote {args.md}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
