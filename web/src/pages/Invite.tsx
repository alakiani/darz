import { useEffect, useState } from "react";
import { Alert, Button, Paper, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
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
  if (info === null)
    return (
      <Alert className="narrow" color="yellow" icon={<IconAlertTriangle />}>
        {fa.inviteInvalid}
      </Alert>
    );
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
    <Paper component="form" className="narrow" p="xl" shadow="sm" onSubmit={submit}>
      <Stack>
        <Title order={1} size="h2">
          {fa.inviteTitle}
        </Title>
        <Text c="dimmed">{recovery ? fa.inviteRecovery(info.handle ?? "") : fa.inviteNew}</Text>
        {!recovery && (
          <TextInput label={fa.handle} name="handle" dir="ltr" autoComplete="username" required value={handle} onChange={(e) => setHandle(e.target.value.toLowerCase())} />
        )}
        <PasswordInput
          label={fa.password}
          description={fa.passwordHint}
          name="password"
          autoComplete="new-password"
          required
          minLength={10}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <PasswordInput label={fa.passwordAgain} name="password-again" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
        {error && (
          <Alert color="red" variant="light">
            {error}
          </Alert>
        )}
        <Button type="submit" size="md" fullWidth>
          {recovery ? fa.recoveryAccept : fa.inviteAccept}
        </Button>
      </Stack>
    </Paper>
  );
}
