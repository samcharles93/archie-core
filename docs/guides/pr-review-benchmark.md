# PR review benchmark

Run `scripts/pr-review-benchmark.sh` with `PRBENCH_REVIEW_MODEL` and
`PRBENCH_CLASSIFICATION_MODEL` set to configured `provider/model` references.
The judge defaults to `anthropic/claude-sonnet-4.6`; set
`PRBENCH_JUDGE_MODEL` to another configured model when needed. The script uses
`~/.config/archie/config.toml` for providers and `GH_TOKEN` or `GITHUB_TOKEN`
for read access to the benchmark pull requests. It posts no forge review.

The default run reviews the 38 runnable Martian pull requests once each at
`deep`, judges the comments against 102 golden comments, and writes
`bin/prbench-results/run.json`, `summary.json`, and one JSON result per pull
request. That output directory is ignored by git. A completed full run fails
when any problem fails or golden micro-recall is below 0.70. Golden-only
precision is reported alongside recall. Set `PRBENCH_OUT` to keep separate
runs; `-problems ID` limits a smoke run to one pull request. Completed results
are saved as each PR finishes. Pass `-resume DIR` to reuse completed results
from a run with the same models; each reused problem must match the current
benchmark entry, and failed problems are rerun.
A smoke run alone is not the acceptance run.

The benchmark passes only each problem's ID and pull request URL to the
reviewer. The judge receives the golden comments after the review has ended.
The runner uses the production decision phases through the merge gate,
including scoring and the 25-comment cap, then stops before polish and forge
posting. A reviewer that fails to finish makes that problem fail. The results
record budget-skipped phases and the selected models. Model substitutions do
not satisfy the PRD's named-model criterion even if recall clears the threshold.
