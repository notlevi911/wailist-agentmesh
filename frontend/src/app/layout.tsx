import type { Metadata, Viewport } from "next";
import { NativeBoot } from "@/components/native/NativeBoot";
import localFont from "next/font/local";
import "./globals.css";

// Self-hosted rather than next/font/google, which fetches from
// fonts.gstatic.com at BUILD time. Two reasons, and the second is the real one:
//
//  1. The build stops depending on network reachability. A CDN outage or an
//     offline machine currently fails `next build` outright.
//  2. This bundle is also served from inside the native Android shell, off the
//     device, with no server in front of it. An app whose typography depends on
//     reaching Google is an app that renders wrong on a train.
//
// These are the same Geist files -- the `geist` package is already a
// dependency, and the variable faces cover every weight in one file each, just
// as the Google-hosted versions do.
const geistSans = localFont({
  src: "../../node_modules/geist/dist/fonts/geist-sans/Geist-Variable.woff2",
  variable: "--font-sans",
  weight: "100 900",
  display: "swap",
});

const geistMono = localFont({
  src: "../../node_modules/geist/dist/fonts/geist-mono/GeistMono-Variable.woff2",
  variable: "--font-mono",
  weight: "100 900",
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL("https://www.agent-mesh.app"),
  title: "AgentMesh",
  description: "Design, deploy, and monitor AI agent workflows.",
  openGraph: {
    title: "AgentMesh",
    description: "Design, deploy, and monitor AI agent workflows.",
    url: "/",
    siteName: "AgentMesh",
    type: "website",
    // No `images` here on purpose: opengraph-image.png (the file convention)
    // already supplies og:image, and it takes precedence over anything set
    // here -- setting both leaves og:image pointing at the generated route
    // while twitter:image points somewhere else, which is how this file
    // briefly ended up advertising two different preview images.
  },
  twitter: {
    card: "summary_large_image",
    title: "AgentMesh",
    description: "Design, deploy, and monitor AI agent workflows.",
  },
  // The square mark, as distinct from the 1200x630 preview banner above.
  // A listing that renders an avatar wants this shape, not the banner, and
  // until now the only thing on the domain was favicon.ico.
  icons: {
    icon: [{ url: "/logo.png", type: "image/png" }],
    apple: [{ url: "/logo.png" }],
  },
};

// Next injects this by default, but the app is now explicitly responsive, so the
// contract is stated rather than inherited. `maximumScale` is deliberately left
// unset: capping zoom locks out anyone who needs to magnify text.
export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable}`}
      style={{ fontFamily: "var(--font-sans)" }}
    >
      <body
        style={{
          margin: 0,
          background: "var(--bg)",
          color: "var(--fg)",
          minHeight: "100dvh",
        }}
      >
        <NativeBoot />
        {children}
      </body>
    </html>
  );
}
