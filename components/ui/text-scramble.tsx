"use client";

import React, { useState, useCallback, useRef, useEffect } from "react";

const CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";

interface TextScrambleProps {
  text: string;
  className?: string;
  style?: React.CSSProperties;
  autoPlay?: boolean;
  delay?: number;
}

export function TextScramble({
  text,
  className = "",
  style,
  autoPlay = false,
  delay = 400,
}: TextScrambleProps) {
  const [displayText, setDisplayText] = useState(text);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const frameRef = useRef(0);
  const isScrambling = useRef(false);

  const scramble = useCallback(() => {
    if (isScrambling.current) return;
    isScrambling.current = true;
    frameRef.current = 0;
    const duration = text.length * 6;

    if (intervalRef.current) clearInterval(intervalRef.current);

    intervalRef.current = setInterval(() => {
      frameRef.current++;
      const progress = frameRef.current / duration;
      const revealedLength = Math.floor(progress * text.length);

      setDisplayText(
        text
          .split("")
          .map((char, i) => {
            if (char === " ") return " ";
            if (i < revealedLength) return text[i];
            return CHARS[Math.floor(Math.random() * CHARS.length)];
          })
          .join("")
      );

      if (frameRef.current >= duration) {
        clearInterval(intervalRef.current!);
        setDisplayText(text);
        isScrambling.current = false;
      }
    }, 50);
  }, [text]);

  useEffect(() => {
    if (!autoPlay) return;
    const t = setTimeout(scramble, delay);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(
    () => () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    },
    []
  );

  return (
    <span className={`cursor-default select-none ${className}`} style={style} onMouseEnter={scramble}>
      {displayText}
    </span>
  );
}
