export const BYTES_PER_MB = 1_000_000;

export function bytesToMB(bytes: number): number {
  return Number((bytes / BYTES_PER_MB).toFixed(6));
}

export function mbToBytes(mb: number): number {
  return Math.round(mb * BYTES_PER_MB);
}
