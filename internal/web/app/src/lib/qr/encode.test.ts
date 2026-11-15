import { describe, expect, it } from "vitest";
import { encodeQR, type QRMatrix } from "./encode";

const URL_TEXT = "http://192.168.1.10:8800/web/enroll";

// encodeQR(URL_TEXT), checked when written by decoding it with an independent
// QR reader (macOS CoreImage), which returned URL_TEXT. 1 is a dark module.
const GOLDEN = [
  "11111110010110011001001111111",
  "10000010010000000010001000001",
  "10111010100110101010001011101",
  "10111010101110000111101011101",
  "10111010111011110001001011101",
  "10000010110111110010001000001",
  "11111110101010101010101111111",
  "00000000100001011010000000000",
  "10111110011100100111101111100",
  "10110101101110001111001010001",
  "10100011010010010100000110000",
  "01010001110110111001011010010",
  "00011010100110000101110101100",
  "10011101000001101001001010101",
  "11110010101011111100011000100",
  "01011101010001001000010100010",
  "00010011100010100100110000100",
  "11011100111100001011011011101",
  "10111110000010010000101001100",
  "10010100101000100000011010010",
  "10000010111100011100111110111",
  "00000000100011101011100011111",
  "11111110001001110111101011100",
  "10000010101111001010100010001",
  "10111010111100100110111111110",
  "10111010101010001110110100011",
  "10111010111110111101101011010",
  "10000010011001011000110110010",
  "11111110100011110100000001100",
];

function rows(matrix: QRMatrix): string[] {
  return matrix.map((row) => row.map((dark) => (dark ? "1" : "0")).join(""));
}

// Reads the 15 format bits around the top-left finder (ISO/IEC 18004 7.9).
function formatBits(m: QRMatrix): number {
  const cells: boolean[] = [];
  for (let i = 0; i <= 5; i++) cells.push(m[i][8]);
  cells.push(m[7][8], m[8][8], m[8][7]);
  for (let i = 9; i < 15; i++) cells.push(m[8][14 - i]);
  return cells.reduce((value, dark, i) => value | ((dark ? 1 : 0) << i), 0) ^ 0x5412;
}

describe("encodeQR (CORE-1614)", () => {
  it("encodes an enroll URL to the reader-verified matrix", () => {
    const code = encodeQR(URL_TEXT);
    expect(code.version).toBe(3);
    expect(rows(code.matrix)).toEqual(GOLDEN);
  });

  it("marks level M and the chosen mask in the format bits", () => {
    const code = encodeQR(URL_TEXT);
    const bits = formatBits(code.matrix);
    expect(bits >>> 13).toBe(0); // level M
    expect((bits >>> 10) & 7).toBe(code.mask);
  });

  it("draws the three finder patterns", () => {
    const { matrix } = encodeQR(URL_TEXT);
    const n = matrix.length;
    const finder = ["1111111", "1000001", "1011101", "1011101", "1011101", "1000001", "1111111"];
    for (const [x0, y0] of [
      [0, 0],
      [n - 7, 0],
      [0, n - 7],
    ]) {
      const block = finder.map((_, dy) =>
        matrix[y0 + dy]
          .slice(x0, x0 + 7)
          .map((dark) => (dark ? "1" : "0"))
          .join(""),
      );
      expect(block).toEqual(finder);
    }
  });

  it("grows the version with the payload and refuses one too long for any version", () => {
    expect(encodeQR("x").version).toBe(1);
    const long = encodeQR("a".repeat(300));
    expect(long.matrix).toHaveLength(long.version * 4 + 17);
    expect(long.version).toBeGreaterThanOrEqual(10);
    expect(() => encodeQR("a".repeat(3000))).toThrow(RangeError);
  });
});
