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
- Compliance with `.cursor/rules/architecture.md` and applicable scoped rules:
  module ownership, dependency direction, and established contracts/invariants.
- Architectural decisions introduced without explicit maintainer approval.
- Expansion of documented known issues into new code paths.

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

An assessment alone is not a defect; report a finding only when there
is a concrete, actionable issue.

## 5. Report format

Use these five sections in this order for every PR review.
Keep the report concise and evidence-based.

### 1. Verdict

- **Status:** `Changes required` if any P1/P2 findings remain;
  otherwise `No blocking findings`.
- Summarize the outcome in one or two sentences.
- P1/P2 findings are blocking; P3 findings and optional improvements
  are non-blocking.
- Do not imply that unexecuted or unavailable checks have passed.

### 2. Findings

- List actionable findings by severity (P1, then P2, then P3).
- For each finding, include a short title, a direct file/line link,
  the problem, a realistic triggering scenario, its consequence,
  and the smallest recommended fix.
- Separate confirmed defects from unverified risks and optional improvements.
- If none exist, explicitly write `No actionable findings.`

### 3. Architecture compliance

Always include this section, even when no violations are found.

- **Status:** `Compliant`, `Violations found`, or `Not fully verified`.
- Name the relevant boundaries actually checked: module responsibilities,
  dependency direction, component ownership, and architectural contracts
  or invariants from `.cursor/rules/architecture.md` and applicable rules.
- Check whether the PR introduces architectural decisions without the
  explicit maintainer approval required by `AGENTS.md`.
- For violations, link both the changed code and the violated rule;
  explain the impact and also report each actionable violation in Findings.
- For example, new Docker Engine API operations must go through
  `internal/swarm` rather than calling the Docker SDK directly.
- Do not present existing documented known issues as newly introduced
  defects unless the PR introduces or expands the violation.
- If a boundary is ambiguous or could not be checked, state that
  explicitly rather than inventing a rule or claiming compliance.
- When compliant, briefly list the boundaries checked; do not claim
  that unrelated parts of the architecture were verified.

### 4. Solution assessment

- Classify the solution as **Systematic solution**, **Appropriate local fix**,
  **Workaround**, or **Overengineering**.
- Justify the classification using root cause, component ownership,
  and proportional complexity.
- Assess independent solutions separately when one classification
  would be misleading.
- A classification alone is not a finding; only report concrete,
  actionable problems as findings.

### 5. Validation

- State which tests and checks were actually run and their results.
- Identify important missing coverage, edge cases, and regressions
  in integration or end-to-end coverage.
- Explicitly state which checks were not run or could not be run and why.

Do not repeat the PR description, pad empty sections, invent findings,
or duplicate the same issue across sections. Summarize architecture
violations in Architecture compliance and detail them in Findings.

## 6. Review boundaries

- Do not modify code during a review.
- Do not open or update a PR without explicit approval.
- Do not propose unrelated refactoring.
- Do not require new abstractions without demonstrated need.
- Prefer existing project conventions over generic best practices.
- Report tests that were not run or could not be run.

Focus on actionable findings rather than lengthy summaries
of what the PR changes.
