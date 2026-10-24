import { describe, expect, it } from "vitest";
import type { MyUsage } from "../api/client";
import { remediesText, standing } from "./Quota";

// The API sends null for an unlimited Quota, which the generated types
// leave out.
const usage = (quota: number | null, used: number, left: number | null, remedies: MyUsage["current"]["remedies"] = []): MyUsage => ({
  quota: quota as number,
  left: left as number,
  running_out: "block",
  renews_at: "2026-11-14T00:00:00Z",
  current: { start: "2026-10-14T00:00:00Z", end: "2026-11-14T00:00:00Z", bytes: used, remedies, detailed: true, revoked: 0 },
  history: [],
  refills: [],
  refill_adds: quota == null ? (null as unknown as number) : 0,
});

describe("standing", () => {
  it("counts Top-ups and Resets in what may be used", () => {
    const reset = { kind: "reset", bytes: 5e9, at: "2026-10-20T00:00:00Z" };
    expect(standing(usage(5e9, 5e9, 5e9, [reset]))).toEqual({ state: "ok", used: 5e9, may: 10e9, percent: 50 });
  });

  it("warns from 80%, and is out only when nothing is left", () => {
    expect(standing(usage(10e9, 7.99e9, 2.01e9)).state).toBe("ok");
    expect(standing(usage(10e9, 8e9, 2e9)).state).toBe("warn");
    expect(standing(usage(10e9, 9.999e9, 1e6))).toMatchObject({ state: "warn", percent: 99 });
    expect(standing(usage(5e9, 6e9, 0))).toMatchObject({ state: "out", percent: 100 });
  });

  it("has no share of an unlimited Quota", () => {
    expect(standing(usage(null, 2e9, null))).toMatchObject({ state: "unlimited", used: 2e9 });
  });
});

describe("remediesText", () => {
  it("sums each kind", () => {
    const p = usage(5e9, 0, 5e9, [
      { kind: "top-up", bytes: 1e9, at: "2026-10-15T00:00:00Z" },
      { kind: "reset", bytes: 2e9, at: "2026-10-16T00:00:00Z" },
      { kind: "top-up", bytes: 1e9, at: "2026-10-17T00:00:00Z" },
    ]).current;
    expect(remediesText(p)).toBe("+ ۲ GB افزوده، + ۲ GB ریست");
  });
});
