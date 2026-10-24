import { useCallback, useEffect, useState } from "react";
import { Alert, Anchor, Button, NavLink, Paper, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import {
  IconArrowsShuffle,
  IconChartLine,
  IconExternalLink,
  IconGauge,
  IconKey,
  IconListDetails,
  IconLogout,
  IconMail,
  IconRoute,
  IconServer2,
  IconUsers,
  IconWorld,
  type Icon,
} from "@tabler/icons-react";
import { api, type Me } from "../api/client";
import { go } from "../router";
import { OverviewScreen } from "./Overview";
import { UsersScreen } from "./Users";
import { InvitesScreen } from "./Invites";
import { NodesScreen, NodeScreen } from "./Nodes";
import { LinksScreen } from "./Links";
import { RulesScreen } from "./Rules";
import { SetsScreen } from "./Sets";
import { TokensScreen } from "./Tokens";

// The admin panel is in English, left to right (issue #14).
const screens: [string, string, Icon][] = [
  ["/admin", "Overview", IconGauge],
  ["/admin/users", "Users", IconUsers],
  ["/admin/invites", "Invites", IconMail],
  ["/admin/nodes", "Nodes", IconServer2],
  ["/admin/links", "Links", IconArrowsShuffle],
  ["/admin/rules", "Rules", IconListDetails],
  ["/admin/sets", "Destination Sets", IconWorld],
  ["/admin/tokens", "API tokens", IconKey],
];

export function AdminApp({ route }: { route: string }) {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const refresh = useCallback(async () => {
    const { data } = await api.GET("/v1/me");
    setMe(data ?? null);
  }, []);
  useEffect(() => {
    refresh();
    const html = document.documentElement;
    html.dir = "ltr";
    html.lang = "en";
    return () => {
      html.dir = "rtl";
      html.lang = "fa";
    };
  }, [refresh]);

  if (me === undefined) return null;
  if (!me || !me.admin || !me.mfa) return <AdminLogin me={me} onSignedIn={setMe} />;

  let screen;
  if (route.startsWith("/admin/nodes/")) screen = <NodeScreen id={route.slice("/admin/nodes/".length)} />;
  else
    switch (route) {
      case "/admin/users":
        screen = <UsersScreen self={me} />;
        break;
      case "/admin/invites":
        screen = <InvitesScreen />;
        break;
      case "/admin/nodes":
        screen = <NodesScreen />;
        break;
      case "/admin/links":
        screen = <LinksScreen />;
        break;
      case "/admin/rules":
        screen = <RulesScreen />;
        break;
      case "/admin/sets":
        screen = <SetsScreen />;
        break;
      case "/admin/tokens":
        screen = <TokensScreen />;
        break;
      default:
        screen = <OverviewScreen />;
    }

  return (
    <div className="admin">
      <nav className="admin-side">
        <a href="#/admin" className="brand">
          <span className="brand-mark">
            <IconRoute size={18} stroke={1.8} />
          </span>
          <strong>WAYS admin</strong>
        </a>
        {screens.map(([path, label, I]) => (
          <NavLink
            key={path}
            href={`#${path}`}
            label={label}
            leftSection={<I size={18} />}
            active={route === path || (path === "/admin/nodes" && route.startsWith("/admin/nodes/"))}
            variant="light"
          />
        ))}
        <div className="spacer" />
        <NavLink
          href="/grafana/"
          target="_blank"
          rel="noopener"
          label="Grafana"
          leftSection={<IconChartLine size={18} />}
          rightSection={<IconExternalLink size={14} />}
        />
        <NavLink
          component="button"
          label="Sign out"
          leftSection={<IconLogout size={18} />}
          onClick={async () => {
            await api.POST("/v1/logout");
            setMe(null);
          }}
        />
      </nav>
      <main className="admin-main">{screen}</main>
    </div>
  );
}

function AdminLogin({ me, onSignedIn }: { me: Me | null; onSignedIn: (m: Me) => void }) {
  const [handle, setHandle] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [error, setError] = useState("");
  if (me && !me.admin) {
    return (
      <Alert className="narrow" color="yellow">
        This account is not an Admin. <a href="#/">Back to the panel</a>
      </Alert>
    );
  }
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const { data, error, response } = await api.POST("/v1/login", { body: { handle, password, totp_code: needCode ? code : undefined } });
    if (data?.admin && !data.mfa) {
      setError("Admins need TOTP: turn it on under Account in the user panel, then sign in again.");
      return;
    }
    if (data) return onSignedIn(data);
    if ((error as { error?: string })?.error === "totp_required") return setNeedCode(true);
    setError(response.status === 429 ? "Too many failed sign-ins; try later." : "Wrong handle, password or code.");
  };
  return (
    <Paper component="form" className="narrow" p="xl" shadow="sm" onSubmit={submit}>
      <Stack>
        <div className="brand">
          <span className="brand-mark">
            <IconRoute size={22} stroke={1.8} />
          </span>
          <Title order={1} size="h3">
            WAYS admin
          </Title>
        </div>
        <TextInput label="Handle" name="handle" autoComplete="username" value={handle} onChange={(e) => setHandle(e.target.value)} />
        <PasswordInput label="Password" name="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        {needCode && (
          <TextInput label="TOTP code" name="code" inputMode="numeric" autoComplete="one-time-code" autoFocus value={code} onChange={(e) => setCode(e.target.value)} />
        )}
        {error && (
          <Alert color="red" variant="light">
            {error}
          </Alert>
        )}
        <Button type="submit" fullWidth>
          Sign in
        </Button>
        <Text fz="sm" ta="center">
          <Anchor href="#/" onClick={() => go("/")}>
            User panel
          </Anchor>
        </Text>
      </Stack>
    </Paper>
  );
}
