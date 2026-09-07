// Shared file→base64 encoding for anything that uploads a file into a paid
// x402 request body: the canvas Inspector's `file` param fields and the Prism
// console's file inputs. Lifted here rather than copied so the chunking rule
// below has exactly one home.

// MAX_PARAM_FILE_BYTES bounds an uploaded file's DECODED size. Mirrors
// maxPrismFileBytes on the backend, which enforces it for real — this side is
// a courtesy so the user finds out before uploading rather than after.
export const MAX_PARAM_FILE_BYTES = 2 * 1024 * 1024;

// bytesToBase64 encodes in 32KB chunks. The obvious one-liner,
// String.fromCharCode(...bytes), spreads every byte as a separate argument and
// blows the call-stack argument limit on files of even a few hundred KB —
// which is well inside the range this is used for.
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

// formatFileSize renders a decoded byte count the way the size-limit messages
// do, so "is 2.4 MB — the limit is 2 MB" and the picker's own label agree.
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

// base64DecodedBytes is the decoded size of a base64 string, for callers that
// hold the encoded form and want to show the user the real file size. Base64
// inflates by 4/3, so reporting the string's own length overstates every file
// by a third.
export function base64DecodedBytes(base64: string): number {
  return Math.floor((base64.length * 3) / 4);
}

export interface EncodedFile {
  value: string;
  fileName: string;
  mimeType: string;
  size: number;
}

// readFileAsBase64 rejects an oversized file before reading it into memory.
// The error message names the actual size, because "too big" without a number
// gives the user nothing to act on.
export async function readFileAsBase64(file: File): Promise<EncodedFile> {
  if (file.size > MAX_PARAM_FILE_BYTES) {
    throw new Error(
      `${file.name} is ${formatFileSize(file.size)} — the limit is ${formatFileSize(MAX_PARAM_FILE_BYTES)}.`,
    );
  }
  const bytes = new Uint8Array(await file.arrayBuffer());
  return {
    value: bytesToBase64(bytes),
    fileName: file.name,
    mimeType: file.type,
    size: file.size,
  };
}
