import { describe, expect, it, vi } from "vitest";
import { canShareFile, saveFile } from "./save";

const file = new File(["[Interface]\n"], "darz-main.conf", { type: "text/plain" });

describe("canShareFile", () => {
  it("shares on a phone whose browser can share the file", () => {
    expect(canShareFile(file, { share: vi.fn(), canShare: () => true }, true)).toBe(true);
  });

  it("downloads on a desktop, a browser without share, or a refused file", () => {
    expect(canShareFile(file, { share: vi.fn(), canShare: () => true }, false)).toBe(false);
    expect(canShareFile(file, {}, true)).toBe(false);
    expect(canShareFile(file, { share: vi.fn(), canShare: () => false }, true)).toBe(false);
  });
});

describe("saveFile", () => {
  it("hands the file to the share sheet", async () => {
    const share = vi.fn().mockResolvedValue(undefined);
    await saveFile(file, { share, canShare: () => true }, true);
    expect(share).toHaveBeenCalledWith({ files: [file] });
  });

  it("does nothing more when the user closes the sheet", async () => {
    const share = vi.fn().mockRejectedValue(new DOMException("closed", "AbortError"));
    await expect(saveFile(file, { share, canShare: () => true }, true)).resolves.toBeUndefined();
  });
});
