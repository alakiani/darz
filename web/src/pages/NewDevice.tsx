import { useState } from "react";
import { Alert, Button, Paper, Stack, TextInput, Title } from "@mantine/core";
import { api, errorOf, type IssuedDevice } from "../api/client";
import { generateKeyPair } from "../lib/keys";
import { fa } from "../fa";
import { go } from "../router";
import { Configs } from "./Configs";

export function NewDevicePage({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [issued, setIssued] = useState<{ device: IssuedDevice; privateKey: string } | null>(null);

  if (issued) {
    return (
      <Configs
        issued={issued.device}
        privateKey={issued.privateKey}
        onSaved={() => {
          setIssued(null); // drop the private key
          onDone();
          go("/");
        }}
      />
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const keys = generateKeyPair();
    const { data, error, response } = await api.POST("/v1/me/devices", { body: { name, public_key: keys.publicKey } });
    setBusy(false);
    if (data) return setIssued({ device: data, privateKey: keys.privateKey });
    setError(response.status === 409 ? fa.capReached : (errorOf(error) ?? fa.error));
  };

  return (
    <Paper component="form" p="xl" onSubmit={submit}>
      <Stack>
        <Title order={1} size="h2">
          {fa.newDevice}
        </Title>
        <TextInput label={fa.deviceName} name="name" required maxLength={64} autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        {error && (
          <Alert color="red" variant="light">
            {error}
          </Alert>
        )}
        <Button type="submit" size="md" loading={busy}>
          {busy ? fa.creating : fa.create}
        </Button>
      </Stack>
    </Paper>
  );
}
