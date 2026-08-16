/**
 * Features that are built and working but not surfaced yet.
 *
 * A park is not a deletion and not a feature flag. Nothing here is configurable,
 * nothing reads an environment variable, and no state persists: each is a single
 * constant, and flipping it puts the feature back exactly as it was. The code,
 * its routes, its endpoints and its tests all stay live in the meantime — which
 * is what makes this different from commenting a component out, where the thing
 * rots quietly until somebody tries to bring it back.
 *
 * `EDITOR_NAV` in nav.ts is the same idea and predates this file; it lives there
 * because a nav group is naturally a nav concern.
 */

/**
 * The subscription-usage gauge in the sidebar footer.
 *
 * Parked, not removed: `UsageGauge`, `useUsage`, `/v1/usage`, the refresh
 * endpoint and `internal/agentusage` all still work, and `sandbox-cli usage`
 * on the host is untouched — this hides one panel in one sidebar.
 *
 * A constant rather than the existing `usageHidden` preference, and the
 * difference matters: that preference is **persisted per browser**, so changing
 * its default would leave the panel showing for everyone who has already used
 * Studio, which is everyone who would notice. This is read before the store, so
 * it decides regardless of what is in localStorage.
 *
 * To bring it back: set this to `false`. The Settings toggle reappears with it
 * and whatever each browser had chosen still applies, because that preference
 * was never touched.
 */
export const USAGE_PANEL_PARKED = true;
