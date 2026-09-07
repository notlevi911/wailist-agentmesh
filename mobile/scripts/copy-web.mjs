// Copies the frontend's static export into www/, which Capacitor serves.
//
// A copy rather than building straight into place: Next 16 refuses a distDir
// that navigates outside its own project ("distDirRoot should not navigate out
// of the projectPath"), so the export lands in frontend/out-mobile and is
// moved here.
import { cp, rm, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, "..", "..", "frontend", "out-mobile");
const dest = join(here, "..", "www");

try {
  await stat(src);
} catch {
  console.error(
    `No export at ${src}.\nRun \`npm run build:web\` first (or \`npm run sync\`, which does both).`,
  );
  process.exit(1);
}

// Replace rather than merge: a stale chunk left behind from a previous build
// is served happily by the WebView and produces failures that look like source
// changes having no effect.
await rm(dest, { recursive: true, force: true });
await cp(src, dest, { recursive: true });
console.log(`copied ${src} -> ${dest}`);
