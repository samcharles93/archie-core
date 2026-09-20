# PRD writing rules

These rules apply to every file in `docs/prds/`.

- Search existing PRDs, issues, architecture records, and code first. Do not ask
  the maintainer to repeat recorded requirements.
- A newer maintainer decision overrides an older design. Mark the old design as
  superseded instead of preserving or defending it.
- Write plain English. Use a technical term only when the reader needs that
  exact contract or concept.
- Use the shortest direct sentence. Write “When upgraded, existing instances
  are migrated to the database,” not a description of an importer seeding
  absent resources.
- State each fact once. Do not repeat a heading, summarize a list before showing
  it, or restate a decision afterward.
- Every sentence must add a decision, requirement, constraint, evidence, or
  verification step. Delete narration and explanations of obvious text.
- Do not turn a requested outcome into extra product features. Keep inferences
  open until the maintainer or repository evidence settles them.
- Do not create a process for a logical boundary. Add one only when it must
  deploy, scale, start, secure, or fail separately.
- Describe working behavior, not explanatory UI around missing behavior. State
  what users can do and how success is proved.
- Before approval, use independent read-only reviewers to check a cross-cutting
  PRD for missing behavior and exaggerated claims. Verify and synthesize their
  findings; do not copy their prose.
- Before requesting approval, review every sentence for complexity, repetition,
  and unsupported scope.
- Mark a draft as unapproved until the maintainer explicitly approves it.
