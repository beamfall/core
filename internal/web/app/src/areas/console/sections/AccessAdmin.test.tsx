import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccessAdmin, ScheduleAdmin } from "./AccessAdmin";

vi.mock("./Profiles", () => ({
  Profiles: () => <section aria-label="Profiles lane" />,
}));

vi.mock("./Devices", () => ({
  Devices: () => <section aria-label="Devices lane" />,
}));

vi.mock("./ProfilesAccess", () => ({
  ProfilesAccess: () => <section aria-label="Policy access lane" />,
}));

vi.mock("./SignInSecurity", () => ({
  SignInSecurity: () => <section aria-label="Sign-in security lane" />,
}));

describe("AccessAdmin", () => {
  it("keeps profile, device role and sign-in administration together", () => {
    render(<AccessAdmin />);

    expect(screen.getByLabelText("Profiles lane")).toBeInTheDocument();
    expect(screen.getByLabelText("Devices lane")).toBeInTheDocument();
    expect(screen.getByLabelText("Sign-in security lane")).toBeInTheDocument();
    expect(screen.queryByLabelText("Policy access lane")).toBeNull();
  });
});

describe("ScheduleAdmin", () => {
  it("holds the parental policy lane on its own page (CORE-1117)", () => {
    render(<ScheduleAdmin />);

    expect(screen.getByLabelText("Policy access lane")).toBeInTheDocument();
    expect(screen.queryByLabelText("Profiles lane")).toBeNull();
  });
});
