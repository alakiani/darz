import { useCallback, useEffect, useState } from "react";
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
const screens: [string, string][] = [
  ["/admin", "Overview"],
  ["/admin/users", "Users"],
  ["/admin/invites", "Invites"],
  ["/admin/nodes", "Nodes"],
  ["/admin/links", "Links"],
  ["/admin/rules", "Rules"],
  ["/admin/sets", "Destination Sets"],
  ["/admin/tokens", "API tokens"],
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
      <nav className="admin-nav">
        <strong>WAYS admin</strong>
        {screens.map(([path, label]) => (
          <a key={path} href={`#${path}`} className={route === path ? "current" : ""}>
            {label}
          </a>
        ))}
        <a href="/grafana/" target="_blank" rel="noopener">
          Grafana
        </a>
        <button
          type="button"
          className="link"
          onClick={async () => {
            await api.POST("/v1/logout");
            setMe(null);
          }}
        >
          Sign out
        </button>
      </nav>
      <main>{screen}</main>
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
      <p className="notice">
        This account is not an Admin. <a href="#/">Back to the panel</a>
      </p>
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
    <form className="card admin-login" onSubmit={submit}>
      <h1>WAYS admin</h1>
      <label>
        Handle
        <input name="handle" autoComplete="username" value={handle} onChange={(e) => setHandle(e.target.value)} />
      </label>
      <label>
        Password
        <input name="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      {needCode && (
        <label>
          TOTP code
          <input name="code" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
      )}
      {error && <p className="error">{error}</p>}
      <button type="submit">Sign in</button>
      <p className="muted">
        <a href="#/" onClick={() => go("/")}>
          User panel
        </a>
      </p>
    </form>
  );
}
