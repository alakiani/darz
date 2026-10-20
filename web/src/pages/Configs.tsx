import { useEffect, useMemo, useState } from "react";
import QRCode from "qrcode";
import type { IssuedDevice } from "../api/client";
import { configFiles } from "../lib/config";
import { makeZip } from "../lib/zip";
import { fa } from "../fa";

function download(name: string, data: BlobPart, type: string) {
  const url = URL.createObjectURL(new Blob([data], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** The configs of a freshly issued Device, shown once: one QR code each,
 * and the files. The private key exists only in this page's memory. */
export function Configs({ issued, privateKey, onSaved }: { issued: IssuedDevice; privateKey: string; onSaved: () => void }) {
  const files = useMemo(() => configFiles(issued.config, privateKey), [issued, privateKey]);
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
        onClick={() => download(`ways-${issued.device.name.replace(/[^\w-]/g, "") || "device"}.zip`, makeZip(files.map((f) => ({ name: f.name, data: f.text }))), "application/zip")}
      >
        {fa.downloadZip}
      </button>
      <div className="grid">
        {files.map((f, i) => (
          <figure key={f.name}>
            <figcaption>{fa.labels[f.label] ?? f.label}</figcaption>
            {qrs[i] && <img src={qrs[i]} alt={f.name} />}
            <button type="button" className="link" onClick={() => download(f.name, f.text, "text/plain")}>
              {fa.download} <span dir="ltr">{f.name}</span>
            </button>
          </figure>
        ))}
      </div>
      <p className="hint">{fa.iphoneHint}</p>
      <p className="hint">{fa.backupHint}</p>
      <button type="button" onClick={onSaved}>
        {fa.saved}
      </button>
    </section>
  );
}
