import { useEffect, useState } from "react";
import { api, type Me } from "../api/client";
import { typedConfirm, type UserInfo } from "./api";
import { byQuotaShare, gbField, mbits, parseGB, periodText, rateOf, renews } from "./quota";

// Users: Suspend/Resume, Device cap, recovery Invite, Delete with typed
// confirmation. Admins see totals per User, not Devices (issue #14). Each
// User's Quota and running-out setting are edited inline, and the remedies
// are actions (ADR 0010); the most used share of Quota comes first.
export function UsersScreen({ self }: { self: Me }) {
  const [users, setUsers] = useState<UserInfo[]>([]);
  const [link, setLink] = useState("");
  const load = () => api.GET("/v1/admin/users").then(({ data }) => setUsers((data ?? []).sort(byQuotaShare)));
  useEffect(() => {
    load();
  }, []);
  const p = (id: string) => ({ params: { path: { id } } });
  // Saves a User's Quota and running-out setting with one of them changed.
  // Slow without a rate yet starts at 1 Mbit/s.
  const setQuota = async (u: UserInfo, change: { quota?: number | null; running_out?: string; rate?: number }) => {
    const quota = "quota" in change ? change.quota : u.quota;
    const running_out = change.running_out ?? u.running_out;
    const rate = running_out === "slow" ? (change.rate ?? (u.rate || rateOf(1))) : undefined;
    // An absent quota is unlimited.
    await api.POST("/v1/admin/users/{id}/quota", { ...p(u.id), body: { quota: quota ?? undefined, running_out, rate } });
    load();
  };

  return (
    <section>
      <h1>Users</h1>
      {link && (
        <p className="notice" data-testid="recovery-link">
          Recovery Invite (send it out of band): <code>{link}</code>
        </p>
      )}
      <table className="list">
        <thead>
          <tr>
            <th>Handle</th>
            <th>Status</th>
            <th>Devices</th>
            <th>This period</th>
            <th>Quota</th>
            <th>Running out</th>
            <th>Renews</th>
            <th>Refills held</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id} data-testid={`user-${u.handle}`}>
              <td>
                {u.handle}
                {u.admin && " (Admin)"}
                {u.totp_enabled && " · TOTP"}
              </td>
              <td className="status">{u.suspended ? "Suspended" : "Active"}</td>
              <td>
                {u.device_count} of{" "}
                <input
                  className="cap"
                  type="number"
                  min={0}
                  max={100}
                  defaultValue={u.device_cap}
                  aria-label="Device cap"
                  onBlur={async (e) => {
                    const cap = Number(e.target.value);
                    if (cap !== u.device_cap) {
                      await api.POST("/v1/admin/users/{id}/device-cap", { ...p(u.id), body: { device_cap: cap } });
                      load();
                    }
                  }}
                />
              </td>
              <td className="period">{periodText(u)}</td>
              <td>
                <input
                  className="cap"
                  type="number"
                  min={0}
                  step={0.1}
                  placeholder="∞"
                  defaultValue={gbField(u.quota)}
                  aria-label="Quota"
                  title="GB per Quota Period; empty is unlimited"
                  onBlur={async (e) => {
                    const quota = parseGB(e.target.value);
                    if (quota === undefined) e.target.value = gbField(u.quota);
                    else if (quota !== (u.quota ?? null)) await setQuota(u, { quota });
                  }}
                />{" "}
                GB
              </td>
              <td>
                <select
                  className="cap"
                  defaultValue={u.running_out}
                  aria-label="Running out"
                  onChange={(e) => setQuota(u, { running_out: e.target.value })}
                >
                  <option value="block">Block</option>
                  <option value="slow">Slow</option>
                </select>
                {u.running_out === "slow" && (
                  <>
                    {" "}
                    to{" "}
                    <input
                      className="cap"
                      type="number"
                      min={0.1}
                      step={0.1}
                      defaultValue={mbits(u.rate ?? 0)}
                      aria-label="Mbit/s"
                      onBlur={async (e) => {
                        const rate = rateOf(Number(e.target.value));
                        if (!(rate > 0)) e.target.value = mbits(u.rate ?? 0);
                        else if (rate !== u.rate) await setQuota(u, { rate });
                      }}
                    />{" "}
                    Mbit/s
                  </>
                )}
              </td>
              <td className="renews">{renews(u.renews_at)}</td>
              <td className="refills">{u.refills}</td>
              <td className="actions">
                {u.id !== self.id && (
                  <button
                    type="button"
                    onClick={async () => {
                      await api.POST(u.suspended ? "/v1/admin/users/{id}/resume" : "/v1/admin/users/{id}/suspend", p(u.id));
                      load();
                    }}
                  >
                    {u.suspended ? "Resume" : "Suspend"}
                  </button>
                )}
                <button
                  type="button"
                  onClick={async () => {
                    const count = Number(prompt(`How many Refills to give ${u.handle}?`, "1"));
                    if (!Number.isInteger(count) || count < 1) return;
                    const days = Number(prompt("Each Refill expires in how many days?", "30"));
                    if (!Number.isInteger(days) || days < 1) return;
                    await api.POST("/v1/admin/users/{id}/refills", { ...p(u.id), body: { count, days } });
                    load();
                  }}
                >
                  Give Refill
                </button>
                {u.quota != null && (
                  <button
                    type="button"
                    onClick={async () => {
                      if (!confirm(`Reset ${u.handle} to a full Quota now? Their Usage stays as it is.`)) return;
                      await api.POST("/v1/admin/users/{id}/reset", p(u.id));
                      load();
                    }}
                  >
                    Reset now
                  </button>
                )}
                <button
                  type="button"
                  onClick={async () => {
                    const n = parseGB(prompt(`GB to add to ${u.handle}'s Quota, for this Quota Period only`) ?? "");
                    if (!n) return;
                    await api.POST("/v1/admin/users/{id}/top-up", { ...p(u.id), body: { bytes: n } });
                    load();
                  }}
                >
                  Top-up
                </button>
                <button
                  type="button"
                  onClick={async () => {
                    const { data } = await api.POST("/v1/admin/users/{id}/recovery-invite", p(u.id));
                    if (data) setLink(data.url);
                  }}
                >
                  Recovery Invite
                </button>
                {u.id !== self.id && (
                  <button
                    type="button"
                    className="danger"
                    onClick={async () => {
                      if (!typedConfirm("delete this User, their Devices and their usage", u.handle)) return;
                      await api.DELETE("/v1/admin/users/{id}", p(u.id));
                      load();
                    }}
                  >
                    Delete
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
