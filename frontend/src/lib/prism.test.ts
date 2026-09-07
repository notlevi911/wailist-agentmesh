import { describe, it, expect } from "vitest";
import { formatUsd, totalCostMicros, type PrismEndpoint } from "./prism";
import {
  MAX_PARAM_FILE_BYTES,
  bytesToBase64,
  formatFileSize,
  readFileAsBase64,
} from "./fileEncoding";
import { capabilityLabels, priceLabel } from "@/components/bazaar/ConsoleCard";
import { X402_PLATFORM_FEE_USD_MICROS } from "./bazaar";

// The flat AgentMesh markup, mirroring models.X402PlatformFeeUSDMicros. The
// real value arrives from the backend at runtime; this is what the sums below
// are checked against.
const FEE = 1_500_000;

describe("what a Prism call actually costs", () => {
  // The whole reason the console itemises price: Prism's endpoints cost cents,
  // the platform fee is $1.50, so quoting the endpoint price alone understates
  // a run by roughly 7x. A user who reads "0.25 USDC" and is billed $1.75 has
  // been misled, however correct each number is on its own.
  it("adds the platform fee to the endpoint price", () => {
    const cheap = { amountMicros: 100_000 } as PrismEndpoint;
    const dear = { amountMicros: 250_000 } as PrismEndpoint;
    expect(totalCostMicros(cheap, FEE)).toBe(1_600_000);
    expect(totalCostMicros(dear, FEE)).toBe(1_750_000);
  });

  it("formats a total as a plain dollar figure", () => {
    expect(formatUsd(1_600_000)).toBe("$1.60");
    expect(formatUsd(1_750_000)).toBe("$1.75");
    expect(formatUsd(100_000)).toBe("$0.10");
    expect(formatUsd(0)).toBe("$0.00");
  });

  it("makes the fee the dominant term for every current price", () => {
    for (const amount of [100_000, 200_000, 250_000]) {
      const total = totalCostMicros({ amountMicros: amount } as PrismEndpoint, FEE);
      expect(total - amount).toBe(FEE);
      // If this ever stops holding, the console's emphasis is worth revisiting.
      expect(FEE).toBeGreaterThan(amount);
    }
  });
});

describe("file encoding", () => {
  // The expected value uses \u escapes, not literal characters: a raw NUL
  // byte in the source makes git treat this whole file as binary, so it
  // shows as "Bin 0 -> 4819 bytes" and is undiffable in every future PR.
  it("round-trips bytes through base64", () => {
    const bytes = new Uint8Array([0x25, 0x50, 0x44, 0x46, 0x00, 0xff]);
    expect(atob(bytesToBase64(bytes))).toBe("%PDF\u0000\u00ff");
  });

  // The obvious String.fromCharCode(...bytes) blows the argument limit well
  // inside the 2 MB range this is used for, so the chunking is load-bearing
  // rather than a micro-optimisation.
  it("encodes a file large enough to break the naive spread", () => {
    const big = new Uint8Array(300_000).fill(65);
    const encoded = bytesToBase64(big);
    expect(encoded.length).toBeGreaterThan(390_000);
    expect(atob(encoded).length).toBe(300_000);
  });

  it("names the real size when a file is over the limit", async () => {
    const oversized = {
      name: "resume.pdf",
      size: MAX_PARAM_FILE_BYTES + 1,
      type: "application/pdf",
      arrayBuffer: async () => new ArrayBuffer(0),
    } as unknown as File;

    await expect(readFileAsBase64(oversized)).rejects.toThrow(/resume\.pdf/);
    await expect(readFileAsBase64(oversized)).rejects.toThrow(/limit is 2\.0 MB/);
  });

  it("accepts a file at exactly the limit", async () => {
    const bytes = new Uint8Array(16).fill(7);
    const atLimit = {
      name: "cv.pdf",
      size: MAX_PARAM_FILE_BYTES,
      type: "application/pdf",
      arrayBuffer: async () => bytes.buffer,
    } as unknown as File;

    const encoded = await readFileAsBase64(atLimit);
    expect(encoded.fileName).toBe("cv.pdf");
    expect(encoded.mimeType).toBe("application/pdf");
    expect(encoded.value).toBe(bytesToBase64(bytes));
  });

  it("formats sizes at each unit boundary", () => {
    expect(formatFileSize(512)).toBe("512 B");
    expect(formatFileSize(2048)).toBe("2 KB");
    expect(formatFileSize(2 * 1024 * 1024)).toBe("2.0 MB");
  });
});

describe("capability labels on a partner card", () => {
  it("collapses quality tiers into one capability", () => {
    expect(
      capabilityLabels([
        "https://prism-99h2.onrender.com/code-review-accurate",
        "https://prism-99h2.onrender.com/code-review-fast",
        "https://prism-99h2.onrender.com/resume-screen-fast",
        "https://prism-99h2.onrender.com/resume-screen-accurate",
      ]),
    ).toEqual(["Code review", "Resume screen"]);
  });

  it("keeps a single-endpoint console readable", () => {
    expect(capabilityLabels(["https://tendrilregister.007575.xyz/x402/run"])).toEqual([
      "Run",
    ]);
  });

  // A word that merely ENDS in a tier name is not a tier: "broadcast" must not
  // lose its tail, and a bare tier segment has nothing to strip down to.
  it("only strips a tier when it is its own word", () => {
    expect(capabilityLabels(["https://x.example/broadcast"])).toEqual(["Broadcast"]);
    expect(capabilityLabels(["https://x.example/fast"])).toEqual(["Fast"]);
  });

  it("skips a malformed URL instead of throwing", () => {
    expect(capabilityLabels(["not a url", "https://x.example/thing"])).toEqual(["Thing"]);
  });
});

describe("what a partner card quotes", () => {
  // Review finding: the card showed Prism's share only, so it read
  // "0.1–0.25 USDC" for runs the console then charges $1.60–$1.75 for. The
  // card is the main way into the console, so it has to agree with it.
  it("quotes the all-in range, not the vendor's share", () => {
    const r = (amountMicros: number) => ({ amountMicros }) as never;
    expect(priceLabel([r(100_000), r(200_000), r(250_000)])).toBe("$1.60-$1.75 a run");
  });

  it("collapses to one figure when every endpoint costs the same", () => {
    const r = (amountMicros: number) => ({ amountMicros }) as never;
    expect(priceLabel([r(10_000)])).toBe("$1.51 a run");
  });

  // The constant has to track models.X402PlatformFeeUSDMicros; if the backend
  // moves and this does not, every Bazaar card silently misquotes.
  it("uses the same platform fee the backend charges", () => {
    expect(X402_PLATFORM_FEE_USD_MICROS).toBe(1_500_000);
  });
});
