export const MIN_BYTES = 8;
export const MAX_BYTES = 72;

export function passwordBytes(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function scorePassword(value: string) {
  const bytes = passwordBytes(value);
  const valid = bytes >= MIN_BYTES && bytes <= MAX_BYTES;
  const variety = Number(/[a-z]/.test(value)) + Number(/[A-Z]/.test(value)) +
    Number(/[0-9]/.test(value)) + Number(/[^A-Za-z0-9]/.test(value));
  const score = valid ? Math.min(4, Math.floor(value.length / 8) + Number(variety >= 3)) : 0;
  return { valid, score, tooLong: bytes > MAX_BYTES };
}

