import { describe, expect, it } from "vitest";
import { generateKeyPair, keyPairFromPrivate } from "./keys";

const hex = (s: string) => Uint8Array.from(s.match(/../g)!.map((b) => parseInt(b, 16)));
const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b));

describe("X25519 keys", () => {
  it("matches RFC 7748 section 6.1", () => {
    const priv = hex("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a");
    const kp = keyPairFromPrivate(priv);
    expect(kp.publicKey).toBe(b64(hex("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")));
  });

  it("generates clamped WireGuard keys, different every time", () => {
    const a = generateKeyPair();
    const b = generateKeyPair();
    expect(a.privateKey).not.toBe(b.privateKey);
    const raw = Uint8Array.from(atob(a.privateKey), (c) => c.charCodeAt(0));
    expect(raw.length).toBe(32);
    expect(raw[0] & 7).toBe(0);
    expect(raw[31] & 128).toBe(0);
    expect(raw[31] & 64).toBe(64);
    expect(a.publicKey).toBe(keyPairFromPrivate(raw).publicKey);
  });
});
