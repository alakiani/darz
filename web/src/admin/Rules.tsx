import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { RuleInfo } from "./api";

// Rules: both shown; only the IR Rule's Tags and fallback change; the
// default Rule is locked (ADR 0004).
export function RulesScreen() {
  const [rules, setRules] = useState<RuleInfo[]>([]);
  useEffect(() => {
    api.GET("/v1/admin/rules").then(({ data }) => setRules(data ?? []));
  }, []);
  return (
    <section>
      <h1>Rules</h1>
      {rules.map((r) => (r.builtin ? <Locked key={r.id} rule={r} /> : <Editable key={r.id} rule={r} onSaved={setRules} />))}
    </section>
  );
}

function Locked({ rule }: { rule: RuleInfo }) {
  return (
    <div className="card" data-testid={`rule-${rule.name}`}>
      <h2>
        {rule.name} <small>locked</small>
      </h2>
      <p>
        {rule.set} → an Exit with {rule.required_tags.join(", ")}; otherwise {rule.fallback}.
      </p>
    </div>
  );
}

function Editable({ rule, onSaved }: { rule: RuleInfo; onSaved: (r: RuleInfo[]) => void }) {
  const [tags, setTags] = useState(rule.required_tags.join(", "));
  const [fallback, setFallback] = useState(rule.fallback);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const required_tags = tags.split(/[\s,]+/).filter(Boolean);
    const { data } = await api.POST("/v1/admin/rules/{id}", { params: { path: { id: String(rule.id) } }, body: { required_tags, fallback } });
    if (data) onSaved(data);
  };
  return (
    <form className="card" onSubmit={save} data-testid={`rule-${rule.name}`}>
      <h2>{rule.name}</h2>
      <p>
        {rule.set} → an Exit with these Tags; otherwise <span className="status">{rule.fallback}</span>.
      </p>
      <label>
        Required Tags ("!X" negates) <input name="tags" value={tags} onChange={(e) => setTags(e.target.value)} />
      </label>
      <label>
        Fallback{" "}
        <select name="fallback" value={fallback} onChange={(e) => setFallback(e.target.value as RuleInfo["fallback"])}>
          <option value="next">next Rule</option>
          <option value="drop">drop</option>
        </select>
      </label>
      <button type="submit">Save</button>
    </form>
  );
}
