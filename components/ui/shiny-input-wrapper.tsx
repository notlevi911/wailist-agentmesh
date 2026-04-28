"use client";

interface ShinyInputWrapperProps {
  children: React.ReactNode;
  className?: string;
}

export function ShinyInputWrapper({ children, className = "" }: ShinyInputWrapperProps) {
  return (
    <>
      <style>{`
        @property --input-gradient-angle {
          syntax: "<angle>";
          initial-value: 0deg;
          inherits: false;
        }

        .shiny-input-wrap {
          position: relative;
          border-radius: 0.4rem;
          border: 1px solid #222222;
          background: #141414;
          transition: border-color 300ms ease;
        }

        .shiny-input-wrap:focus-within {
          border: 1px solid transparent;
          background:
            linear-gradient(#141414, #141414) padding-box,
            conic-gradient(
              from var(--input-gradient-angle),
              transparent 0%,
              #7c3aed 10%,
              #c4b5fd 20%,
              #7c3aed 30%,
              transparent 40%
            ) border-box;
          animation: input-gradient-spin 3s linear infinite;
        }

        [data-slot="input-otp-slot"][data-active="true"] {
          border: 1px solid transparent !important;
          background:
            linear-gradient(#1a1a1a, #1a1a1a) padding-box,
            conic-gradient(
              from var(--input-gradient-angle),
              transparent 0%,
              #7c3aed 10%,
              #c4b5fd 20%,
              #7c3aed 30%,
              transparent 40%
            ) border-box !important;
          animation: input-gradient-spin 3s linear infinite;
        }

        @keyframes input-gradient-spin {
          to { --input-gradient-angle: 360deg; }
        }
      `}</style>
      <div className={`shiny-input-wrap ${className}`}>
        {children}
      </div>
    </>
  );
}
