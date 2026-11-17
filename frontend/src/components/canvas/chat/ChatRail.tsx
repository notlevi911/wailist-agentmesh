"use client";
import { useState, useEffect, useRef } from "react";
import { ChatPane } from "./ChatPane";
import { ChatModeSwitch } from "./ChatModeSwitch";
import { TerminalTab } from "../TerminalTab";
import { IconChat, IconInspect, IconGrid } from "@/components/ui";
import type { ChatSession } from "./useChatSession";

// The studio's right-hand column: one tall panel that toggles between the
// conversation, the selected node's config, and the run's Tendril terminal.
// Width/resize mechanics are owned by CanvasPage; the bottom dock is purely a
// log viewer and holds none of these.

export type RailTab = "chat" | "inspector" | "terminal" | "palette";

interface ChatRailProps {
  session: ChatSession;
  onSend: (text: string) => void;
  busy: boolean;
  onShowLogs?: () => void;
  width?: number | string;
  hidden?: boolean;
  // Build mode edits the graph instead of running the deployed agent.
  // canToggleBuildMode is false until the graph could run -- before that,
  // build mode is forced on and Run is not offered.
  buildMode?: boolean;
  canToggleBuildMode?: boolean;
  onToggleBuildMode?: () => void;
  /** Without a chat trigger, Run has no conversation to hold. */
  hasChatTrigger?: boolean;
  /** Rendered under the INSPECT tab. Always mounted (hidden via CSS) so
   *  in-progress edits survive a tab switch. */
  inspectorNode: React.ReactNode;
  /** Drives the accent-dot badge on the INSPECT pill -- a node is selected
   *  but the user is looking at another tab. */
  hasSelection: boolean;
  /** Non-null only while the run holds a Tendril lease; gates the TERM pill. */
  leaseId: string | null;
  /** The node palette, supplied only when the studio is stacked AND the
   *  client may edit -- a narrow window on a computer. The palette panel is
   *  280px wide and full height, so in a stacked column it would swallow the
   *  canvas; as a pane in here it keeps its drag-and-drop while the graph
   *  stays visible above. Absent on a desktop (the palette has its own
   *  column) and on a handheld (nothing to author with). */
  paletteNode?: React.ReactNode;
  /** Collapses the rail to a strip. Absent in the sheet, which has a grip. */
  onCollapse?: () => void;
  /** Switches the visible pane when it changes. The rail still owns its own
   *  tab state -- this only nudges it, so tapping a pill afterwards still
   *  wins. Used by the compact bottom sheet to open on the node the reader
   *  just selected. */
  forceTab?: RailTab | null;
  /** Rendered above the chat composer when a run cannot start. */
  blockedNode?: React.ReactNode;
}

export function ChatRail({
  session,
  onSend,
  busy,
  onShowLogs,
  width = 320,
  hidden = false,
  buildMode = false,
  canToggleBuildMode = false,
  onToggleBuildMode,
  hasChatTrigger = false,
  inspectorNode,
  hasSelection,
  leaseId,
  forceTab = null,
  paletteNode,
  blockedNode,
  onCollapse,
}: ChatRailProps) {
  const [tab, setTab] = useState<RailTab>("chat");

  // Only on a *change* of forceTab, so the reader can switch away from the
  // inspector while the same node stays selected.
  const lastForced = useRef<RailTab | null>(null);
  useEffect(() => {
    if (forceTab && forceTab !== lastForced.current) setTab(forceTab);
    lastForced.current = forceTab;
  }, [forceTab]);
  // A lease that ends mid-run must not leave the rail stuck on a dead
  // terminal. Derived at render rather than synced through an effect: an
  // effect fires a second render pass and can lose a race with a pill click
  // in the same tick. "inspector" needs no fallback -- inspectorNode is
  // unconditional and renders its own empty state.
  // A tab whose pane is not currently offered falls back to chat rather than
  // showing nothing: the terminal exists only while a lease does, and the
  // palette only while the studio is stacked -- widening the window mid-session
  // takes the palette back to its own column.
  const unavailable =
    (tab === "terminal" && !leaseId) || (tab === "palette" && !paletteNode);
  const effectiveTab: RailTab = unavailable ? "chat" : tab;

  return (
    <div
      data-panel="chat"
      hidden={hidden}
      style={{
        width,
        flexShrink: 0,
        minWidth: 0,
        borderLeft: "1px solid var(--border)",
        background: "var(--bg-elev-1)",
        height: "100%",
        display: hidden ? "none" : "flex",
        flexDirection: "column",
      }}
    >
      <div
        style={{
          padding: "12px 12px",
          borderBottom: "1px solid var(--border)",
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          // At INSPECTOR.min the Build/Run pill wraps to a second row rather
          // than clipping against the tab pills.
          flexWrap: "wrap",
          rowGap: 6,
          columnGap: 4,
        }}
      >
        <div
          style={{ display: "flex", alignItems: "center", gap: "var(--s-2)" }}
        >
          <RailSwitch
            tab={effectiveTab}
            onSelect={setTab}
            hasSelection={hasSelection}
            showPalette={!!paletteNode}
          />
          {/* Terminal stays its own pill rather than becoming a third
              segment: it is a transient machine session that only exists
              while a lease does, not a peer of the two permanent panes. */}
          {leaseId && (
            <TabPill
              label="Term"
              active={effectiveTab === "terminal"}
              onClick={() => setTab("terminal")}
            />
          )}
        </div>
        {/* One right-hand cluster. The row is `space-between`, so a third
            top-level child goes to the MIDDLE, not the right — which is
            where the chevron ended up. The chevron is last so it sits
            nearest the edge it folds the panel towards. */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--s-2)",
            marginLeft: "auto",
          }}
        >
          {onToggleBuildMode && (
            <ChatModeSwitch
              buildMode={!!buildMode}
              onSelect={onToggleBuildMode}
              hasChatTrigger={!!hasChatTrigger}
              canRun={!!canToggleBuildMode}
              busy={busy}
            />
          )}
          {/* Mirrors the palette's chevron; the rail it leaves brings it
              back. See studioLayout.ts. */}
          {onCollapse && (
            <button
              type="button"
              onClick={onCollapse}
              className="rail-collapse"
              title="Collapse the chat panel"
              aria-label="Collapse the chat panel"
            >
              ›
            </button>
          )}
        </div>
      </div>

      <div
        style={{
          flex: 1,
          minHeight: 0,
          // Every level from here down needs minWidth:0: a flex item's default
          // min-width:auto floors it at its content's min-content width, which
          // pushed the chat composer past the rail's right edge near the 260px
          // minimum.
          minWidth: 0,
          display: "flex",
          flexDirection: "column",
        }}
      >
        {/* Chat -- always mounted; ChatPane owns `draft` in local state, so
            unmounting would eat a half-typed message. */}
        <div
          style={{
            display: effectiveTab === "chat" ? "flex" : "none",
            flex: 1,
            minHeight: 0,
            minWidth: 0,
          }}
        >
          <ChatPane
            session={session}
            onSend={onSend}
            busy={busy}
            onShowLogs={onShowLogs}
            blockedNode={blockedNode}
            composerNote={
              !buildMode && !hasChatTrigger
                ? "This workflow starts from the Run button. Add a Chat trigger if you want to talk to it."
                : undefined
            }
          />
        </div>

        {/* Inspector -- always mounted so in-progress field edits survive a
            tab switch. */}
        <div
          style={{
            display: effectiveTab === "inspector" ? "flex" : "none",
            flex: 1,
            minHeight: 0,
            minWidth: 0,
            flexDirection: "column",
          }}
        >
          {inspectorNode}
        </div>

        {/* Palette -- mounted with the sheet so a drag can start the moment
            the pane is shown. */}
        {paletteNode && (
          <div
            style={{
              display: effectiveTab === "palette" ? "flex" : "none",
              flex: 1,
              minHeight: 0,
              minWidth: 0,
              flexDirection: "column",
            }}
          >
            {paletteNode}
          </div>
        )}

        {/* Terminal -- mounted only while selected: it owns a live WebSocket
            + SSH session, unlike the two above which are cheap to keep
            alive. */}
        {effectiveTab === "terminal" && leaseId && (
          <div style={{ flex: 1, minHeight: 0, minWidth: 0 }}>
            <TerminalTab leaseId={leaseId} onClose={() => setTab("chat")} />
          </div>
        )}
      </div>
    </div>
  );
}

