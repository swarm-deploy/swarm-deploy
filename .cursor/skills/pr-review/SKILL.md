---
name: pr-review
description: >
  Review GitHub Pull Requests for correctness, regressions,
  architecture, solution systematicity, proportional complexity,
  and test coverage. Use when asked to review or analyze a PR.
---

# Pull Request Review

Review changes as a senior Go engineer familiar with this repository.

The primary goal is to identify real defects and regressions,
not to produce as many comments as possible.

## 1. Understand the change

Before reviewing:

1. Read `AGENTS.md` and applicable `.cursor/rules`.
2. Read the PR description and understand its intended behavior.
3. Inspect the complete diff against the target branch.
4. Read surrounding code, callers, consumers, and related tests.
5. Identify existing invariants that the change must preserve.

Do not review the diff in isolation.

## 2. Review priorities

Review in the following order:

### Correctness

- Incorrect behavior and regressions.
- Broken error handling and edge cases.
- Concurrency, transaction, and lifecycle problems.
- Changes to established semantics.
- Compatibility with existing persisted data and API contracts.

### Architecture

- Violations of documented module boundaries.
- Dependencies introduced in the wrong layer.
- Responsibilities assigned to inappropriate components.
- Duplication of existing functionality.

### Complexity

- Unnecessary abstractions, interfaces, or intermediate types.
- Redundant database queries or external API calls.
- Duplicated transformations or validations.
- Solutions significantly more complex than the problem requires.

Suggest simplifications only when they provide a concrete benefit.

### Solution Design: Systematicity & Complexity

Evaluate the quality of the solution, not just its implementation.

#### Systematicity

- Does the change address the root cause rather than work around a symptom?
- Is the behavior implemented in the component that owns it?
- Does it cover all relevant code paths consistently?
- Does it reuse existing invariants, contracts, and mechanisms?
- Does it introduce special cases, duplicated logic, or compensating behavior?
- Would the same underlying problem require another fix elsewhere?

#### Proportional complexity

- Is the solution proportionate to the problem?
- Could the root cause be addressed with fewer components, abstractions,
  dependencies, or state transitions?
- Does it add unnecessary indirection, synchronization, persistence, or configuration?
- Is added complexity justified by concrete requirements rather than
  hypothetical future needs?
- Does a broader solution reduce overall complexity or only relocate and expand it?

#### Classification

Distinguish between:

- **Systematic solution:** addresses the root cause with justified complexity.
- **Appropriate local fix:** solves an isolated problem without architectural debt.
- **Workaround:** masks a broader problem or adds compensating behavior.
- **Overengineering:** adds complexity disproportionate to the problem.

Do not demand broad refactoring when a local fix is sufficient.
When reporting a workaround or overengineering, explain the root problem
and suggest the smallest systematic alternative.
Evaluate total system complexity, not merely changed lines.

### Tests

- Verify that changed behavior is covered.
- Check important edge cases and error paths.
- Identify regressions in existing test coverage.
- When integration tests are removed, verify that equivalent
  end-to-end behavior remains covered.
- Unit tests do not automatically replace integration tests.

## 3. Validate findings

Before reporting an issue:

1. Identify the exact code path.
2. Establish a realistic scenario that triggers the problem.
3. Verify that existing code does not already handle it.
4. Explain the observable consequence.
5. Reference the relevant file and lines.

Do not report speculative problems as confirmed defects.

Do not raise style-only comments unless they affect
maintainability in a concrete way.

## 4. Report findings

Order findings by severity:

- P1: Critical correctness issue or significant regression.
- P2: Meaningful defect, reliability issue, or unnecessary complexity.
- P3: Minor issue with a concrete improvement.

For every finding provide:

- File and line reference.
- Description of the problem.
- Scenario in which it occurs.
- Consequence.
- Suggested direction for fixing it.

Separate confirmed defects from optional improvements.

Always include a short **Solution assessment**: systematic solution,
appropriate local fix, workaround, or overengineering.
Explain the assessment using the root cause, component ownership, and
proportional complexity. If the PR contains independent solutions,
assess them separately when a single classification would be misleading.
An assessment alone is not a defect; report a finding only when there
is a concrete, actionable issue.

If no actionable findings exist, explicitly state that.

## 5. Review boundaries

- Do not modify code during a review.
- Do not open or update a PR without explicit approval.
- Do not propose unrelated refactoring.
- Do not require new abstractions without demonstrated need.
- Prefer existing project conventions over generic best practices.
- Report tests that were not run or could not be run.

Focus on actionable findings rather than lengthy summaries
of what the PR changes.
