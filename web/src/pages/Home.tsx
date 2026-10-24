import { useEffect, useState } from "react";
import { api, type DeviceInfo, type Me, type MyUsage } from "../api/client";
import { fa } from "../fa";
import { QuotaCard } from "./Quota";

export function stateLabel(d: DeviceInfo): string {
  if (d.revoked) return fa.revoked;
  return d.state === "active" ? fa.active : fa.blocked;
}

export function HomePage({ me }: { me: Me }) {
  const [devices, setDevices] = useState<DeviceInfo[]>([]);
  const [usage, setUsage] = useState<MyUsage | null>(null);
  useEffect(() => {
    api.GET("/v1/me/devices").then(({ data }) => setDevices(data ?? []));
    api.GET("/v1/me/usage").then(({ data }) => setUsage(data ?? null));
  }, []);
  const live = devices.filter((d) => !d.revoked);
  const full = me.device_count >= me.device_cap;

  return (
    <section>
      {usage && <QuotaCard usage={usage} onChange={setUsage} />}
      <div className="row">
        <h1>
          {fa.devices} <small>{fa.deviceCount(me.device_count, me.device_cap)}</small>
        </h1>
        {full ? (
          <p className="notice">{fa.capReached}</p>
        ) : (
          <a className="button" href="#/devices/new">
            {fa.newDevice}
          </a>
        )}
      </div>
      {live.length === 0 && <p>{fa.noDevices}</p>}
      <ul className="devices">
        {live.map((d) => (
          <li key={d.id}>
            <a href={`#/devices/${d.id}`}>
              <strong>{d.name}</strong>
              <span className={`state ${d.state}`}>
                {stateLabel(d)}
                {d.recent_bytes > 0 && <small className="recent"> · {fa.usedRecently}</small>}
              </span>
            </a>
          </li>
        ))}
      </ul>
    </section>
  );
}
