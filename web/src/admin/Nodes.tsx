import { useEffect, useState } from "react";
import { api } from "../api/client";
import { go } from "../router";
import { ago, typedConfirm, type NodeDetail, type NodeTile } from "./api";

type Role = "bridge" | "exit";
const roleList: Role[] = ["bridge", "exit"];
const splitTags = (s: string) =>
  s
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter(Boolean);

function RoleBoxes({ roles, setRoles }: { roles: Role[]; setRoles: (r: Role[]) => void }) {
  return (
    <>
      {roleList.map((r) => (
        <label key={r} className="check">
          <input
            type="checkbox"
            name={`role-${r}`}
            checked={roles.includes(r)}
            onChange={(e) => setRoles(e.target.checked ? [...roles, r] : roles.filter((x) => x !== r))}
          />{" "}
          {r}
        </label>
      ))}
    </>
  );
}

// Nodes: add with the Ansible command, open, edit Roles and Tags, delete.
export function NodesScreen() {
  const [nodes, setNodes] = useState<NodeTile[]>([]);
  const [name, setName] = useState("");
  const [roles, setRoles] = useState<Role[]>(["exit"]);
  const [tags, setTags] = useState("");
  const [host, setHost] = useState("");
  const [command, setCommand] = useState("");
  const [error, setError] = useState("");
  const load = () => api.GET("/v1/admin/nodes").then(({ data }) => setNodes(data ?? []));
  useEffect(() => {
    load();
  }, []);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const { data, error } = await api.POST("/v1/admin/nodes", { body: { name, roles, tags: splitTags(tags), ssh_host: host } });
    if (!data) return setError((error as { error?: string })?.error ?? "failed");
    setCommand(data.command ?? "Already enrolled; nothing to run.");
    load();
  };

  return (
    <section>
      <h1>Nodes</h1>
      <table className="list">
        <thead>
          <tr>
            <th>Name</th>
            <th>Roles</th>
            <th>Tags</th>
            <th>Contact</th>
            <th>Snapshot</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => (
            <tr key={n.id} data-testid={`node-row-${n.name}`}>
              <td>
                <a href={`#/admin/nodes/${n.id}`}>{n.name}</a>
              </td>
              <td>{n.roles.join(" + ")}</td>
              <td>{n.tags.join(", ")}</td>
              <td>{n.enrolled ? ago(n.last_contact) : "not enrolled"}</td>
              <td>
                {n.applied_version}/{n.latest_version}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2>Add a Node</h2>
      <form className="card" onSubmit={add}>
        <label>
          Name <input name="name" required value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <div>
          Roles <RoleBoxes roles={roles} setRoles={setRoles} />
        </div>
        <label>
          Tags (such as IR) <input name="tags" value={tags} onChange={(e) => setTags(e.target.value)} />
        </label>
        <label>
          SSH host, for the command only <input name="host" required value={host} onChange={(e) => setHost(e.target.value)} />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit">Add Node</button>
      </form>
      {command && (
        <div className="notice">
          <p>Run this from the operator's laptop within the hour; the token works once:</p>
          <pre data-testid="enroll-command">{command}</pre>
        </div>
      )}
    </section>
  );
}

export function NodeScreen({ id }: { id: string }) {
  const [d, setD] = useState<NodeDetail | null>(null);
  const [roles, setRoles] = useState<Role[]>([]);
  const [tags, setTags] = useState("");
  const load = (form: boolean) =>
    api.GET("/v1/admin/nodes/{id}", { params: { path: { id } } }).then(({ data }) => {
      setD(data ?? null);
      if (data && form) {
        setRoles(data.node.roles as Role[]);
        setTags(data.node.tags.join(", "));
      }
    });
  useEffect(() => {
    load(true);
    // Contact, Links and Paths change by the second; the edit form does not.
    const t = setInterval(() => load(false), 5000);
    return () => clearInterval(t);
  }, [id]);
  if (!d) return null;
  const n = d.node;

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    await api.POST("/v1/admin/nodes/{id}", { params: { path: { id } }, body: { roles, tags: splitTags(tags) } });
    load(true);
  };
  const remove = async () => {
    if (!typedConfirm("delete this Node; its certificate stops working at once", n.name)) return;
    await api.DELETE("/v1/admin/nodes/{id}", { params: { path: { id } } });
    go("/admin/nodes");
  };

  return (
    <section>
      <h1 data-testid="node-name">{n.name}</h1>
      <p>
        {n.enrolled ? `contact ${ago(n.last_contact)}` : "not enrolled"} · snapshot {n.applied_version}/{n.latest_version}
        {n.agent_version && ` · agent ${n.agent_version}`}
      </p>
      <form className="card inline" onSubmit={save}>
        <RoleBoxes roles={roles} setRoles={setRoles} />
        <label>
          Tags <input name="tags" value={tags} onChange={(e) => setTags(e.target.value)} />
        </label>
        <button type="submit">Save</button>
      </form>

      {d.paths.length > 0 && (
        <>
          <h2>Paths</h2>
          <table className="list" data-testid="paths">
            <thead>
              <tr>
                <th>Rule</th>
                <th>Mode</th>
                <th>Eligible</th>
                <th>Buckets</th>
              </tr>
            </thead>
            <tbody>
              {d.paths.map((p) => (
                <tr key={p.rule}>
                  <td>{p.rule}</td>
                  <td>{p.mode}</td>
                  <td>{p.eligible}</td>
                  <td>
                    {p.shares.map((s) => `${s.candidate}${s.transit ? " (transit)" : ""}: ${s.buckets}`).join(", ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      <h2>Links</h2>
      <table className="list">
        <tbody>
          {d.links.map((l) => (
            <tr key={l.id}>
              <td>
                {l.a} – {l.b}
              </td>
              <td>{l.up_a || l.up_b ? `up, ${l.transport}, ${l.rtt_ms.toFixed(0)} ms` : "down"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <button type="button" className="danger" onClick={remove}>
        Delete Node
      </button>
    </section>
  );
}
