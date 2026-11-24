import { expect, it } from "vitest";
import { afterEnrollURL, enrollReturnPath } from "./returnTo";

// The QR route survives enrollment; nothing else, and never another origin.

it("carries only the QR route", () => {
  expect(enrollReturnPath("/play-on", "?display=h1")).toBe("/play-on?display=h1");
  expect(enrollReturnPath("/mydata", "")).toBeNull();
});

it("lands on the QR route after enrolling and refuses anything else", () => {
  const base = import.meta.env.BASE_URL;
  const root = base.replace(/\/$/, "");
  expect(afterEnrollURL("?next=%2Fplay-on%3Fdisplay%3Dh1")).toBe(`${root}/play-on?display=h1`);
  expect(afterEnrollURL("")).toBe(base);
  expect(afterEnrollURL("?next=%2F%2Fevil.example%2Fplay-on")).toBe(base);
  expect(afterEnrollURL("?next=https%3A%2F%2Fevil.example")).toBe(base);
  expect(afterEnrollURL("?next=%2Fsettings")).toBe(base);
});
