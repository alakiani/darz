import { useEffect, useState } from "react";
import { api, type DeviceInfo, type IssuedDevice } from "../api/client";
import { generateKeyPair } from "../lib/keys";
import { fa } from "../fa";
import { go } from "../router";
import { Configs } from "./Configs";
import { stateLabel } from "./Home";

export function DevicePage({ id, onChange }: { id: string; onChange: () => void }) {
  const [device, setDevice] = useState<DeviceInfo | null>(null);
  const [issued, setIssued] = useState<{ device: IssuedDevice; privateKey: string } | null>(null);
  useEffect(() => {
    api.GET("/v1/me/devices").then(({ data }) => setDevice(data?.find((d) => d.id === id) ?? null));
  }, [id]);

  if (issued) {
    return (
      <Configs
        issued={issued.device}
        privateKey={issued.privateKey}
        onSaved={() => {
          setIssued(null);
          onChange();
          go("/");
        }}
      />
    );
  }
  if (!device) return null;

  const revoke = async () => {
    if (!confirm(fa.revokeConfirm)) return;
    await api.POST("/v1/me/devices/{id}/revoke", { params: { path: { id } } });
    onChange();
    go("/");
  };
  const replace = async () => {
    const keys = generateKeyPair();
    const { data } = await api.POST("/v1/me/devices/{id}/replace", {
      params: { path: { id } },
      body: { name: device.name, public_key: keys.publicKey },
    });
    if (data) setIssued({ device: data, privateKey: keys.privateKey });
  };

  return (
    <section>
      <h1>
        {fa.device}: {device.name} <span className={`state ${device.state}`}>{stateLabel(device)}</span>
      </h1>
      <dl>
        <dt>{fa.address}</dt>
        <dd dir="ltr">{device.address}</dd>
        <dt>{fa.created}</dt>
        <dd>{new Date(device.created_at).toLocaleDateString("fa-IR")}</dd>
      </dl>
      <h2>{fa.usage}</h2>
      <p className="muted">{fa.usageSoon}</p>
      <p className="hint">{fa.backupHint}</p>
      {!device.revoked && (
        <div className="actions">
          <p>{fa.replaceHint}</p>
          <button type="button" onClick={replace}>
            {fa.replace}
          </button>
          <button type="button" className="danger" onClick={revoke}>
            {fa.revoke}
          </button>
        </div>
      )}
      <a href="#/">{fa.back}</a>
    </section>
  );
}
