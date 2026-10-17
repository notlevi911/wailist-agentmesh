"use client";
import { useState, type InputHTMLAttributes } from "react";

export function PasswordField({ label, ...props }: InputHTMLAttributes<HTMLInputElement> & { id: string; label: string }) {
  const [visible, setVisible] = useState(false);
  return <>
    <label htmlFor={props.id}>{label}</label>
    <div style={{ display: "flex", gap: 8 }}>
      <input {...props} type={visible ? "text" : "password"} />
      <button type="button" disabled={props.disabled} aria-label={`${visible ? "Hide" : "Show"} ${label.toLowerCase()}`}
        aria-pressed={visible} onClick={() => setVisible((current) => !current)}>
        {visible ? "Hide" : "Show"}
      </button>
    </div>
  </>;
}

