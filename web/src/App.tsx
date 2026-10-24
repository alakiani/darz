import { useCallback, useEffect, useState } from "react";
import { api, type Me } from "./api/client";
import { fa } from "./fa";
import { go, useRoute } from "./router";
import { InvitePage } from "./pages/Invite";
import { LoginPage } from "./pages/Login";
import { HomePage } from "./pages/Home";
import { NewDevicePage } from "./pages/NewDevice";
import { DevicePage } from "./pages/Device";
import { AccountPage } from "./pages/Account";
import { HelpPage } from "./pages/Help";
import { UsagePage } from "./pages/Usage";
import { AdminApp } from "./admin/AdminApp";

export function App() {
  const route = useRoute();
  if (route === "/admin" || route.startsWith("/admin/")) return <AdminApp route={route} />;
  return <UserApp route={route} />;
}

function UserApp({ route }: { route: string }) {
  const [me, setMe] = useState<Me | null | undefined>(undefined); // undefined: not known yet

  const refresh = useCallback(async () => {
    const { data } = await api.GET("/v1/me");
    setMe(data ?? null);
  }, []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  const signedIn = (m: Me) => {
    setMe(m);
    go("/");
  };

  let page;
  if (route.startsWith("/invite/")) {
    page = <InvitePage token={route.slice("/invite/".length)} onSignedIn={signedIn} />;
  } else if (route === "/help") {
    page = <HelpPage />;
  } else if (me === undefined) {
    page = null;
  } else if (me === null) {
    page = <LoginPage onSignedIn={signedIn} />;
  } else if (route === "/devices/new") {
    page = <NewDevicePage onDone={refresh} />;
  } else if (route.startsWith("/devices/")) {
    page = <DevicePage id={route.slice("/devices/".length)} onChange={refresh} />;
  } else if (route === "/usage") {
    page = <UsagePage />;
  } else if (route === "/account") {
    page = <AccountPage me={me} onChange={refresh} onSignedOut={() => setMe(null)} />;
  } else {
    page = <HomePage me={me} />;
  }

  return (
    <div className="app">
      <header>
        <a href="#/" className="brand">
          {fa.appName}
          <small>{fa.tagline}</small>
        </a>
        <nav>
          <a href="#/help">{fa.help}</a>
          {me && <a href="#/usage">{fa.usage}</a>}
          {me && <a href="#/account">{fa.account}</a>}
        </nav>
      </header>
      <main>{page}</main>
    </div>
  );
}
