"use client";

import { useState } from "react";
import type { WorkflowNode } from "@/lib/types";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL && typeof window !== "undefined"
    ? "/api"
    : (process.env.NEXT_PUBLIC_API_URL ?? "");

const PROVIDER_NAMES: Record<string, string> = {
  slack: "Slack",
  github: "GitHub",
  notion: "Notion",
  airtable: "Airtable",
  hubspot: "HubSpot",
  asana: "Asana",
  clickup: "ClickUp",
  jira: "Jira",
  linear: "Linear",
  mailchimp: "Mailchimp",
  gitlab: "GitLab",
  todoist: "Todoist",
};

function displayName(provider: string) {
  return (
    PROVIDER_NAMES[provider] ??
    provider.charAt(0).toUpperCase() + provider.slice(1)
  );
}

export function ConnectorOAuthButton({
  provider,
  workflowId,
  node,
}: {
  provider: string;
  workflowId: string;
  node: WorkflowNode;
}) {
  const secretKey = `${provider}OAuthAccessToken`;
  const connected = node.secrets?.[secretKey] === "__enc__";
  const [hovered, setHovered] = useState(false);
  const [reconnectHovered, setReconnectHovered] = useState(false);

  const connect = () => {
    if (!API_BASE) return;
    window.location.href = `${API_BASE}/connectors/oauth/${provider}/start?workflowId=${encodeURIComponent(workflowId)}&nodeId=${encodeURIComponent(node.id)}`;
  };

  if (connected) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "9px 12px",
          background: "rgba(74, 222, 128, 0.06)",
          border: "1px solid rgba(74, 222, 128, 0.22)",
          borderRadius: "var(--r-2)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          {/* Glowing green connected dot */}
          <span
            style={{
              width: 7,
              height: 7,
              borderRadius: "50%",
              background: "#4ade80",
              boxShadow: "0 0 7px #4ade80",
              flexShrink: 0,
            }}
          />
          <span style={{ fontSize: 12, color: "#4ade80", fontWeight: 500 }}>
            Connected
          </span>
          <span style={{ fontSize: 11, color: "var(--fg-dim)" }}>
            · {displayName(provider)}
          </span>
        </div>
        <button
          type="button"
          onClick={connect}
          onMouseEnter={() => setReconnectHovered(true)}
          onMouseLeave={() => setReconnectHovered(false)}
          style={{
            height: 24,
            padding: "0 9px",
            background: reconnectHovered ? "var(--bg-elev-3)" : "transparent",
            border: `1px solid ${reconnectHovered ? "var(--border-strong)" : "var(--border)"}`,
            borderRadius: "var(--r-2)",
            color: reconnectHovered ? "var(--fg-muted)" : "var(--fg-dim)",
            fontSize: 11,
            fontFamily: "var(--font-sans)",
            cursor: "pointer",
            transition: "all 0.15s",
            whiteSpace: "nowrap",
          }}
        >
          Reconnect
        </button>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={connect}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        height: 36,
        width: "100%",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        gap: 7,
        background: hovered ? "var(--accent-soft)" : "transparent",
        border: `1px solid ${hovered ? "var(--accent)" : "var(--accent-line)"}`,
        borderRadius: "var(--r-2)",
        color: hovered ? "var(--accent)" : "var(--fg-muted)",
        fontSize: 12,
        fontFamily: "var(--font-sans)",
        fontWeight: 500,
        cursor: "pointer",
        transition: "background 0.15s, border-color 0.15s, color 0.15s",
        boxShadow: hovered ? "0 0 14px var(--accent-glow)" : "none",
      }}
    >
      {/* Plug / link icon */}
      <svg
        width="13"
        height="13"
        viewBox="0 0 16 16"
        fill="none"
        style={{ flexShrink: 0 }}
      >
        <path
          d="M6.5 9.5l3-3M4.5 11.5l-1.5 1.5a2.121 2.121 0 000 3 2.121 2.121 0 003 0l3-3a2.121 2.121 0 000-3 2.121 2.121 0 00-.5-.35M11.5 4.5l1.5-1.5a2.121 2.121 0 000-3 2.121 2.121 0 00-3 0l-3 3a2.121 2.121 0 000 3c.15.18.32.34.5.35"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
        />
      </svg>
      Connect {displayName(provider)}
    </button>
  );
}
