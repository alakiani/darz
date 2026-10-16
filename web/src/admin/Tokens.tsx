import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { TokenInfo } from "./api";

// API tokens: nodes:enroll only, for the operator's laptop (ADR 0002).
export function TokensScreen() {
  const [tokens, setTokens] = useState<TokenInfo[]>([]);
  const [name, setName] = useState("");
  const [secret, setSecret] = useState("");
  const load = () => api.GET("/v1/admin/tokens").then(({ data }) => setTokens(data ?? []));
  useEffect(() => {
    load();
  }, []);

  return (
    <section>
      <h1>API tokens</h1>
      <form
        className="card inline"
        onSubmit={async (e) => {
          e.preventDefault();
          const { data } = await api.POST("/v1/admin/tokens", { body: { name } });
          if (data) setSecret(data.secret);
          setName("");
          load();
        }}
      >
        <label>
          Name <input name="name" required value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <button type="submit">Make a nodes:enroll token</button>
      </form>
      {secret && (
        <p className="notice">
          Shown once; keep it in ansible-vault or the keychain. It lasts 90 days: <code data-testid="token-secret">{secret}</code>
        </p>
      )}
      <table className="list">
        <thead>
          <tr>
            <th>Name</th>
            <th>Scope</th>
            <th>Expires</th>
            <th>Status</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {tokens.map((t) => (
            <tr key={t.id} data-testid={`token-${t.name}`}>
              <td>{t.name}</td>
              <td>{t.scope}</td>
              <td>{new Date(t.expires_at).toLocaleDateString()}</td>
              <td className="status">{t.revoked ? "revoked" : "active"}</td>
              <td>
                {!t.revoked && (
                  <button
                    type="button"
                    onClick={async () => {
                      await api.POST("/v1/admin/tokens/{id}/revoke", { params: { path: { id: t.id } } });
                      load();
                    }}
                  >
                    Revoke
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
