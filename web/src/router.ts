// Hash routes: the Invite token in #/invite/<token> never reaches a server.
import { useEffect, useState } from "react";

export function useRoute(): string {
  const [route, setRoute] = useState(() => location.hash.slice(1) || "/");
  useEffect(() => {
    const on = () => setRoute(location.hash.slice(1) || "/");
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function go(path: string) {
  location.hash = path;
}
