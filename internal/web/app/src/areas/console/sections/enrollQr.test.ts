import { describe, expect, it } from "vitest";
import { enrollQrTarget, isLoopbackHost } from "./enrollQr";

const lan = [
  { kind: "lan", url: "http://192.168.1.10:8800" },
  { kind: "overlay", url: "https://100.64.0.2:8800" },
];

describe("enrollQrTarget (CORE-1614)", () => {
  it("encodes the address the Console is open at when a phone can reach it", () => {
    expect(enrollQrTarget("http://192.168.1.10:8800", "192.168.1.10", "/web/", null)).toBe(
      "http://192.168.1.10:8800/web/enroll",
    );
    expect(enrollQrTarget("https://media.example.test", "media.example.test", "/web/", lan)).toBe(
      "https://media.example.test/web/enroll",
    );
  });

  it("uses the server's first LAN endpoint on loopback, never localhost", () => {
    for (const host of ["localhost", "127.0.0.1", "[::1]", "app.localhost"]) {
      expect(enrollQrTarget(`http://${host}:8800`, host, "/web/", lan)).toBe(
        "http://192.168.1.10:8800/web/enroll",
      );
    }
  });

  it("offers nothing on loopback without a usable LAN endpoint", () => {
    expect(enrollQrTarget("http://localhost:8800", "localhost", "/web/", null)).toBeNull();
    expect(enrollQrTarget("http://localhost:8800", "localhost", "/web/", [lan[1]])).toBeNull();
    expect(
      enrollQrTarget("http://localhost:8800", "localhost", "/web/", [
        { kind: "lan", url: "http://127.0.0.1:8800" },
      ]),
    ).toBeNull();
    expect(
      enrollQrTarget("http://localhost:8800", "localhost", "/web/", [{ kind: "lan", url: "nope" }]),
    ).toBeNull();
  });

  it("recognises loopback hostnames", () => {
    expect(isLoopbackHost("LOCALHOST")).toBe(true);
    expect(isLoopbackHost("127.4.5.6")).toBe(true);
    expect(isLoopbackHost("192.168.1.10")).toBe(false);
    // Aliases a browser accepts for this machine (Codex review, CORE-1614).
    for (const host of [
      "localhost.",
      "app.localhost.",
      "[::ffff:7f00:1]",
      "[::ffff:127.0.0.1]",
      "0.0.0.0",
      "[::]",
      "[0:0:0:0:0:0:0:1]",
    ]) {
      expect(isLoopbackHost(host), host).toBe(true);
    }
    for (const host of ["[::ffff:c0a8:10a]", "localhost.example", "127.example", "[fe80::1]"]) {
      expect(isLoopbackHost(host), host).toBe(false);
    }
    expect(isLoopbackHost("localhost.example.test")).toBe(false);
  });
});
