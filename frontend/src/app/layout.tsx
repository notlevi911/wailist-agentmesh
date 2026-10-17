import type { Metadata, Viewport } from "next";
import { NativeBoot } from "@/components/native/NativeBoot";
import { AppSplash } from "@/components/native/AppSplash";
import { BottomNav } from "@/components/nav/BottomNav";
import { RouteTransition } from "@/components/nav/RouteTransition";
import { IS_NATIVE } from "@/lib/nativeAuth";
import { buildCsp } from "@/lib/csp";
import { SURFACE } from "@/lib/tokens";
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
  // Search Console ownership of https://www.agent-mesh.app/, by the account
  // that owns the Google Cloud project -- Google's OAuth verification requires
  // that link. Public by design; removing it un-verifies the property.
  verification: {
    google: "9-jSNxDO_dUs9vmhvHwqhurWMQ1Fr6nbVoPrnp_fN10",
  },
};

// Next injects this by default, but the app is now explicitly responsive, so the
// contract is stated rather than inherited. `maximumScale` is deliberately left
// unset: capping zoom locks out anyone who needs to magnify text.
//
// `viewportFit: "cover"` is not decoration. mobile/android/variables.gradle sets
// targetSdkVersion = 36, and edge-to-edge is enforced for anything targeting
// Android 15 or later: the WebView draws behind the status and navigation bars
// whether or not the page is ready for it. Without this opt-in the safe-area
// insets read as zero, so the top nav sits underneath the status bar. At 36
// (Android 16) the opt-out is gone entirely, so there is no version of this app
// that goes back to the old behaviour. Anything touching a screen edge pairs
// this with the .am-safe-* classes in responsive.css.
export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  // A theme colour has to be a colour, not a var(): the browser reads this
  // meta tag to paint chrome outside the document, where the page's custom
  // properties do not exist. Sourced from lib/tokens.ts, where a test asserts
  // it still equals --bg -- it used to be a literal with a comment asking for
  // it to be kept in step with globals.css by hand.
  themeColor: SURFACE.bg,
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
      {/*
        The Content-Security-Policy, in the native shell only.
        
        A meta tag rather than a header because there is no server: the bundle
        is files on the device, served by the WebView itself, so a header has
        nobody to set it. The web app is behind Vercel and gets its own headers,
        which is why this is gated on IS_NATIVE -- a build-time constant, so the
        tag is absent from the web bundle entirely rather than merely inert.
        
        NEXT_PUBLIC_API_URL is read here, at build time, for the same reason
        lib/api.ts reads it: connect-src has to name the actual backend this
        build talks to. See lib/csp.ts for what the policy does and does not
        buy.
      */}
      {IS_NATIVE && (
        <meta
          httpEquiv="Content-Security-Policy"
          content={buildCsp(process.env.NEXT_PUBLIC_API_URL)}
        />
      )}
      <body
        style={{
          margin: 0,
          background: "var(--bg)",
          color: "var(--fg)",
          minHeight: "100dvh",
        }}
      >
        <NativeBoot />
        {/* Above NativeBoot in the tree on purpose: NativeBoot renders null and
            does the work, this covers the screen while it happens. Both are
            mounted here rather than per-route so the launch frame is the same
            whichever route the app opens on. */}
        <AppSplash />
        {/* Wraps the page so an arriving screen can be given a direction on a
            phone. A no-op on desktop and under reduced motion. */}
        <RouteTransition>{children}</RouteTransition>
        {/* Renders itself only on a handheld, and only at a section root. One
            mount point rather than one per page: it is app-wide chrome, and
            every page that would have to opt in is a page that can forget to. */}
        <BottomNav />
      </body>
    </html>
  );
}
