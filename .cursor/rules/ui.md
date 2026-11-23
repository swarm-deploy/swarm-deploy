---
name: ui
description: Rules for swarm-deploy frontend changes
globs: ["ui/**/*.vue", "ui/**/*.ts", "ui/**/*.css", "ui/styles.css"]
apply: by file patterns
---

# Reuse first

- Inspect `ui/src/components/` before creating a component. Reuse an existing component, or extend it when the extension serves existing use cases.
- Use `AppCard`, `AppPanel`, and `AppStatusBadge` for their established presentation patterns. Keep using `AppTable`, `AppTableEmpty`, and `AppPagination`; do not introduce parallel implementations.
- Add a common component only for clear duplication with at least two meaningful usages. Prefer slots and composition over large prop-driven APIs.
- Never duplicate an existing UI pattern without a clear reason.

# Visual and operational consistency

- Reuse the existing CSS variables, typography, spacing, borders, radii, status colors, table styles, and interaction states. Preserve established responsive behavior.
- This is an infrastructure management UI, not a marketing site. Avoid decorative gradients, unnecessary shadows or animation, oversized spacing, excessively rounded cards, decorative icons, nested cards, and unjustified table-to-card conversions.
- Apply these rules to new and changed UI; they do not require removing established visual elements from unrelated pages.
- Keep structured data compact and scannable. Make resource names, image references, IDs, statuses, and error messages accessible; do not hide diagnostic information for visual simplicity.
- Emphasize resource identity, operational state, errors, navigation, and predictable controls. Do not visually promote secondary information.

# Component responsibilities

- Common components own reusable presentation and interaction behavior.
- Domain components retain business logic, API calls, status interpretation, resource formatting, and domain actions. Map domain states to common presentation variants.
- Pass simple presentation props or slots to common components instead of entire domain models.

# Accessibility and verification

- Preserve semantic HTML, keyboard navigation, visible focus states, and accessible labels. Use buttons and links for actions instead of clickable containers.
- Run `npm test` and `npm run build` from `ui/`.
- When browser tooling is available, inspect affected pages at desktop and narrow widths, including loading, empty, and error states where applicable. Check for overflow and compare with existing patterns. Report when browser verification is unavailable.

# Scope

- Keep changes minimal. Do not redesign unrelated pages, add UI libraries without explicit approval, replace the styling system, or abstract hypothetical requirements.
