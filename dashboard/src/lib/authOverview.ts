import type { Auth } from "./types";

export interface AuthOverview {
  methods: string[];
  signup: boolean;
  anonymous: boolean;
  verifyEmail: boolean;
  jwtExpiry: string;
}

const PROVIDER_LABELS: Record<string, string> = { google: "Google", github: "GitHub" };

const titleCase = (s: string) => PROVIDER_LABELS[s] ?? s.charAt(0).toUpperCase() + s.slice(1);

/** Sign-in methods and switches as the server will apply them (email/password is always on). */
export function authOverview(auth: Auth | null | undefined): AuthOverview {
  const signup = auth?.allow_signup ?? true;
  const anonymous = signup && (auth?.allow_anonymous ?? false);
  const oauth = Object.entries(auth?.oauth ?? {})
    .filter(([, p]) => p)
    .map(([name]) => titleCase(name));
  const methods = ["Email", ...oauth, ...(anonymous ? ["Anonymous"] : [])];
  return {
    methods,
    signup,
    anonymous,
    verifyEmail: auth?.email?.verify_email ?? false,
    jwtExpiry: auth?.jwt_expiry || "default",
  };
}
