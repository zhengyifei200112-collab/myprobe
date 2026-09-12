# MyProbe Design System

Phase 3 establishes framework-neutral visual rules on top of the existing Vue 3 stack. Page migrations remain in their later phases.

## Foundations

- `tokens.css` is the only source of shared color, typography, spacing, radius, elevation, motion, layering, sizing, and breakpoint values.
- `base.css` supplies the global font, focus treatment, disabled state, selection, utility classes, and reduced-motion behavior.
- Theme values are selected with `data-theme="light"`, `data-theme="dark"`, or `data-theme="system"` on the root element. Existing aliases remain temporarily available to avoid changing page behavior during Phase 3.
- Component media queries use `42.5rem` for the compact layout; keep that value aligned with `--breakpoint-compact` until native custom media queries are available in the build target.

## Components

Import components from `design-system/index.ts` using the appropriate relative path:

- Actions: `DsButton`
- Forms: `DsInput`, `DsSelect`, `DsTextarea`
- Surfaces and data: `DsCard`, `DsStat`, `DsBadge`, `DsStatusIndicator`, `DsProgress`
- Overlays: `DsDialog`, `DsSheet`, `DsConfirmDialog`, `DsDropdown`, `DsToast`
- Navigation and feedback: `DsTabs`, `DsLoading`, `DsEmptyState`

All icon-only buttons need an accessible label through the `label` slot or `aria-label`. Dropdown content should expose `role="menuitem"`. Tab items may provide `panelId` when the corresponding tab panel is rendered by the consumer.

## Boundaries

These primitives contain no API, authentication, notification, node, or persistence logic. The public business-facing node composition now lives in `public-dashboard/PublicNodeCard.vue`; any future admin variant remains a separate Phase 5 composition built from the same primitives.
