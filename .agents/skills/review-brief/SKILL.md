---
name: review-brief
description: Prepare a concise, read-only review brief for local changes, a branch, a PR, or fixes to prior findings. Use to shorten human code-review turnaround, not to approve or post a review.
---

# Review brief

Help a human reviewer reach a useful judgment quickly. Report evidence, not an approval verdict. Keep findings local.

## Scope and reproducibility

- **Local changes:** Review staged and unstaged changes and relevant untracked source files without staging them. Record the current HEAD and distinguish the three groups.
- **Branch:** Use an explicit base, PR metadata, or a clearly established upstream base. Review the merge-base-to-HEAD diff; report uncommitted work separately. State the base assumption if uncertain.
- **PR:** Record the PR base and head SHAs and review that revision. Do not silently substitute the current checkout for an inaccessible PR.
- **After fixes:** Compare prior findings with the new head. Mark each resolved, still present, or unverified; inspect fixes for regressions.

If access or context is missing, state the limit and continue with reliable evidence. Do not ask questions during the review.

## Focused review

1. Start with the diff. Read applicable `AGENTS.md` instructions, including nested guidance. For a branch or PR, check base-ref guidance when the local checkout lacks it. Apply existing repository rules without copying them into the brief.
2. Read relevant sections of an implementation plan when linked or clearly associated with the change. Use settled decisions to understand intended behavior while independently checking correctness. Mention a contradiction, open decision, or stale plan only when it affects the reviewed change. Do not search broadly for a plan.
3. Inspect the nearest tests for changed behavior. Expand into callers, configuration, interfaces, or generated artifacts only to investigate a concrete risk. Stop an investigation once evidence is sufficient to confirm or dismiss a useful reviewer-attention item.
4. For a PR, inspect available CI results and CodeRabbit feedback. Record the revision each covers when available and check whether material findings still apply at the reviewed head. Briefly reference existing unresolved findings instead of duplicating them; add new evidence when useful. Distinguish passed, failed, pending, stale, and unavailable checks. A passed check proves only that check passed.
5. Use relevant ai-helpers review guidance if already available, but do not spend time locating it or assume its commands, agents, or project profiles work in the current tool.

Use one focused pass by default. Escalate only when you can name the unresolved risk, the evidence needed, and how the answer could change a reviewer-attention item. Give any specialist agent a bounded task and shared review scope. Run a focused check only when it can resolve material uncertainty; do not run broad test suites by default.

## Output

Keep the brief under 300 words. State the repository and reviewed refs, relevant plan revision if available, and what changed. Give at most three reviewer-attention items, labeled **confirmed issue**, **unverified concern**, or **suggestion**. For each, include the trigger, file and line, evidence, and practical fix or test. Summarize CI and CodeRabbit status when available. If no actionable findings are supported, say so.

Do not edit, format, generate, stage, commit, push, switch branches, post comments, create pending reviews, or approve a PR. Do not run a check that changes the working tree.
