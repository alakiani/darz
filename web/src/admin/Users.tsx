import { useEffect, useState } from "react";
import { api, type Me } from "../api/client";
import { bytes, typedConfirm, type UserInfo } from "./api";

// Users: Suspend/Resume, Device cap, recovery Invite, Delete with typed
// confirmation. Admins see totals per User, not Devices (issue #14).
export function UsersScreen({ self }: { self: Me }) {
  const [users, setUsers] = useState<UserInfo[]>([]);
  const [link, setLink] = useState("");
  const load = () => api.GET("/v1/admin/users").then(({ data }) => setUsers(data ?? []));
  useEffect(() => {
    load();
  }, []);
  const p = (id: string) => ({ params: { path: { id } } });

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
            <th>This month</th>
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
              <td>{bytes(u.month_bytes)}</td>
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
