import { useEffect, useState } from "react";
import { api } from "../api/client";
import { ago, type SetInfo } from "./api";

// Destination Sets: feed status and gates, fetch now, roll back, Admin
// overrides (ADR 0004).
export function SetsScreen() {
  const [sets, setSets] = useState<SetInfo[]>([]);
  useEffect(() => {
    api.GET("/v1/admin/sets").then(({ data }) => setSets(data ?? []));
  }, []);
  return (
    <section>
      <h1>Destination Sets</h1>
      {sets.map((s) => (
        <SetCard key={s.id} set={s} onChange={setSets} />
      ))}
    </section>
  );
}

function SetCard({ set: s, onChange }: { set: SetInfo; onChange: (s: SetInfo[]) => void }) {
  const [adds, setAdds] = useState(s.additions.join("\n"));
  const [rems, setRems] = useState(s.removals.join("\n"));
  const [error, setError] = useState("");
  const p = { params: { path: { id: String(s.id) } } };
  const lines = (t: string) => t.split("\n").map((l) => l.trim()).filter(Boolean);
  const after = (data: SetInfo[] | undefined, error: unknown) => {
    setError(data ? "" : ((error as { error?: string })?.error ?? "failed"));
    if (data) onChange(data);
  };

  return (
    <div className="card" data-testid={`set-${s.name}`}>
      <h2>{s.name}</h2>
      <p>
        <span data-testid="prefixes">{s.prefixes}</span> prefixes, {s.addresses.toLocaleString()} addresses
      </p>
      {s.feed && (
        <>
          <p>
            Feed: updated {ago(s.updated_at)}, last fetch {ago(s.last_attempt)}
          </p>
          {s.last_error ? (
            <p className="error" data-testid="feed-error">
              Last update rejected by the gates; the last good set stays: {s.last_error}
            </p>
          ) : (
            <p className="muted">Gates: parses cleanly, not empty, within ±2% of the last good set.</p>
          )}
          <div className="actions">
            <button
              type="button"
              onClick={async () => {
                const { data, error } = await api.POST("/v1/admin/sets/{id}/fetch", p);
                after(data, error);
              }}
            >
              Fetch now
            </button>
            <button
              type="button"
              disabled={!s.can_roll_back}
              onClick={async () => {
                const { data, error } = await api.POST("/v1/admin/sets/{id}/rollback", p);
                after(data, error);
              }}
            >
              Roll back
            </button>
          </div>
        </>
      )}
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          const { data, error } = await api.POST("/v1/admin/sets/{id}/overrides", { ...p, body: { additions: lines(adds), removals: lines(rems) } });
          after(data, error);
        }}
      >
        <label>
          Additions, one prefix per line
          <textarea name="additions" rows={3} value={adds} onChange={(e) => setAdds(e.target.value)} />
        </label>
        <label>
          Removals, one prefix per line
          <textarea name="removals" rows={3} value={rems} onChange={(e) => setRems(e.target.value)} />
        </label>
        <button type="submit">Save overrides</button>
      </form>
      {error && <p className="error">{error}</p>}
    </div>
  );
}
