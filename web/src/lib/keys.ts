// Device keys are made here, in the browser, and the private key never
// leaves it (ADR 0003).
import nacl from "tweetnacl";

export interface KeyPair {
  privateKey: string; // base64, as WireGuard writes it
  publicKey: string;
}

const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b));

/** A key pair from a raw 32-byte private key. */
export function keyPairFromPrivate(priv: Uint8Array): KeyPair {
  return { privateKey: b64(priv), publicKey: b64(nacl.scalarMult.base(priv)) };
}

/** A fresh WireGuard key pair, clamped like `wg genkey`. */
export function generateKeyPair(): KeyPair {
  const priv = new Uint8Array(32);
  crypto.getRandomValues(priv);
  priv[0] &= 248;
  priv[31] = (priv[31] & 127) | 64;
  return keyPairFromPrivate(priv);
}
