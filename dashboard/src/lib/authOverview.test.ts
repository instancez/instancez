import { describe, it, expect } from "vitest";
import { authOverview } from "./authOverview";
import type { Auth } from "./types";

const base: Auth = {
  jwt_expiry: "15m",
  refresh_token_expiry: "7d",
  allow_signup: null,
  allow_anonymous: null,
  redirect_urls: [],
  email: null,
  oauth: {},
};

describe("authOverview", () => {
  it("treats a missing auth block as signup on, email only", () => {
    expect(authOverview(null)).toEqual({ methods: ["Email"], signup: true, anonymous: false, verifyEmail: false, jwtExpiry: "default" });
    expect(authOverview(undefined).methods).toEqual(["Email"]);
  });

  it("lists email, configured oauth providers and anonymous in order", () => {
    const a = authOverview({
      ...base,
      allow_anonymous: true,
      email: { verify_email: true, templates: {} },
      oauth: { google: { client_id: "x" } as never, github: null, gitlab: { client_id: "y" } as never },
    });
    expect(a.methods).toEqual(["Email", "Google", "Gitlab", "Anonymous"]);
    expect(a.verifyEmail).toBe(true);
    expect(a.jwtExpiry).toBe("15m");
  });

  it("disables anonymous when public sign-up is off", () => {
    const a = authOverview({ ...base, allow_signup: false, allow_anonymous: true });
    expect(a.signup).toBe(false);
    expect(a.anonymous).toBe(false);
    expect(a.methods).toEqual(["Email"]);
  });

  it("falls back to default for an empty jwt_expiry", () => {
    expect(authOverview({ ...base, jwt_expiry: "" }).jwtExpiry).toBe("default");
  });
});
