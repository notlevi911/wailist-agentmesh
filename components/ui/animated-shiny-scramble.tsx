"use client";

import React, { useState, useCallback, useRef, useEffect } from "react";
import { motion } from "framer-motion";
import { cn } from "@/lib/utils";

const CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";

interface AnimatedShinyScrambleProps {
  text: string;
  className?: string;
  gradientColors?: string;
  gradientDuration?: number;
  autoPlay?: boolean;
  delay?: number;
}

export function AnimatedShinyScramble({
  text,
  className,
  gradientColors = "linear-gradient(90deg, #6b21a8 0%, #9333ea 25%, #e9d5ff 50%, #9333ea 75%, #6b21a8 100%)",
  gradientDuration = 2.5,
  autoPlay = false,
  delay = 400,
}: AnimatedShinyScrambleProps) {
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
    <motion.span
      className={cn("cursor-default select-none", className)}
      style={{
        background: gradientColors,
        backgroundSize: "200% auto",
        WebkitBackgroundClip: "text",
        WebkitTextFillColor: "transparent",
        backgroundClip: "text",
      }}
      animate={{ backgroundPosition: ["0% center", "200% center"] }}
      transition={{
        duration: gradientDuration,
        repeat: Infinity,
        repeatType: "reverse",
        ease: "linear",
      }}
      onMouseEnter={scramble}
    >
      {displayText}
    </motion.span>
  );
}
