import { useEffect, useMemo, useState } from "react";
import QRCode from "qrcode";
import { Alert, Button, Paper, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle, IconCheck, IconDownload } from "@tabler/icons-react";
import type { IssuedDevice } from "../api/client";
import { configFile } from "../lib/config";
import { fa } from "../fa";

function download(name: string, data: BlobPart, type: string) {
  const url = URL.createObjectURL(new Blob([data], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** The config of a freshly issued Device, shown once: a QR code and the
 * file. The private key exists only in this page's memory. */
export function Configs({ issued, privateKey, onSaved }: { issued: IssuedDevice; privateKey: string; onSaved: () => void }) {
  const file = useMemo(() => configFile(issued.config, privateKey), [issued, privateKey]);
  const [qr, setQr] = useState("");
  useEffect(() => {
    QRCode.toDataURL(file.text, { errorCorrectionLevel: "M", margin: 1, width: 280 }).then(setQr);
  }, [file]);

  return (
    <Stack>
      <Title order={1} size="h2">
        {fa.configsTitle}: {issued.device.name}
      </Title>
      <Alert color="yellow" icon={<IconAlertTriangle />}>
        {fa.configsOnce}
      </Alert>
      <Paper p="lg">
        <figure className="qr">
          {qr && <img src={qr} alt={file.name} />}
          <Button
            variant="light"
            leftSection={<IconDownload size={18} />}
            data-testid="download-conf"
            onClick={() => download(file.name, file.text, "text/plain")}
          >
            {fa.download}&nbsp;<span dir="ltr">{file.name}</span>
          </Button>
        </figure>
      </Paper>
      <Text c="dimmed" fz="sm">
        {fa.iphoneHint}
      </Text>
      <Text c="dimmed" fz="sm">
        {fa.stuckHint}
      </Text>
      <Button size="md" leftSection={<IconCheck size={18} />} onClick={onSaved}>
        {fa.saved}
      </Button>
    </Stack>
  );
}
