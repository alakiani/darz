import { useState } from "react";
import { api, errorOf, type Me } from "../api/client";
import { fa } from "../fa";

export function LoginPage({ onSignedIn }: { onSignedIn: (m: Me) => void }) {
  const [handle, setHandle] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const { data, error, response } = await api.POST("/v1/login", {
      body: { handle, password, totp_code: needCode ? code : undefined },
    });
    if (data) return onSignedIn(data);
    if (errorOf(error) === "totp_required") return setNeedCode(true);
    if (response.status === 429) return setError(fa.tooMany);
    setError(needCode ? fa.wrongCode : fa.wrongCredentials);
  };

  return (
    <form className="card" onSubmit={submit}>
      <h1>{fa.signIn}</h1>
      <label>
        {fa.handle}
        <input name="handle" dir="ltr" autoComplete="username" required value={handle} onChange={(e) => setHandle(e.target.value.toLowerCase())} />
      </label>
      <label>
        {fa.password}
        <input name="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      {needCode && (
        <label>
          {fa.totpCode}
          <input name="code" dir="ltr" inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
      )}
      {error && <p className="error">{error}</p>}
      <button type="submit">{fa.signIn}</button>
    </form>
  );
}
