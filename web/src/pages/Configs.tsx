import { useEffect, useMemo, useState } from "react";
import QRCode from "qrcode";
import type { IssuedDevice } from "../api/client";
import { configFiles } from "../lib/config";
import { canShareFile, download, saveFile } from "../lib/save";
import { makeZip } from "../lib/zip";
import { fa } from "../fa";

/** The configs of a freshly issued Device, shown once: one QR code each,
 * and the files. The private key exists only in this page's memory. */
export function Configs({ issued, privateKey, onSaved }: { issued: IssuedDevice; privateKey: string; onSaved: () => void }) {
  const files = useMemo(
    () => configFiles(issued.config, privateKey).map((f) => ({ ...f, file: new File([f.text], f.name, { type: "text/plain" }) })),
    [issued, privateKey],
  );
  const [qrs, setQrs] = useState<string[]>([]);
  useEffect(() => {
    Promise.all(files.map((f) => QRCode.toDataURL(f.text, { errorCorrectionLevel: "M", margin: 1, width: 280 }))).then(setQrs);
  }, [files]);

  return (
    <section className="configs">
      <h1>
        {fa.configsTitle}: {issued.device.name}
      </h1>
      <p className="warning">{fa.configsOnce}</p>
      <button
        type="button"
        data-testid="download-zip"
        onClick={() =>
          download(
            new File([makeZip(files.map((f) => ({ name: f.name, data: f.text })))], `darz-${issued.device.name.replace(/[^\w-]/g, "") || "device"}.zip`, {
              type: "application/zip",
            }),
          )
        }
      >
        {fa.downloadZip}
      </button>
      <div className="grid">
        {files.map((f, i) => (
          <figure key={f.name}>
            <figcaption>{fa.labels[f.label] ?? f.label}</figcaption>
            {qrs[i] && <img src={qrs[i]} alt={f.name} />}
            <button type="button" className="link" onClick={() => saveFile(f.file)}>
              {canShareFile(f.file) ? fa.openInWireGuard : fa.download} <span dir="ltr">{f.name}</span>
            </button>
          </figure>
        ))}
      </div>
      <p className="hint">{fa.backupHint}</p>
      <button type="button" onClick={onSaved}>
        {fa.saved}
      </button>
    </section>
  );
}
