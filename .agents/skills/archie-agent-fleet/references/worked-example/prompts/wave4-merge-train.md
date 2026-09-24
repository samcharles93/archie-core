# Wave 4: merge train

Serial. You run this yourself in the captain checkout at
`/work/apps/archie-core`. No subagent owns `main`, and only one `task check`
exists at a time.

Order: `cp/shape` (already merged in Wave 1), then `cp/config`, `cp/live`,
`cp/recovery`, `cp/steps`. Config goes early because it owns the bootstrap path
the others boot through.

## Per branch

```bash
BR=cp/live

git -C /work/apps/archie-core status --short          # must be clean
git merge-base --is-ancestor main $BR && echo "stale: rebase required"
git log --oneline main..$BR
git diff --stat main..$BR
```

A branch that predates a merge computed its deletions against code that has
since changed. On this repo a stale branch's diff deleted 460 lines of
`internal/config` that read as tidy relocation and would have reverted another
lane's decomposition. Rebase it or re-derive the work. Never merge a diff whose
deletions were computed against code that has moved.

Then:

1. `git merge --no-ff $BR` (or apply the lane's handoff patch if the lane ran in
   a managed worktree).
2. `task build --force`, then `task check`, once, alone. Nothing else runs on
   this machine while it does. The force-build is required until
   archie-core-hhgw lands: `task build`'s sources are Go-only
   (Taskfile.yml:325), so a ui/dist-only change does not retrigger it and a
   stale embedded dashboard would ship (Wave 0R audit c10).
3. On failure: one fix pass back to that lane's retained writer with the real
   output pasted in, not a summary. On a second failure, `git reset --hard` the
   merge, requeue the lane, and move to the next branch. Do not stack a third
   patch, and do not fix it yourself unless the fix is one line and obvious.
4. Commit generated assets in the same commit. `ui/dist/` and schema outputs are
   LAW: never regenerated away, never reverted, never left out.
5. `bd close <id>` for each bead the branch delivered. Do not stage `.beads/`,
   and never create a commit whose only content is beads bookkeeping.

## Landing check, every time

```bash
git log --oneline -3
git branch --no-merged main
git log --oneline origin/main..main
```

Done has three states: no commits, committed and unmerged, merged. Only the
third counts. A closed bead is what a worker believed, not what landed.

## Your own edits

A coordinator `sed` rename on this repo once produced `&fakeStore{fakeStore{` in
six places and broke the build. Your edits get the same gate as the children's,
and nobody else reviews them.

## Do not push

Sam pushes manually and does not always announce it. Report what is unpushed as
fact. If `git log origin/main..main` is long, say how long; do not act on it.
