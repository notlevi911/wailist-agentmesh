import type { NextConfig } from "next";

// BACKEND_URL is a server-only env var (not NEXT_PUBLIC_) — it never reaches
// the client bundle. Set it to the Railway backend URL in production.
// NEXT_PUBLIC_API_URL should be set to "/api" so all client fetches go through
// this proxy and the auth cookie stays on the frontend's own domain.
const BACKEND_URL = process.env.BACKEND_URL ?? "";

const MOBILE = process.env.MOBILE_BUILD === "1";

const nextConfig: NextConfig = {
  ...(MOBILE ? { output: "export" as const, distDir: "out-mobile" } : {}),
  // Next's rewrites() proxy kills the upstream connection after 30s by
  // default -- too short for the chat-driven workflow builder, whose
  // meta-agent loop can take several sequential Gemini round trips per
  // message. Give it more room.
  experimental: {
    proxyTimeout: 120000,
  },
  async rewrites() {
    if (!BACKEND_URL) return [];
    return [
      {
        source: "/api/:path*",
        destination: `${BACKEND_URL}/:path*`,
      },
    ];
  },
};

export default nextConfig;
