import { describe, expect, it } from "vitest";
import { configFiles } from "./config";

const cfg = {
  address: "10.77.0.2/32",
  ipv6_placeholder: "fd77::1/128",
  dns: "10.77.0.1",
  bridge_public_key: "BRIDGEKEY=",
  allowed_ips: ["0.0.0.0/0", "::/0"],
  endpoints: [
    { label: "Main", host: "b.wayfinder.ir", port: 61873 },
    { label: "Backup 1", host: "c.wayfinder.ir", port: 61873 },
    { label: "Backup 2", host: "a.wayfinder.ir", port: 61873 },
  ],
};

describe("config files", () => {
  it("renders one stock WireGuard config per Endpoint Slot", () => {
    const files = configFiles(cfg, "PRIVATEKEY=");
    expect(files.map((f) => f.name)).toEqual(["ways-main.conf", "ways-backup1.conf", "ways-backup2.conf"]);
    expect(files[0].text).toBe(
      "[Interface]\n" +
        "PrivateKey = PRIVATEKEY=\n" +
        "Address = 10.77.0.2/32, fd77::1/128\n" +
        "DNS = 10.77.0.1\n" +
        "\n" +
        "[Peer]\n" +
        "PublicKey = BRIDGEKEY=\n" +
        "AllowedIPs = 0.0.0.0/0, ::/0\n" +
        "Endpoint = b.wayfinder.ir:61873\n" +
        "PersistentKeepalive = 25\n",
    );
    expect(files[2].text).toContain("Endpoint = a.wayfinder.ir:61873\n");
  });

  it("names tunnels within the Android app's 15 characters", () => {
    for (const f of configFiles(cfg, "K=")) {
      expect(f.name.replace(/\.conf$/, "").length).toBeLessThanOrEqual(15);
    }
  });
});
