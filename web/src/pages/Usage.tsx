// The User's Usage by Quota Period, newest first (ADR 0010). Periods
// within the last ~90 days open into each live Device plus one Revoked
// Devices row; older ones have only their stored total, so period totals
// and calendar months are never mixed.
import { Fragment, useEffect, useState } from "react";
import { api, type MyUsage, type PeriodUsage } from "../api/client";
import { fa } from "../fa";
import { bytes } from "./Device";
import { periodDay, remediesText } from "./Quota";

export function UsagePage() {
  const [usage, setUsage] = useState<MyUsage | null>(null);
  useEffect(() => {
    api.GET("/v1/me/usage").then(({ data }) => setUsage(data ?? null));
  }, []);
  if (!usage) return null;

  const periods = [usage.current, ...usage.history];
  return (
    <section>
      <h1>{fa.usageTitle}</h1>
      <table className="usage periods">
        <thead>
          <tr>
            <th>{fa.dates}</th>
            <th>{fa.used}</th>
            <th>{fa.quota}</th>
            <th>{fa.remedies}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {periods.map((p, i) => (
            <Period key={p.start} period={p} quota={i === 0 ? quotaText(usage) : ""} current={i === 0} />
          ))}
        </tbody>
      </table>
      {usage.history.some((p) => !p.detailed) && <p className="hint">{fa.olderPeriods}</p>}
      <a href="#/">{fa.back}</a>
    </section>
  );
}

function quotaText(u: MyUsage): string {
  return u.quota == null ? fa.unlimited : bytes(u.quota);
}

// Past periods carry no Quota: what applied then isn't stored, so their
// Quota cell stays empty until that is settled (#81).
function Period({ period: p, quota, current }: { period: PeriodUsage; quota: string; current: boolean }) {
  const [open, setOpen] = useState(false);
  // A period runs up to, not including, its end: show the last day it covers.
  const last = Date.parse(p.end) - 1;
  return (
    <Fragment>
      <tr data-testid="period" className={current ? "current" : undefined}>
        <td>
          {fa.periodRange(periodDay(p.start), periodDay(last))}
          {current && <small className="hint"> · {fa.currentPeriod}</small>}
        </td>
        <td>{bytes(p.bytes)}</td>
        <td>{quota}</td>
        <td>{remediesText(p)}</td>
        <td>
          {p.detailed && (
            <button type="button" className="link" aria-expanded={open} onClick={() => setOpen(!open)}>
              {fa.byDevice}
            </button>
          )}
        </td>
      </tr>
      {open &&
        (p.devices ?? []).map((d) => (
          <tr key={d.id} className="sub" data-testid="period-device">
            <td>{d.name}</td>
            <td>{bytes(d.bytes)}</td>
            <td colSpan={3} />
          </tr>
        ))}
      {open && p.revoked > 0 && (
        <tr className="sub" data-testid="period-revoked">
          <td>{fa.revokedDevices}</td>
          <td>{bytes(p.revoked)}</td>
          <td colSpan={3} />
        </tr>
      )}
    </Fragment>
  );
}
