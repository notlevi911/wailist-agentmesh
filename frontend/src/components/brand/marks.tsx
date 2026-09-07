import type { SimpleIcon } from "simple-icons";

// Every brand mark in the app is drawn on the same 24-unit grid in
// currentColor, so a logo drops into any icon box -- a canvas node's, the
// landing marquee's -- and inherits the exact size and colour the placeholder
// letter had. Only the glyph shape changes.
//
// These renderers live apart from the canvas node's template->icon table on
// purpose. That table statically imports ~23 simple-icons so the bundler can
// tree-shake the other ~3,400; a caller that wants one mark would otherwise
// pull in all 23 just to draw it. Importing from here costs only what it uses.

interface MarkProps {
  size?: number;
}

// Shared attributes. `display: block` kills the inline-baseline gap that
// otherwise pushes a logo off-centre inside a flex icon box.
const markSvg = {
  role: "img",
  viewBox: "0 0 24 24",
  fill: "currentColor",
  style: { display: "block" as const },
};

// Renders any icon from the simple-icons package. The caller does the static
// import of the icon it needs, which is what keeps the bundle honest.
export function SimpleIconMark({
  icon,
  size = 15,
}: MarkProps & { icon: SimpleIcon }) {
  return (
    <svg {...markSvg} aria-label={icon.title} width={size} height={size}>
      <path d={icon.path} />
    </svg>
  );
}

// simple-icons no longer ships Slack or Microsoft Teams -- both were removed
// at the brands' request, pending trademark permission -- so these two are
// drawn here on the same grid as everything else.
export function SlackMark({ size = 15 }: MarkProps) {
  return (
    <svg {...markSvg} aria-label="Slack" width={size} height={size}>
      {[0, 90, 180, 270].map((a) => (
        <g key={a} transform={`rotate(${a} 12 12)`}>
          <rect x="12" y="9.3" width="6.4" height="2.7" rx="1.35" />
          <rect x="15.7" y="12" width="2.7" height="2.7" rx="1.35" />
        </g>
      ))}
    </svg>
  );
}

export function TeamsMark({ size = 15 }: MarkProps) {
  return (
    <svg {...markSvg} aria-label="Microsoft Teams" width={size} height={size}>
      <circle cx="8" cy="5.6" r="2.3" />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M10 7 h8 a2 2 0 0 1 2 2 v8 a2 2 0 0 1 -2 2 h-8 a2 2 0 0 1 -2 -2 v-8 a2 2 0 0 1 2 -2 Z M11 10 v1.7 h2.1 v5.3 h1.8 v-5.3 h2.1 v-1.7 Z"
      />
    </svg>
  );
}

// Tendril publishes no icon to any package, so this reproduces the geometry of
// the mark on their own site (tendrilhq.com/favicon.svg): a slab-serif "T",
// cream on their green. Scaled from their 32-unit grid to our 24 (x0.75) and
// drawn in currentColor, dropping the background plate -- the same monochrome
// treatment every other logo here gets.
export function TendrilMark({ size = 15 }: MarkProps) {
  return (
    <svg {...markSvg} aria-label="Tendril" width={size} height={size}>
      {/* top bar */}
      <rect x="3.75" y="5.25" width="16.5" height="3" />
      {/* stem */}
      <rect x="10.5" y="5.25" width="3" height="12.75" />
      {/* serif foot */}
      <rect x="7.5" y="16.5" width="9" height="2.25" />
    </svg>
  );
}
