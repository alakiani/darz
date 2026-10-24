// The Users table's and Invite form's Quota fields (ADR 0010): decimal GB
// with one decimal place, Mbit/s for Slow, Gregorian dates.
import { bytes } from "./api";

const GB = 1e9;
/** Bytes per second in one Mbit/s. */
const MBIT = 125_000;

type Usage = { period_bytes: number; quota?: number | null };

/** "3.2 / 5 GB", or "3.2 GB · ∞" when the Quota is unlimited. */
export function periodText(u: Usage): string {
  if (u.quota == null) return `${bytes(u.period_bytes)} · ∞`;
  return `${(u.period_bytes / GB).toFixed(1)} / ${gbField(u.quota)} GB`;
}

/** Highest share of Quota used first; unlimited Users last, by bytes used. */
export function byQuotaShare(a: Usage & { handle: string }, b: Usage & { handle: string }): number {
  const share = (u: Usage) => (u.quota == null ? -1 : u.quota > 0 ? u.period_bytes / u.quota : Infinity);
  const [x, y] = [share(a), share(b)];
  if (x !== y) return y - x;
  if (x === -1 && a.period_bytes !== b.period_bytes) return b.period_bytes - a.period_bytes;
  return a.handle.localeCompare(b.handle);
}

/** A Quota as a GB field's value; empty is unlimited. */
export function gbField(n?: number | null): string {
  return n == null ? "" : String(Math.round(n / (GB / 10)) / 10);
}

/** A GB field's bytes: null when empty (unlimited), undefined when invalid. */
export function parseGB(s: string): number | null | undefined {
  if (s.trim() === "") return null;
  const v = Number(s);
  if (!Number.isFinite(v) || v < 0) return undefined;
  return Math.round(v * 10) * (GB / 10);
}

/** Bytes per second for a rate in Mbit/s. */
export function rateOf(mbit: number): number {
  return Math.round(mbit * MBIT);
}

/** A rate in bytes per second as Mbit/s. */
export function mbits(rate: number): string {
  return String(Math.round((rate / MBIT) * 10) / 10);
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** A day in the Gregorian calendar, in English, whatever the browser's
 * locale: "24 Nov 2026". Quota Periods start at 00:00 UTC. */
export function renews(iso: string): string {
  const d = new Date(iso);
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}
