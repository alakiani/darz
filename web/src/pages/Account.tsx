import { useEffect, useState } from "react";
import QRCode from "qrcode";
import { Alert, Badge, Button, Group, Paper, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { IconLogout, IconShieldLock } from "@tabler/icons-react";
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
    <Stack gap="lg">
      <Title order={1} size="h2">
        {fa.account}: <span dir="ltr">{me.handle}</span>
      </Title>
      <Paper component="form" p="lg" onSubmit={changePassword}>
        <Stack>
          <Title order={2} size="h4">
            {fa.changePassword}
          </Title>
          <PasswordInput label={fa.currentPassword} autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
          <PasswordInput
            label={fa.newPassword}
            description={fa.passwordHint}
            autoComplete="new-password"
            required
            minLength={10}
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
          <Group>
            <Button type="submit">{fa.save}</Button>
          </Group>
        </Stack>
      </Paper>

      <Paper p="lg">
        <Stack>
          <Group justify="space-between">
            <Group gap="xs">
              <IconShieldLock size={20} />
              <Title order={2} size="h4">
                {fa.totp}
              </Title>
            </Group>
            <Badge color={me.totp_enabled ? "green" : "gray"} variant="light">
              {me.totp_enabled ? fa.totpOn : fa.totpOff}
            </Badge>
          </Group>
          {!me.totp_enabled && !offer && (
            <Group>
              <Button variant="light" onClick={startTOTP}>
                {fa.totpEnable}
              </Button>
            </Group>
          )}
          {(offer || (me.totp_enabled && !me.admin)) && (
            <Stack component="form" onSubmit={confirmTOTP}>
              {offer && (
                <>
                  <Text fz="sm">{fa.totpScan}</Text>
                  <figure className="qr">{qr && <img src={qr} alt="TOTP" />}</figure>
                  <Text dir="ltr" ta="center" fz="sm" className="secret">
                    {offer.secret}
                  </Text>
                </>
              )}
              <TextInput label={fa.totpCode} dir="ltr" inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} />
              <Group>
                <Button type="submit" color={me.totp_enabled ? "red" : undefined} variant={me.totp_enabled ? "light" : "filled"}>
                  {me.totp_enabled ? fa.totpDisable : fa.confirm}
                </Button>
              </Group>
            </Stack>
          )}
        </Stack>
      </Paper>

      {msg && <Alert variant="light">{msg}</Alert>}
      <Group>
        <Button variant="default" leftSection={<IconLogout size={18} />} onClick={() => signOut(false)}>
          {fa.signOut}
        </Button>
        <Button color="red" variant="light" onClick={() => signOut(true)}>
          {fa.signOutEverywhere}
        </Button>
      </Group>
    </Stack>
  );
}
