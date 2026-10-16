import { useEffect, useState } from "react";
import { api, errorOf, type InviteInfo, type Me } from "../api/client";
import { fa } from "../fa";

export function InvitePage({ token, onSignedIn }: { token: string; onSignedIn: (m: Me) => void }) {
  const [info, setInfo] = useState<InviteInfo | null | undefined>();
  const [handle, setHandle] = useState("");
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    api.POST("/v1/invites/lookup", { body: { token } }).then(({ data }) => setInfo(data ?? null));
  }, [token]);

  if (info === undefined) return null;
  if (info === null) return <p className="notice">{fa.inviteInvalid}</p>;
  const recovery = info.kind === "recovery";

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (password !== again) return setError(fa.passwordsDiffer);
    const { data, error, response } = await api.POST("/v1/invites/accept", { body: { token, handle, password } });
    if (data) return onSignedIn(data);
    setError(response.status === 409 ? fa.handleTaken : (errorOf(error) ?? fa.error));
  };

  return (
    <form className="card" onSubmit={submit}>
      <h1>{fa.inviteTitle}</h1>
      <p>{recovery ? fa.inviteRecovery(info.handle ?? "") : fa.inviteNew}</p>
      {!recovery && (
        <label>
          {fa.handle}
          <input name="handle" dir="ltr" autoComplete="username" required value={handle} onChange={(e) => setHandle(e.target.value.toLowerCase())} />
        </label>
      )}
      <label>
        {fa.password} <small>({fa.passwordHint})</small>
        <input name="password" type="password" autoComplete="new-password" required minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      <label>
        {fa.passwordAgain}
        <input name="password-again" type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
      </label>
      {error && <p className="error">{error}</p>}
      <button type="submit">{recovery ? fa.recoveryAccept : fa.inviteAccept}</button>
    </form>
  );
}
