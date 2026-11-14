---
name: pr-review
description: >
  Review GitHub Pull Requests for correctness, regressions,
  architectural consistency, unnecessary complexity, and
  test coverage. Use when asked to review or analyze a PR.
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
