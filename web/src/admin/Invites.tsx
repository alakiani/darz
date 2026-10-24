import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { InviteRecord } from "./api";
import { parseGB, presetsText, rateOf } from "./quota";

// Invites: create with a cap, list, withdraw (issue #14). A new User's
// Invite may preset their Quota, running-out setting and Refills (ADR 0010).
export function InvitesScreen() {
  const [list, setList] = useState<InviteRecord[]>([]);
  const [cap, setCap] = useState(3);
  const [admin, setAdmin] = useState(false);
  const [quota, setQuota] = useState(""); // GB; empty is unlimited
  const [runningOut, setRunningOut] = useState("block");
  const [mbit, setMbit] = useState(1);
  const [refills, setRefills] = useState(0);
  const [refillDays, setRefillDays] = useState(30);
  const [url, setUrl] = useState("");
  const load = () => api.GET("/v1/admin/invites").then(({ data }) => setList(data ?? []));
  useEffect(() => {
    load();
  }, []);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    const body = {
      device_cap: cap,
      admin,
      quota: parseGB(quota) ?? undefined, // absent is unlimited
      running_out: runningOut,
      rate: runningOut === "slow" ? rateOf(mbit) : undefined,
      refills,
      refill_days: refillDays,
    };
    const { data } = await api.POST("/v1/admin/invites", { body });
    if (data) setUrl(data.url);
    load();
  };

  return (
    <section>
      <h1>Invites</h1>
      <form className="card inline" onSubmit={create}>
        <label>
          Device cap <input name="cap" type="number" min={1} max={100} value={cap} onChange={(e) => setCap(Number(e.target.value))} />
        </label>
        <label>
          Quota (GB){" "}
          <input name="quota" type="number" min={0} step={0.1} placeholder="Unlimited" value={quota} onChange={(e) => setQuota(e.target.value)} />
        </label>
        <label>
          Running out
          <select name="running_out" value={runningOut} onChange={(e) => setRunningOut(e.target.value)}>
            <option value="block">Block</option>
            <option value="slow">Slow</option>
          </select>
        </label>
        {runningOut === "slow" && (
          <label>
            Mbit/s <input name="mbits" type="number" min={0.1} step={0.1} value={mbit} onChange={(e) => setMbit(Number(e.target.value))} />
          </label>
        )}
        <label>
          Refills <input name="refills" type="number" min={0} max={100} value={refills} onChange={(e) => setRefills(Number(e.target.value))} />
        </label>
        <label>
          Refill expiry (days){" "}
          <input name="refill_days" type="number" min={1} max={3650} value={refillDays} onChange={(e) => setRefillDays(Number(e.target.value))} />
        </label>
        <label className="check">
          <input name="admin" type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} /> Admin
        </label>
        <button type="submit">Create Invite</button>
      </form>
      {url && (
        <p className="notice" data-testid="invite-url">
          Single-use, valid 7 days. Send it out of band: <code>{url}</code>
        </p>
      )}
      <table className="list">
        <thead>
          <tr>
            <th>Created</th>
            <th>Kind</th>
            <th>For</th>
            <th>Presets</th>
            <th>Status</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {list.map((i) => (
            <tr key={i.id} data-testid={`invite-${i.status}`}>
              <td>{new Date(i.created_at).toLocaleString()}</td>
              <td>
                {i.kind}
                {i.admin && " (Admin)"}
                {i.device_cap ? `, cap ${i.device_cap}` : ""}
              </td>
              <td>{i.handle}</td>
              <td className="presets">{presetsText(i)}</td>
              <td className="status">{i.status}</td>
              <td>
                {i.status === "open" && (
                  <button
                    type="button"
                    onClick={async () => {
                      await api.POST("/v1/admin/invites/{id}/withdraw", { params: { path: { id: i.id } } });
                      load();
                    }}
                  >
                    Withdraw
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
