# PR review benchmark

```
task bench:prreview
```

Reviews the 38 runnable Martian pull requests once each at `deep` depth with
the production pipeline, judges the posted comments against the golden
comments in `internal/app/prbench/martian.json`, and writes
`docs/benchmarks/pr-review-<timestamp>.md` (recall per PR, overall recall,
precision, models, archie version). Commit that file with the run.

- Models come from the config at `~/.config/archie/config.toml`: the reviewer
  and judge both default to its `default` alias. Override with
  `task bench:prreview -- -model ALIAS_OR_PROVIDER/MODEL -judge-model ALIAS_OR_PROVIDER/MODEL`.
  Pick a judge other than the reviewer's model for an independent judgement.
  Aliases set only in the dashboard are not in the file; pass a
  `provider/model` ref instead.
- Pull requests are public, so no credential is needed. `GH_TOKEN` or
  `GITHUB_TOKEN`, if set, only raises the GitHub API rate limit.
- `-problems ID,ID` runs a partial smoke run; its report says it is not the
  acceptance run. A full run reports PASS when nothing failed and golden
  micro-recall is at least 0.70.
- It is not part of `task test` and posts nothing to any pull request.
