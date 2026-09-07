"use client";
import { useEffect } from "react";

/**
 * Freezes page scroll while `locked` is true.
 *
 * Two details the naive version gets wrong:
 *
 * 1. It restores the *previous* inline overflow rather than clearing it, so a
 *    page that deliberately sets its own overflow is not trampled by the menu
 *    closing.
 * 2. It pins the scroll position — but only when the target is the document.
 *    Setting `overflow: hidden` on the document discards the scroll offset in
 *    some engines, so the page jumps to the top when the lock lifts; capturing
 *    the offset and restoring it afterwards keeps the reader where they were.
 *    An element going `overflow-y: auto` -> `hidden` keeps its `scrollTop`,
 *    so the container path has nothing to put back and must not write one: see
 *    the cleanup for what that write would break.
 *
 * By default it locks both <html> and <body> — locking only one leaves iOS
 * Safari able to scroll the other. Pass `container` for a surface that scrolls
 * an element instead of the document: the landing page is `height: 100dvh;
 * overflow: hidden` with an inner `overflow-y: auto` div, so a document-level
 * lock there does nothing at all.
 *
 * Hiding the scrollbar also hands its width back to the layout, which shifts
 * everything still visible (the bar above the sheet) sideways. The gutter is
 * measured and re-added as padding so nothing moves.
 */
export function useScrollLock(
  locked: boolean,
  container?: React.RefObject<HTMLElement | null>,
) {
  useEffect(() => {
    if (!locked) return;

    const el = container?.current;
    const targets = el ? [el] : [document.documentElement, document.body];
    // Capture overflowX/overflowY, not the `overflow` shorthand: a target that
    // set them as separate inline props (rather than via the shorthand) may
    // not serialize `.style.overflow` back as a two-value string, in which
    // case reading only the shorthand comes back "" and the restore below
    // would clear the original axes instead of putting them back.
    const prev = targets.map((t) => ({
      overflowX: t.style.overflowX,
      overflowY: t.style.overflowY,
      paddingRight: t.style.paddingRight,
    }));
    // Only the document path uses this — see the cleanup.
    const scrollY = window.scrollY;

    // Width of the scrollbar that is about to disappear. Zero on overlay-
    // scrollbar platforms, ~8px here (see the ::-webkit-scrollbar rule).
    const gutter = el
      ? el.offsetWidth - el.clientWidth
      : window.innerWidth - document.documentElement.clientWidth;

    targets.forEach((t, i) => {
      t.style.overflowX = "hidden";
      t.style.overflowY = "hidden";
      // Compensate on the element that owned the scrollbar — the last target,
      // since <body> is what paints inside <html>'s gutter.
      if (gutter > 0 && i === targets.length - 1) {
        const base = parseFloat(getComputedStyle(t).paddingRight) || 0;
        t.style.paddingRight = `${base + gutter}px`;
      }
    });

    return () => {
      targets.forEach((t, i) => {
        t.style.overflowX = prev[i].overflowX;
        t.style.overflowY = prev[i].overflowY;
        t.style.paddingRight = prev[i].paddingRight;
      });
      // Deliberately not restoring `el.scrollTop`. An element keeps its offset
      // through the lock, so the write would be a no-op in the common case and
      // actively wrong in one that matters: a sheet link closes the sheet and
      // starts a smooth `scrollTo` in the same tick, and this cleanup runs
      // after it. An imperative scrollTop assignment cancels a smooth scroll,
      // so restoring here would strand the reader where they opened the sheet.
      if (!el) window.scrollTo(0, scrollY);
    };
  }, [locked, container]);
}
