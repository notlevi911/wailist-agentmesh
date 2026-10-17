"use client";
import { Fragment, useEffect, useId, useRef, useState } from "react";
import { Collapsible } from "radix-ui";
import { type NavItem, groupNavItems, isNavItemActive } from "@/lib/nav";
import { useScrollLock } from "@/hooks/useScrollLock";
import { useSheetHistory } from "@/hooks/useCloseOnBack";

interface AppNavProps {
  items: readonly NavItem[];
  /** Brand cluster, pinned to the start of the bar. */
  brand: React.ReactNode;
  /** Trailing controls (account menu, CTA). Stays visible at every width. */
  actions?: React.ReactNode;
  /** Current pathname, for active state. Landing pages can pass "". */
  pathname: string;
  onSelect: (item: NavItem, options?: { replace: boolean }) => void;
  /** Renders the inline links; lets each surface keep its own link styling. */
  renderInlineLink: (args: {
    item: NavItem;
    active: boolean;
    onClick: () => void;
  }) => React.ReactNode;
  /**
   * Visual treatment. `app` is the solid 56px bar on the authed shell;
   * `landing` is the taller transparent bar over the marketing hero. Only the
   * skin differs — layout, semantics and motion are shared.
   */
  variant?: "app" | "landing";
  /**
   * Appended below the links in the sheet. For a CTA that lives in the bar at
   * wide widths and needs somewhere to go once the bar drops it. Rendered
   * outside the <nav>, because a call to action is not wayfinding.
   */
  sheetFooter?: React.ReactNode;
  /**
   * The element that actually scrolls, when it is not the document. The landing
   * page scrolls an inner `overflow-y: auto` div, so locking <html>/<body>
   * there would silently do nothing.
   */
  scrollContainer?: React.RefObject<HTMLElement | null>;
}

/**
 * The navigation shell for both the marketing and application surfaces.
 *
 * Disclosure semantics keep normal link traversal; menu semantics would
 * promise arrow-key navigation. CSS chooses the visible surface at each width.
 */
export function AppNav({
  items,
  brand,
  actions,
  pathname,
  onSelect,
  renderInlineLink,
  variant = "app",
  sheetFooter,
  scrollContainer,
}: AppNavProps) {
  const [open, setOpen] = useState(false);
  const sheetId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const sheetRef = useRef<HTMLDivElement>(null);

  useScrollLock(open, scrollContainer);
  const { close, consumeHistoryEntry } = useSheetHistory(() => setOpen(false), open);

  // Escape closes and returns focus to the trigger, per the disclosure pattern.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus({ preventScroll: true });
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  // Focus leaving the trigger/sheet closes it. Scoped to those two rather than
  // the whole root: `actions` (e.g. the account menu trigger) renders inside
  // the same root but is not part of this disclosure, so focus moving there
  // must close the sheet rather than being treated as "still inside".
  useEffect(() => {
    if (!open) return;
    const onFocusIn = (e: FocusEvent) => {
      const target = e.target as Node;
      if (triggerRef.current?.contains(target)) return;
      if (sheetRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("focusin", onFocusIn);
    return () => document.removeEventListener("focusin", onFocusIn);
  }, [open]);

  // A hidden desktop trigger cannot release a surviving mobile scroll lock.
  useEffect(() => {
    const mq = window.matchMedia("(max-width: 768px)");
    const closeAboveBreakpoint = () => {
      if (!mq.matches) setOpen(false);
    };
    closeAboveBreakpoint();
    mq.addEventListener("change", closeAboveBreakpoint);
    return () => mq.removeEventListener("change", closeAboveBreakpoint);
  }, []);

  const select = (item: NavItem) => {
    if (item.href && item.href !== pathname) {
      const replace = consumeHistoryEntry();
      setOpen(false);
      onSelect(item, { replace });
      return;
    }
    close();
    if (!item.href) onSelect(item);
  };

  return (
    <Collapsible.Root open={open} onOpenChange={setOpen} asChild>
      <div
        className={`appnav appnav--${variant}${open ? " appnav--open" : ""}`}
        data-open={open || undefined}
      >
        <div className="appnav__shell">
          <div className="appnav__bar">
            {brand}
            <div className="appnav__spacer" />
            <nav className="appnav__inline" aria-label="Primary">
              {/* Keyed here rather than in renderInlineLink: `href` is optional
                    on a NavItem, so a consumer keying by it silently produces
                    undefined keys for the landing page's section links. */}
              {items.map((item) => (
                <Fragment key={item.label}>
                  {renderInlineLink({
                    item,
                    active: isNavItemActive(item, pathname),
                    onClick: () => select(item),
                  })}
                </Fragment>
              ))}
            </nav>
            {actions ? <div className="appnav__actions">{actions}</div> : null}
            <button
              ref={triggerRef}
              type="button"
              className="appnav__trigger"
              aria-expanded={open}
              aria-controls={open ? sheetId : undefined}
              aria-label={open ? "Close menu" : "Menu"}
              onClick={() => setOpen((o) => !o)}
            >
              <BurgerIcon open={open} />
            </button>
          </div>

          <Collapsible.Content asChild>
            <div
              ref={sheetRef}
              id={sheetId}
              className="appnav__sheet"
              aria-hidden={!open}
              inert={!open}
            >
              <nav aria-label="Primary">
                {groupNavItems(items).map(({ group, items: groupItems }) => (
                  <div key={group || "_"} className="appnav__section">
                    {group ? <div className="appnav__group">{group}</div> : null}
                    <ul className="appnav__list">
                      {groupItems.map((item, index) => (
                        <li key={item.label}>
                          <button
                            type="button"
                            className="appnav__link"
                            data-active={isNavItemActive(item, pathname) || undefined}
                            style={{ animationDelay: `${40 + index * 35}ms` }}
                            aria-current={
                              isNavItemActive(item, pathname) ? "page" : undefined
                            }
                            onClick={() => select(item)}
                          >
                            <span>{item.label}</span>
                            <svg
                              className="appnav__chevron"
                              width="16"
                              height="16"
                              viewBox="0 0 16 16"
                              fill="none"
                              aria-hidden="true"
                            >
                              <path
                                d="m6 4 4 4-4 4"
                                stroke="currentColor"
                                strokeWidth="1.2"
                                strokeLinecap="round"
                                strokeLinejoin="round"
                              />
                            </svg>
                          </button>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </nav>
              {sheetFooter ? (
                <div className="appnav__sheet-foot">{sheetFooter}</div>
              ) : null}
            </div>
          </Collapsible.Content>
        </div>
      </div>
    </Collapsible.Root>
  );
}

function BurgerIcon({ open }: { open: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
      <line
        x1="2"
        y1={open ? "4" : "6"}
        x2="16"
        y2={open ? "14" : "6"}
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
      <line
        x1="2"
        y1={open ? "14" : "12"}
        x2="16"
        y2={open ? "4" : "12"}
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}
