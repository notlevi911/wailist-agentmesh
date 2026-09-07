"use client";
import { IconSpeaker, IconStop } from "@/components/ui";
import { toSpeechText } from "./speechText";
import { useSpeechPlayback } from "./useSpeechPlayback";
import type { ChatMessage as Message } from "./useChatSession";
import { MarkdownContent } from "./MarkdownContent";

// One turn in the conversation.
//
// The whole point of this component is the split between the answer and the
// activity strip beneath it: the answer is prose at normal reading size, the
// strip is dimmed 10px mono. A non-technical reader sees a reply; a developer
// sees a link into the logs. Neither has to look at the other's half.

interface ChatMessageProps {
  message: Message;
  /** Reveals the logs pane. Not yet filtered to this turn's run. */
  onShowLogs?: () => void;
}

/** "2 tools · 8.2s · $0.0042" — omitting whatever isn't known. */
function activityParts(m: Message): string[] {
  const parts: string[] = [];
  if (m.toolCount && m.toolCount > 0) {
    parts.push(`${m.toolCount} tool${m.toolCount === 1 ? "" : "s"}`);
  }
  if (typeof m.elapsedS === "number" && m.elapsedS > 0) {
    parts.push(`${m.elapsedS.toFixed(1)}s`);
  }
  if (typeof m.spendUSD === "number" && m.spendUSD > 0) {
    // Sub-cent amounts are normal here, so four decimals rather than two.
    parts.push(`$${m.spendUSD.toFixed(4)}`);
  }
  return parts;
}

export function ChatMessage({ message, onShowLogs }: ChatMessageProps) {
  const isUser = message.sender === "user";
  // Called unconditionally -- Rules of Hooks -- even on the user branch,
  // which never renders the speaker button and so never uses the result.
  const playback = useSpeechPlayback(message.id);

  if (isUser) {
    return (
      <div
        className="chat-msg"
        style={{ display: "flex", justifyContent: "flex-end" }}
      >
        <div
          style={{
            maxWidth: "85%",
            padding: "8px 12px",
            borderRadius: "var(--r-2)",
            background: "var(--bg-elev-3)",
            border: "1px solid var(--border)",
            color: "var(--fg)",
            fontSize: 13,
            lineHeight: 1.55,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {message.text}
        </div>
      </div>
    );
  }

  const parts = activityParts(message);
  const canShowLogs = !!onShowLogs;
  const canSpeak = !message.pending && message.text.trim() !== "";

  return (
    <div
      className="chat-msg"
      style={{ display: "flex", flexDirection: "column" }}
    >
      {message.pending ? (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 7,
            color: "var(--fg-dim)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            padding: "4px 0",
          }}
        >
          <span className="chat-thinking-dot" />
          working…
        </div>
      ) : (
        <div
          style={{
            // ~65ch keeps an agent's long answer readable instead of running
            // the full width of a wide pane.
            maxWidth: "62ch",
            color: message.isError
              ? "var(--danger)"
              : message.interrupted
                ? "var(--fg-muted)"
                : "var(--fg)",
            fontSize: 13,
            lineHeight: 1.6,
            overflowWrap: "anywhere",
          }}
        >
          {/* "interrupted" is not "failed": the run may have succeeded, this
              tab just stopped watching it after a reload. Labelling it as a
              failure would report an outcome we don't actually know. */}
          {(message.isError || message.interrupted) && (
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                textTransform: "uppercase",
                letterSpacing: "0.08em",
                display: "block",
                marginBottom: 4,
                color: message.isError ? "var(--danger)" : "var(--warm)",
              }}
            >
              {message.isError ? "run failed" : "interrupted"}
            </span>
          )}
          <MarkdownContent text={message.text} />
        </div>
      )}

      {/* The activity strip, plus the speaker button when there's something
          to read aloud. This is the bridge between the two audiences:
          everything technical about the turn, compressed to one dim line that
          opens the full logs -- the speaker button sits beside it rather than
          in its own row so it doesn't read as a third, separate control. */}
      {!message.pending &&
        (parts.length > 0 || canShowLogs || (canSpeak && playback.supported)) && (
        <div
          style={{
            alignSelf: "flex-start",
            marginTop: 5,
            display: "flex",
            alignItems: "center",
            gap: 10,
          }}
        >
          {canSpeak && playback.supported && (
            <button
              type="button"
              className="chat-speak"
              onClick={() => playback.toggle(toSpeechText(message.text))}
              aria-label={
                playback.speaking ? "Stop reading reply aloud" : "Read reply aloud"
              }
              title={playback.speaking ? "Stop reading aloud" : "Read aloud"}
              style={{
                display: "flex",
                alignItems: "center",
                padding: "3px 2px",
                background: "none",
                border: "none",
                color: playback.speaking ? "var(--accent)" : "var(--fg-dim)",
                cursor: "pointer",
              }}
            >
              {playback.speaking ? <IconStop size={9} /> : <IconSpeaker size={12} />}
            </button>
          )}
          {(parts.length > 0 || canShowLogs) && (
            <button
              type="button"
              className="chat-activity"
              disabled={!canShowLogs}
              onClick={() => onShowLogs?.()}
              title={canShowLogs ? "Show the run logs" : undefined}
              style={{
                padding: "3px 0",
                background: "none",
                border: "none",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                fontVariantNumeric: "tabular-nums",
                color: "var(--fg-dim)",
                cursor: canShowLogs ? "pointer" : "default",
                letterSpacing: "0.02em",
              }}
            >
              {parts.length > 0 ? parts.join(" · ") : "details"}
              {canShowLogs && <span aria-hidden> ›</span>}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
