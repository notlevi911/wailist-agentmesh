"use client";
import { useEffect, useRef, type ReactNode } from "react";
import styles from "./SettingsPage.module.css";

export function SettingsSection({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return <section className={`${styles.card} ${styles.panel}`} aria-labelledby={`settings-${id}`}>
    <h2 id={`settings-${id}`}>{title}</h2>{children}
  </section>;
}

export function useActiveForm() {
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => { active.current = false; };
  }, []);
  return active;
}

export function usdInput(micros: number): string {
  const digits = String(micros).padStart(7, "0");
  const fraction = digits.slice(-6).replace(/0+$/, "");
  return `${digits.slice(0, -6)}${fraction ? "." + fraction : ""}`;
}

export function parseUsdInput(value: string): number | null {
  const parts = /^(\d+)(?:\.(\d{1,6}))?$/.exec(value.trim());
  if (!parts || parts[1].length > 16) return null;
  const micros = Number(parts[1] + (parts[2] ?? "").padEnd(6, "0"));
  return Number.isSafeInteger(micros) ? micros : null;
}

export function formatDateUTC(value?: string): string {
  if (!value || !Number.isFinite(Date.parse(value))) return "Unavailable";
  return new Intl.DateTimeFormat("en", { dateStyle: "medium", timeZone: "UTC" }).format(new Date(value));
}
