"use client";

import { FC, useRef } from "react";
import { motion, MotionValue, useScroll, useTransform } from "framer-motion";
import { cn } from "@/lib/utils";

interface TextRevealByWordProps {
  text: string;
  className?: string;
}

export const TextRevealByWord: FC<TextRevealByWordProps> = ({
  text,
  className,
}) => {
  const targetRef = useRef<HTMLDivElement | null>(null);
  const { scrollYProgress } = useScroll({ 
    target: targetRef,
    // Start tracking when top of element hits top of viewport
    // Stop tracking when bottom of element hits bottom of viewport
    offset: ["start start", "end end"]
  });
  const words = text.split(" ");

  return (
    <div ref={targetRef} className={cn("relative z-0 h-[250vh]", className)}>
      <div className="sticky top-0 mx-auto flex h-screen max-w-2xl items-center px-6">
        <p className="flex flex-wrap text-2xl font-semibold leading-relaxed text-foreground/20 md:text-3xl lg:text-4xl">
          {words.map((word, i) => {
            // Adjust so it finishes early, giving time to read
            const start = (i / words.length) * 0.7;
            const end = start + (1 / words.length) * 0.7;
            return (
              <Word key={i} progress={scrollYProgress} range={[start, end]}>
                {word}
              </Word>
            );
          })}
        </p>
      </div>
    </div>
  );
};

interface WordProps {
  children: React.ReactNode;
  progress: MotionValue<number>;
  range: [number, number];
}

const Word: FC<WordProps> = ({ children, progress, range }) => {
  const opacity = useTransform(progress, range, [0, 1]);
  return (
    <span className="relative mr-2">
      <span className="absolute opacity-20">{children}</span>
      <motion.span style={{ opacity }} className="text-foreground">
        {children}
      </motion.span>
    </span>
  );
};
