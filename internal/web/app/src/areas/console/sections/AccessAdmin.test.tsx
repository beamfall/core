import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccessAdmin } from "./AccessAdmin";

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
  it("keeps profile, device role, and policy access administration as separate lanes", () => {
    render(<AccessAdmin />);

    expect(screen.getByLabelText("Profiles lane")).toBeInTheDocument();
    expect(screen.getByLabelText("Devices lane")).toBeInTheDocument();
    expect(screen.getByLabelText("Policy access lane")).toBeInTheDocument();
    expect(screen.getByLabelText("Sign-in security lane")).toBeInTheDocument();
  });
});