/** The rail's chat/inspector toggle: one segmented control with a sliding
 *  thumb. Both labels stay visible so the control says what it switches
 *  between before it is ever clicked, and each half is its own hit target so
 *  clicking the pane you are already on is a no-op instead of flipping you
 *  away from it. */
function RailSwitch({
  tab,
  onSelect,
  hasSelection,
  showPalette,
}: {
  tab: RailTab;
  onSelect: (tab: RailTab) => void;
  hasSelection: boolean;
  showPalette: boolean;
}) {
  // Segment order, which is also the thumb's index space. Build must come
  // first: it is where a stacked-layout edit starts, and putting it after
  // Inspect would shift Chat and Inspect out from under the reader's thumb
  // every time the window crossed the breakpoint.
  const segments: RailTab[] = showPalette
    ? ["palette", "chat", "inspector"]
    : ["chat", "inspector"];
  const index = Math.max(0, segments.indexOf(tab));

  return (
    <div
      className="rail-switch"
      data-segments={segments.length}
      role="tablist"
      aria-label="Rail panel"
    >
      <span
        className="rail-switch__thumb"
        aria-hidden="true"
        style={{
          // The thumb is exactly one segment wide (CSS sizes it from
          // data-segments), so translating by whole multiples of 100% lands
          // it on each in turn.
          transform: `translateX(${index * 100}%)`,
          // Terminal is live, so none of these panes is: fade the thumb
          // rather than parking it on a segment that is not showing.
          opacity: tab === "terminal" ? 0.35 : 1,
        }}
      />
      {showPalette && (
        <button
          type="button"
          role="tab"
          aria-selected={tab === "palette"}
          className="rail-switch__seg"
          onClick={() => onSelect("palette")}
        >
          <IconGrid size={11} />
          Build
        </button>
      )}
      <button
        type="button"
        role="tab"
        aria-selected={tab === "chat"}
        className="rail-switch__seg"
        onClick={() => onSelect("chat")}
      >
        <IconChat size={11} />
        Chat
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={tab === "inspector"}
        className="rail-switch__seg"
        onClick={() => onSelect("inspector")}
      >
        <IconInspect size={11} />
        Inspect
        {hasSelection && tab !== "inspector" && (
          <span
            aria-label="a node is selected"
            style={{
              width: 5,
              height: 5,
              borderRadius: "var(--r-full)",
              background: "var(--accent)",
              flexShrink: 0,
            }}
          />
        )}
      </button>
    </div>
  );
}

function TabPill({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: "var(--s-1)",
        fontFamily: "var(--font-mono)",
        fontSize: "var(--t-0)",
        textTransform: "uppercase",
        letterSpacing: "0.06em",
        padding: "2px 8px",
        borderRadius: "var(--r-full)",
        border: `1px solid ${active ? "var(--accent)" : "var(--border)"}`,
        color: active ? "var(--accent)" : "var(--fg-dim)",
        background: "transparent",
        cursor: "pointer",
      }}
    >
      {label}
    </button>
  );
}
