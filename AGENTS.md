## Project Context
- Name of project = Swarm Deploy
- This project implements Continuous Deployment for Docker Swarm

## Projects Rules
- Before modifying code, read `.cursor/rules/architecture.md` in full and identify the modules and supporting boundaries affected by the task. This rule applies to every coding task, regardless of file type.
- Before starting any coding task, inspect `.cursor/rules/*.md` frontmatter and load and follow only the rules applicable to the files being changed.
- Treat frontmatter as policy:
- `apply: always` means rule is always active.
- `apply: by file patterns` + `globs` means apply only to matching files.
- `alwaysApply: true` means apply regardless of globs.
- In the first progress update, briefly state which `.cursor` rules were loaded.
- For Go changes, run `make check` before finishing. For UI changes, run `npm test` and `npm run build` in `ui/`. If a check cannot run, report why.
- If instructed to create a pull request, always assign appropriate repository labels to the PR before finishing the task. Use only the allowed labels from the file `.github/workflows/pr-labels.yaml`

## Pull Request Descriptions

- Every pull request description MUST include `## User-facing changes` and `## Events`, including PRs that only change backend or internal code.
- Under `User-facing changes`, describe changes to observable user behavior: UI/UX, sync and deployment behavior, statuses, diagnostics, errors, notifications, and assistant responses. Describe the effect on users, not just implementation details. Write `None` if there is no user-facing impact.
- Under `Events`, list added, changed, or removed domain, lifecycle, and activity events, including their name/type, emission conditions, payload or meaning, and where users see them when applicable. Include changes to event consumption or presentation that affect behavior. Write `None` if events are unchanged.
- Review the actual diff before claiming `None`. Keep these sections accurate when a PR changes during review.

## Conventional Commits

- Commit messages and PR titles MUST follow Conventional Commits.
- Format: `<type>[(scope)]: <description>`.
- Use lowercase types and concise English imperative descriptions.
- Examples: `feat: add notifications`, `fix: correct tracing keys`, `docs: update architecture`.

## Architecture Decisions

**Agents MUST NOT make architectural decisions without explicit maintainer approval.**

- Preserve existing architecture, component boundaries, and established patterns.
- Respect the responsibilities and dependency directions documented in `.cursor/rules/architecture.md`; reuse existing components, interfaces, and interaction patterns before introducing abstractions.
- Do not introduce cross-module dependencies, transfer responsibilities, or change architectural contracts without explicit maintainer approval.
- Local refactoring and implementation decisions are allowed if they do not change architectural responsibilities.
- If an implementation conflicts with an established boundary, stop the conflicting work, explain the conflict, motivation, alternatives, and trade-offs, and request explicit maintainer approval before proceeding. Do not resolve documented architectural ambiguities by inventing a rule.
- Approval of a task does not imply approval of architectural changes.

**When in doubt, ask before implementing.**

The module inventory, ownership, interactions, and known boundary issues are in
`.cursor/rules/architecture.md`. Existing scoped instructions supplement these rules.

## Assistant Tools

Built-in assistant tools are implemented in Go under
`./internal/modules/assistant/tools/`.

Tool names generally follow `{module}_{entity}_{action}`.
For example, the tool for listing Docker networks is `docker_network_list`.
Consult the existing tool definitions and registration for actual names.

