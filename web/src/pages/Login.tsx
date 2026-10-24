import { useState } from "react";
import { Alert, Button, Paper, PasswordInput, Stack, TextInput, Title } from "@mantine/core";
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
    <Paper component="form" className="narrow" p="xl" shadow="sm" onSubmit={submit}>
      <Stack>
        <Title order={1} size="h2">
          {fa.signIn}
        </Title>
        <TextInput label={fa.handle} name="handle" dir="ltr" autoComplete="username" required value={handle} onChange={(e) => setHandle(e.target.value.toLowerCase())} />
        <PasswordInput label={fa.password} name="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {needCode && (
          <TextInput label={fa.totpCode} name="code" dir="ltr" inputMode="numeric" autoComplete="one-time-code" required autoFocus value={code} onChange={(e) => setCode(e.target.value)} />
        )}
        {error && (
          <Alert color="red" variant="light">
            {error}
          </Alert>
        )}
        <Button type="submit" size="md" fullWidth>
          {fa.signIn}
        </Button>
      </Stack>
    </Paper>
  );
}
