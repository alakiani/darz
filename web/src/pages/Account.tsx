import { useEffect, useState } from "react";
import QRCode from "qrcode";
import { api, type Me, type TOTPOffer } from "../api/client";
import { fa } from "../fa";

export function AccountPage({ me, onChange, onSignedOut }: { me: Me; onChange: () => void; onSignedOut: () => void }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState("");
  const [offer, setOffer] = useState<TOTPOffer | null>(null);
  const [qr, setQr] = useState("");
  const [code, setCode] = useState("");
  useEffect(() => {
    if (offer) QRCode.toDataURL(offer.uri, { margin: 1, width: 220 }).then(setQr);
  }, [offer]);

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    const { response } = await api.POST("/v1/me/password", { body: { current, new: next } });
    setMsg(response.ok ? fa.passwordChanged : fa.error);
    if (response.ok) setCurrent(""), setNext("");
  };
  const startTOTP = async () => {
    const { data } = await api.POST("/v1/me/totp");
    if (data) setOffer(data);
  };
  const confirmTOTP = async (e: React.FormEvent) => {
    e.preventDefault();
    const path = me.totp_enabled ? "/v1/me/totp/disable" : "/v1/me/totp/confirm";
    const { data } = await api.POST(path, { body: { code } });
    if (!data) return setMsg(fa.wrongCode);
    setOffer(null);
    setCode("");
    setMsg("");
    onChange();
  };
  const signOut = async (everywhere: boolean) => {
    await api.POST(everywhere ? "/v1/me/logout-everywhere" : "/v1/logout");
    onSignedOut();
  };

  return (
    <section>
      <h1>
        {fa.account}: <span dir="ltr">{me.handle}</span>
      </h1>
      <form className="card" onSubmit={changePassword}>
        <h2>{fa.changePassword}</h2>
        <label>
          {fa.currentPassword}
          <input type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
        </label>
        <label>
          {fa.newPassword} <small>({fa.passwordHint})</small>
          <input type="password" autoComplete="new-password" required minLength={10} value={next} onChange={(e) => setNext(e.target.value)} />
        </label>
        <button type="submit">{fa.save}</button>
      </form>

      <div className="card">
        <h2>
          {fa.totp}: {me.totp_enabled ? fa.totpOn : fa.totpOff}
        </h2>
        {!me.totp_enabled && !offer && (
          <button type="button" onClick={startTOTP}>
            {fa.totpEnable}
          </button>
        )}
        {(offer || (me.totp_enabled && !me.admin)) && (
          <form onSubmit={confirmTOTP}>
            {offer && (
              <>
                <p>{fa.totpScan}</p>
                {qr && <img src={qr} alt="TOTP" />}
                <p dir="ltr" className="secret">
                  {offer.secret}
                </p>
              </>
            )}
            <label>
              {fa.totpCode}
              <input dir="ltr" inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} />
            </label>
            <button type="submit">{me.totp_enabled ? fa.totpDisable : fa.confirm}</button>
          </form>
        )}
      </div>

      {msg && <p className="notice">{msg}</p>}
      <div className="actions">
        <button type="button" onClick={() => signOut(false)}>
          {fa.signOut}
        </button>
        <button type="button" className="danger" onClick={() => signOut(true)}>
          {fa.signOutEverywhere}
        </button>
      </div>
    </section>
  );
}
