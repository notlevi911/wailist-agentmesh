"use client";
import { useEffect, useRef, useState } from "react";
import { IconMic } from "@/components/ui";
import { ChatMessage } from "./ChatMessage";
import { stop as stopSpeaking } from "./speechPlayback";
import { useSpeechRecognition } from "./useSpeechRecognition";
import type { ChatSession } from "./useChatSession";

// Composer sizing. Three lines at rest so a typical prompt is visible as a
// whole rather than through a one-line slot, growing to roughly ten before it
// starts scrolling.
const COMPOSER_MIN_H = 76;
const COMPOSER_MAX_H = 220;

// The human half of the console: transcript above, composer below.
//
// Deliberately self-sufficient. Someone who never opens the Logs pane should
// still be able to run the workflow, read the answer, and see what it cost --
// that is the whole "not important for normal people" requirement.

interface ChatPaneProps {
  session: ChatSession;
  /** Sends a message; the parent turns it into a run. */
  onSend: (text: string) => void;
  /** True while a run is in flight — the composer waits rather than queueing. */
  busy: boolean;
  onShowLogs?: () => void;
}

export function ChatPane({ session, onSend, busy, onShowLogs }: ChatPaneProps) {
  const [draft, setDraft] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);
  // What `draft` held before the current dictation session started, so a
  // dictated segment replaces itself as it's revised (interim results
  // repeat the whole growing utterance, not deltas) without touching
  // anything the user typed or a previous dictation pass already committed.
  const dictationBaseRef = useRef("");

  const stt = useSpeechRecognition((text, isFinal) => {
    const base = dictationBaseRef.current;
    const merged = base + (base.trim() && text ? " " : "") + text;
    setDraft(merged);
    if (isFinal) dictationBaseRef.current = merged;
  });

  const inputRef = useRef<HTMLTextAreaElement>(null);

  // The composer starts three lines tall and grows with the draft up to
  // COMPOSER_MAX_H, after which it scrolls. Height has to be reset to "auto"
  // before each measurement -- scrollHeight never shrinks below the element's
  // current height, so without the reset the box could only ever grow.
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(
      Math.max(el.scrollHeight, COMPOSER_MIN_H),
      COMPOSER_MAX_H,
    )}px`;
  }, [draft]);

  // Follow the conversation as it grows, the same way the log list does.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [session.messages]);

  // ChatPane stays mounted across a Chat/Inspect/Term tab switch (see
  // ChatRail) but unmounts when the whole rail does, i.e. when the user
  // leaves this workflow's canvas -- stop an in-progress reply readout so it
  // doesn't keep talking over whatever page comes next.
  useEffect(() => stopSpeaking, []);

  const submit = () => {
    const text = draft.trim();
    if (!text || busy) return;
    // Stop any in-flight dictation first: without this, a Send tap mid
    // sentence would clear `draft` here and then have the session's next
    // (now stray) result callback write the tail of what was just said back
    // into the box, after the message it belonged with had already sent.
    stt.stop();
    setDraft("");
    onSend(text);
  };

  const toggleDictation = () => {
    if (stt.listening) {
      stt.stop();
      return;
    }
    dictationBaseRef.current = draft;
    stt.start();
  };

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        // flex:1 against the wrapper's explicit column, rather than a
        // percentage height against an indefinite parent.
        flex: 1,
        minHeight: 0,
        // A flex item defaults to min-width:auto, i.e. it refuses to shrink
        // below its own min-content width. The composer's textarea carries an
        // intrinsic ~20-column width, which floored this pane at ~289px and
        // pushed the bubbles and the Send button past the rail's right edge
        // once the rail was dragged near its 260px minimum.
        minWidth: 0,
      }}
    >
      {/* Transcript */}
      <div
        role="log"
        aria-live="polite"
        aria-label="Conversation"
        className="chat-transcript"
        style={{
          flex: 1,
          minHeight: 0,
          minWidth: 0,
          overflow: "auto",
          padding: "10px 14px",
          display: "flex",
          flexDirection: "column",
          gap: 12,
        }}
      >
        {session.hydrated && session.messages.length === 0 && (
          <div
            style={{
              margin: "auto",
              textAlign: "center",
              maxWidth: "34ch",
              color: "var(--fg-dim)",
              fontSize: 12,
              lineHeight: 1.6,
            }}
          >
            Ask this workflow something to run it.
            <span
              style={{
                display: "block",
                marginTop: 8,
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                lineHeight: 1.7,
              }}
            >
              {/* Honest about the model we actually ship: every message is an
                  independent run, exactly as n8n's chat trigger behaves
                  without a memory node. Better to say so than to imply a
                  continuity the engine does not have. */}
              each message runs the workflow fresh — agents don&apos;t remember
              previous messages yet
            </span>
          </div>
        )}

        {session.messages.map((m) => (
          <ChatMessage key={m.id} message={m} onShowLogs={onShowLogs} />
        ))}
        <div ref={bottomRef} />
      </div>

      {/* Composer */}
      <div style={{ flexShrink: 0, minWidth: 0 }}>
        {stt.error && (
          <div
            role="alert"
            style={{
              padding: "6px 10px 0",
              fontSize: 11,
              lineHeight: 1.5,
              color: "var(--danger)",
            }}
          >
            {stt.error}
          </div>
        )}
        <div
          style={{
            minWidth: 0,
            borderTop: "1px solid var(--border)",
            padding: 10,
            display: "flex",
            gap: 8,
            alignItems: "flex-end",
          }}
        >
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            // Enter sends, Shift+Enter starts a new line -- the convention
            // every chat UI uses, including n8n's.
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit();
            }
          }}
          ref={inputRef}
          rows={3}
          placeholder={busy ? "Running…" : stt.listening ? "Listening…" : "Ask something…"}
          // Disabled while listening, not just while busy: onText's merge
          // (`dictationBaseRef.current + new interim text`) overwrites `draft`
          // wholesale on every speech chunk, so a manual edit typed mid-session
          // would get silently clobbered by the next interim result with no
          // sign anything was lost. Locking the box for the session's duration
          // is simpler and safer than trying to reconcile the two input
          // sources; toggleDictation re-captures `draft` as the new base the
          // next time dictation starts, so nothing already typed is lost.
          disabled={busy || stt.listening}
          style={{
            flex: 1,
            // Without this the textarea's intrinsic column width becomes a
            // hard floor and the Send button gets pushed out of the rail.
            minWidth: 0,
            resize: "none",
            minHeight: COMPOSER_MIN_H,
            maxHeight: COMPOSER_MAX_H,
            padding: "9px 11px",
            background: "var(--bg)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-2)",
            color: "var(--fg)",
            fontFamily: "var(--font-sans)",
            fontSize: 13,
            lineHeight: 1.5,
            outline: "none",
            opacity: busy ? 0.6 : 1,
          }}
        />
        {stt.supported && (
          <button
            type="button"
            className={`chat-mic${stt.listening ? " chat-mic--recording" : ""}`}
            onClick={toggleDictation}
            disabled={busy}
            aria-label={stt.listening ? "Stop dictation" : "Dictate a message"}
            aria-pressed={stt.listening}
            title={stt.listening ? "Stop dictation" : "Dictate a message"}
            style={{
              height: 38,
              width: 38,
              flexShrink: 0,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              background: "var(--bg)",
              border: `1px solid ${stt.listening ? "var(--danger)" : "var(--border)"}`,
              borderRadius: "var(--r-2)",
              color: stt.listening ? "var(--danger)" : "var(--fg-dim)",
              cursor: busy ? "default" : "pointer",
              opacity: busy ? 0.6 : 1,
            }}
          >
            <IconMic size={14} />
          </button>
        )}
        <button
          type="button"
          className="chat-send"
          onClick={submit}
          disabled={busy || draft.trim() === ""}
          aria-label="Send message"
          style={{
            // 38px tall but padded wide to clear the 44px touch target.
            height: 38,
            minWidth: 44,
            padding: "0 14px",
            background: "var(--accent)",
            border: "1px solid var(--accent)",
            borderRadius: "var(--r-2)",
            color: "var(--accent-fg)",
            fontFamily: "var(--font-sans)",
            fontSize: 13,
            fontWeight: 600,
            cursor: busy || draft.trim() === "" ? "default" : "pointer",
            opacity: busy || draft.trim() === "" ? 0.45 : 1,
            flexShrink: 0,
          }}
        >
          Send
        </button>
        </div>
      </div>
    </div>
  );
}
