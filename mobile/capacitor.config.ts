import type { CapacitorConfig } from "@capacitor/cli";

// The native Android shell.
//
// It wraps the same Next.js frontend the web app ships, built as a static
// export (frontend/next.config.ts, MOBILE_BUILD=1) and copied into www/. What
// makes it an app rather than a bookmark is the geofence plugin in
// android/app/src/main/java/ai/agentmesh/app: the OS watches the boundary and
// wakes us on a crossing, which no web page can do.
const config: CapacitorConfig = {
  appId: "ai.agentmesh.app",
  appName: "AgentMesh",
  webDir: "www",

  server: {
    // https, not the default capacitor:// -- the WebView treats an https
    // origin as a secure context, which the geolocation and notification APIs
    // both require. It also keeps the origin stable across releases, which
    // matters because localStorage and IndexedDB are keyed on it.
    androidScheme: "https",
  },
};

export default config;
