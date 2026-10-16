// The API client, typed from the OpenAPI contract the Coordinator checks in
// (internal/coordinator/openapi.json; `npm run gen` regenerates the types).
import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

export type Me = components["schemas"]["Me"];
export type DeviceInfo = components["schemas"]["DeviceInfo"];
export type IssuedDevice = components["schemas"]["IssuedDevice"];
export type InviteInfo = components["schemas"]["InviteInfo"];
export type TOTPOffer = components["schemas"]["TOTPOffer"];

// Every request is JSON: the API refuses anything else that changes state.
export const api = createClient<paths>({ baseUrl: "", headers: { "Content-Type": "application/json" } });

/** The message of an API error, if any. */
export function errorOf(e: unknown): string | undefined {
  return (e as { error?: string } | undefined)?.error;
}
