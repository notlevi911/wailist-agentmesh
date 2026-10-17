import { expect, it } from "vitest";
import { passwordBytes, scorePassword } from "./strength";

it("matches the server byte bounds", () => {
  expect(scorePassword("short").valid).toBe(false);
  expect(scorePassword("éééé").valid).toBe(true);
  expect(passwordBytes("é".repeat(36))).toBe(72);
  expect(scorePassword("é".repeat(36)).valid).toBe(true);
  expect(scorePassword("é".repeat(37))).toMatchObject({ valid: false, tooLong: true });
  expect(scorePassword("x".repeat(73)).valid).toBe(false);
});

