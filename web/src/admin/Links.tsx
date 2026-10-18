import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { LinkTile, TransportList } from "./api";

// Links: read-only health, plus the editable candidate Transport list
// (ADR 0007).
export function LinksScreen() {
  const [links, setLinks] = useState<LinkTile[]>([]);
  const [tl, setTl] = useState<TransportList | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.GET("/v1/admin/links").then(({ data }) => setLinks(data ?? []));
    api.GET("/v1/admin/transports").then(({ data }) => setTl(data ?? null));
  }, []);

  const toggle = async (t: string, on: boolean) => {
    if (!tl) return;
    const before = tl;
    const enabled = tl.known.filter((k) => (k === t ? on : tl.enabled.includes(k)));
    setTl({ ...tl, enabled }); // show the change at once; undo it if refused
    const { data, error } = await api.POST("/v1/admin/transports", { body: { enabled, known: tl.known } });
    setError(data ? "" : ((error as { error?: string })?.error ?? "failed"));
    setTl(data ?? before);
  };

  return (
    <section>
      <h1>Links</h1>
      <table className="list" data-testid="links">
        <thead>
          <tr>
            <th>Link</th>
            <th>Crosses the filter</th>
            <th>Health</th>
            <th>Instances</th>
          </tr>
        </thead>
        <tbody>
          {links.map((l) => (
            <tr key={l.id}>
              <td>
                {l.a} – {l.b}
              </td>
              <td>{l.crosses ? "yes" : "no"}</td>
              <td className="status">{l.up_a || l.up_b ? `up, ${l.transport}, ${l.rtt_ms.toFixed(0)} ms` : "down"}</td>
              <td>{l.instances.map((i) => `${i.transport}${i.transport === "gre" ? "" : `:${i.listen_port || "?"}`}${i.up ? " ✓" : ""}`).join(", ")}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {tl && (
        <div className="card">
          <h2>Candidate Transports</h2>
          <p className="muted">Off means no Link uses it. AmneziaWG is the only Transport across the filter and stays on.</p>
          {tl.known.map((t) => (
            <label key={t} className="check">
              <input type="checkbox" name={`transport-${t}`} checked={tl.enabled.includes(t)} onChange={(e) => toggle(t, e.target.checked)} /> {t}
            </label>
          ))}
          {error && <p className="error">{error}</p>}
        </div>
      )}
    </section>
  );
}
