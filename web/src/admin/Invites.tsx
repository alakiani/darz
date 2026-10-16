import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { InviteRecord } from "./api";

// Invites: create with a cap, list, withdraw (issue #14).
export function InvitesScreen() {
  const [list, setList] = useState<InviteRecord[]>([]);
  const [cap, setCap] = useState(3);
  const [admin, setAdmin] = useState(false);
  const [url, setUrl] = useState("");
  const load = () => api.GET("/v1/admin/invites").then(({ data }) => setList(data ?? []));
  useEffect(() => {
    load();
  }, []);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    const { data } = await api.POST("/v1/admin/invites", { body: { device_cap: cap, admin } });
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
