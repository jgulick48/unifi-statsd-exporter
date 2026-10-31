# Plan First, Then Delegate

## 1. Plan before acting

For any task beyond a trivial one-line change or a direct question:

1. **Investigate just enough** to understand the scope (read the relevant files, check existing patterns).
2. **Write a numbered plan.** Each step should be small, self-contained, and include:
   - What will change (files, functions, commands)
   - How it will be verified (build, test, lint, manual check)
   - Which model will do it (see the routing table below)
3. **Stop and get agreement.** Present the plan and wait for the user to approve or adjust it before making any changes. Don't start editing files while waiting for approval.
4. **Re-confirm on scope changes.** If a step turns out bigger than planned, or a new step is needed, pause and update the plan with the user before continuing.

Skip the approval step only for trivial edits (typos, single-line fixes) or when the user explicitly says to just do it.

## 2. Delegate to cost-effective models

Once the plan is approved, the main session acts as the **coordinator**. It keeps architectural decisions and final review, and hands self-contained steps to subagents through the Agent tool's `model` parameter.

| Model    | Use for |
|----------|---------|
| `haiku`  | Mechanical, well-specified work: file searches, renames, boilerplate, formatting, adding straightforward tests from a clear spec, running builds/tests and summarizing output, simple doc updates. |
| `sonnet` | Typical implementation work: writing or refactoring a function or package, fixing a bug with a known cause, writing non-trivial tests, moderate code review. |
| `opus`   | Keep for work that needs deep reasoning: architecture and design decisions, ambiguous or cross-cutting bugs, security-sensitive code, concurrency, and the final review of delegated work. Usually done by the coordinator itself, not delegated. |

Routing guidelines:

- **Default to the cheapest model that can reliably do the step.** If unsure between two tiers, pick the cheaper one when the step is easy to verify, and the stronger one when mistakes would be subtle.
- **Don't delegate tiny tasks.** If a step takes only a few tool calls, doing it directly is cheaper than spinning up a subagent that has to rebuild context.
- **Run independent steps in parallel** by sending multiple Agent calls in one message. Run dependent steps in sequence.
- **Escalate on failure.** If a subagent's output is wrong or incomplete, retry once with a clearer prompt, then move the step up one tier.

## 3. Write good delegation prompts

Subagents start with no context, so each prompt must stand on its own:

- The goal of the step and why it matters
- Exact file paths, function names, and relevant existing patterns to follow
- Constraints (Go version, style, no new dependencies, don't touch X)
- The acceptance check (e.g. `go build ./... && go test ./...` passes)
- What to return (a short summary of changes, or specific findings)

## 4. Verify and report

- Review every subagent's result before marking its step done. Read the diff and run the acceptance check yourself.
- After each step, give the user a one-line progress update against the plan.
- At the end, summarize what was done, which model handled each step, and anything skipped or left unresolved.
