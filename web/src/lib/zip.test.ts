import { describe, expect, it } from "vitest";
import { crc32, makeZip } from "./zip";

describe("zip", () => {
  it("computes CRC-32", () => {
    expect(crc32(new TextEncoder().encode("hello"))).toBe(0x3610a686);
    expect(crc32(new Uint8Array())).toBe(0);
  });

  it("stores files a reader can find through the central directory", () => {
    const zip = makeZip([
      { name: "ways-main.conf", data: "[Interface]\n" },
      { name: "ways-backup1.conf", data: "x" },
    ]);
    const view = new DataView(zip.buffer);
    // End of central directory record, at the end with no comment.
    const eocd = zip.length - 22;
    expect(view.getUint32(eocd, true)).toBe(0x06054b50);
    expect(view.getUint16(eocd + 10, true)).toBe(2);
    let at = view.getUint32(eocd + 16, true);
    const names: string[] = [];
    for (let i = 0; i < 2; i++) {
      expect(view.getUint32(at, true)).toBe(0x02014b50);
      const nameLen = view.getUint16(at + 28, true);
      const local = view.getUint32(at + 42, true);
      expect(view.getUint32(local, true)).toBe(0x04034b50);
      names.push(new TextDecoder().decode(zip.subarray(at + 46, at + 46 + nameLen)));
      at += 46 + nameLen;
    }
    expect(names).toEqual(["ways-main.conf", "ways-backup1.conf"]);
  });
});
