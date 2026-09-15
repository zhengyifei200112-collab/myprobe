# MyProbe Design System

The MyProbe design system provides framework-neutral visual rules on top of the existing Vue 3 stack. Public, administration, settings, notification, authentication, and sharing surfaces consume these shared foundations.

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

These primitives contain no API, authentication, notification, node, or persistence logic. The public business-facing node composition lives in `public-dashboard/PublicNodeCard.vue`; the administration composition uses the same primitives with page-specific hierarchy and responsive rules in `admin-dashboard/`.

Site, appearance, and background composition lives in `settings-center/`.
Server settings provide the shared default theme and accent, while a visitor's
non-sensitive local theme choice may override that default on their device.
