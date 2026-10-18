/** Hands a file to the user: through the share sheet on a phone that can
 * share files, so a config opens straight in the WireGuard app, and as a
 * download everywhere else. */

type Sharer = Pick<Navigator, "share" | "canShare">;

/** Whether this browser can share the file. Only touch devices: a desktop
 * user saving a file wants a download, not a share sheet. */
export function canShareFile(file: File, nav: Partial<Sharer> = navigator, coarse = matchMedia("(pointer: coarse)").matches): boolean {
  return coarse && typeof nav.share === "function" && typeof nav.canShare === "function" && nav.canShare({ files: [file] });
}

export function download(file: File) {
  const url = URL.createObjectURL(file);
  const a = document.createElement("a");
  a.href = url;
  a.download = file.name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Shares the file if the browser can, falling back to a download. Must
 * run from a click handler: share needs the user's gesture. */
export async function saveFile(file: File, nav: Partial<Sharer> = navigator, coarse?: boolean) {
  if (!canShareFile(file, nav, coarse)) return download(file);
  try {
    await nav.share!({ files: [file] });
  } catch (e) {
    // The user closing the sheet is not a failure; anything else is.
    if (!(e instanceof DOMException && e.name === "AbortError")) download(file);
  }
}
