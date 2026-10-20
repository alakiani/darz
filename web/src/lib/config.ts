// A Device's configs, one per Endpoint Slot, rendered in the browser so the
// private key never leaves it (ADR 0003, ADR 0005).

export interface DeviceConfig {
  address: string;
  ipv6_placeholder: string;
  dns: string;
  bridge_public_key: string;
  allowed_ips: string[];
  endpoints: { label: string; host: string; port: number }[];
}

export interface ConfigFile {
  label: string;
  name: string; // the file name becomes the tunnel's name; Android allows 15 characters
  text: string;
}

const fileNames: Record<string, string> = {
  Main: "ways-main",
  "Backup 1": "ways-backup1",
  "Backup 2": "ways-backup2",
};

export function configFiles(cfg: DeviceConfig, privateKey: string): ConfigFile[] {
  return cfg.endpoints.map((e, i) => ({
    label: e.label,
    name: (fileNames[e.label] ?? `ways-${i + 1}`) + ".conf",
    text:
      "[Interface]\n" +
      `PrivateKey = ${privateKey}\n` +
      `Address = ${cfg.address}, ${cfg.ipv6_placeholder}\n` +
      `DNS = ${cfg.dns}\n` +
      "\n" +
      "[Peer]\n" +
      `PublicKey = ${cfg.bridge_public_key}\n` +
      `AllowedIPs = ${cfg.allowed_ips.join(", ")}\n` +
      `Endpoint = ${e.host}:${e.port}\n` +
      // Mobile networks drop idle UDP mappings quickly.
      "PersistentKeepalive = 25\n",
  }));
}
