// Navigation manifest — the single source of truth for what appears in the nav.
//
// The routes used to be declared twice: NAV_ITEMS in Topbar.tsx and
// NAV_SECTIONS in LandingPage.tsx. With more pages coming, two lists that must
// be kept in step by hand is the wrong shape, so both moved here.
//
// `group` exists for the mobile sheet: a flat list of three routes reads fine,
// a flat list of a dozen does not. Items without a group render in the sheet's
// first, unlabelled block.

export type NavItem = {
  label: string;
  /** Route to push. Mutually exclusive with `sectionId`. */
  href?: string;
  /** In-page scroll target (landing page). Mutually exclusive with `href`. */
  sectionId?: string;
  /** Section heading in the mobile sheet. Ungrouped items come first. */
  group?: string;
  /**
   * Active-state override. Defaults to a `startsWith(href)` test, which is
   * wrong for any route that is a prefix of another — pass this when it is.
   */
  match?: (pathname: string) => boolean;
};

/** Primary routes for the authed application shell. */
export const APP_NAV_ITEMS: readonly NavItem[] = [
  { label: "Workflows", href: "/workflows" },
  { label: "Bazaar", href: "/bazaar" },
  { label: "Usage", href: "/usage" },
  { label: "Credits", href: "/billing" },
];

/** In-page sections for the marketing landing page. */
export const LANDING_NAV_ITEMS: readonly NavItem[] = [
  { label: "Overview", sectionId: "pillars" },
  { label: "How it works", sectionId: "flow" },
  { label: "Waitlist", sectionId: "waitlist" },
];

/** Whether `item` represents the currently open page. */
export function isNavItemActive(item: NavItem, pathname: string): boolean {
  if (item.match) return item.match(pathname);
  if (!item.href) return false;
  return pathname.startsWith(item.href);
}

/**
 * Groups items for the sheet, preserving declaration order and keeping
 * ungrouped items in a leading block with an empty heading.
 */
export function groupNavItems(
  items: readonly NavItem[],
): { group: string; items: NavItem[] }[] {
  const out: { group: string; items: NavItem[] }[] = [];
  for (const item of items) {
    const group = item.group ?? "";
    const last = out[out.length - 1];
    if (last && last.group === group) last.items.push(item);
    else out.push({ group, items: [item] });
  }
  return out;
}
