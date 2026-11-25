import {
  type ComponentProps,
  type ReactNode,
  useCallback,
  useEffect,
  useId,
  useState,
} from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { cn } from "@/lib/utils";
import type { Failure, Outcome } from "../api/request.ts";
import { checkPassword, passwordHint } from "../domain/password.ts";
import { Failure as FailureBox } from "./failure.tsx";
import { Notice } from "./notice.tsx";
import { Button } from "./ui/button.tsx";
import { Input } from "./ui/input.tsx";
import { Label } from "./ui/label.tsx";
import { Wordmark } from "./wordmark.tsx";

// The frame of the screens for a person with no login: the logo, and one column in the middle.
export function AccessPage({ children }: { children: ReactNode }) {
  return (
    <main className="mx-auto grid min-h-dvh w-full max-w-[430px] content-center gap-6.5 px-5 py-14">
      <div className="flex min-h-11 items-center gap-2.5 text-foreground">
        <img src="/calyx-logo.svg" alt="" className="h-9.5 w-10.5" />
        <Wordmark className="h-7 w-auto" />
      </div>
      {children}
    </main>
  );
}

// A link that looks as a text button.
export function TextLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link
      to={to}
      className="inline-flex min-h-11 items-center justify-self-start px-1.5 font-semibold text-primary hover:underline hover:underline-offset-3"
    >
      {children}
    </Link>
  );
}

// The field error of a 400 for one field of the body.
export const fieldFailed = (failure: Failure | null, field: string) =>
  failure?.kind === "problem" &&
  failure.code === "invalid_request" &&
  failure.fields.some((f) => f.location === `body.${field}`);

export function Field({
  label,
  hint,
  error,
  ...input
}: { label: string; hint?: string; error?: string | null } & ComponentProps<"input">) {
  const id = useId();
  const note = error ?? hint;
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id} className="font-semibold">
        {label}
      </Label>
      <Input
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={note ? `${id}-note` : undefined}
        {...input}
      />
      {note && (
        <span
          id={`${id}-note`}
          className={cn("text-sm", error ? "font-semibold text-bad" : "text-muted-foreground")}
        >
          {note}
        </span>
      )}
    </div>
  );
}

// The field of a new password, with the rule of the backend as its hint.
export function NewPassword({ label, error }: { label: string; error: string | null }) {
  return (
    <Field
      label={label}
      name="password"
      type="password"
      autoComplete="new-password"
      hint={passwordHint}
      error={error}
      required
    />
  );
}

// The token of a link in a mail. The page reads it once, and then removes it from the address,
// so it is not in the history. A reload then has no token.
export function useLinkToken(): string | null {
  const [token] = useState(() => new URLSearchParams(window.location.search).get("token") || null);
  const navigate = useNavigate();
  const { pathname, search } = useLocation();
  useEffect(() => {
    if (new URLSearchParams(search).has("token")) void navigate(pathname, { replace: true });
  }, [navigate, pathname, search]);
  return token;
}

export type Lookup<T> =
  | { kind: "none" }
  | { kind: "loading" }
  | { kind: "ok"; data: T }
  | { kind: "failed"; failure: Failure };

// A lookup of a token. It is not a query of the cache: the stream must not read it again, because
// each lookup counts in the limit of the backend.
export function useLookup<T>(
  token: string | null,
  ask: (token: string, signal?: AbortSignal) => Promise<Outcome<T>>,
): { lookup: Lookup<T>; retry: () => void } {
  const [lookup, setLookup] = useState<Lookup<T>>(token ? { kind: "loading" } : { kind: "none" });
  const load = useCallback(
    (signal?: AbortSignal) => {
      if (!token) return;
      setLookup({ kind: "loading" });
      void ask(token, signal).then((outcome) => {
        if (signal?.aborted) return;
        setLookup(
          outcome.ok
            ? { kind: "ok", data: outcome.data }
            : { kind: "failed", failure: outcome.failure },
        );
      });
    },
    [token, ask],
  );
  useEffect(() => {
    const control = new AbortController();
    load(control.signal);
    return () => control.abort();
  }, [load]);
  return { lookup, retry: () => load() };
}

const context = { role: null } as const;

// A 410 leads to a new link. Any other failure can pass with a new attempt.
export function LinkFailure({
  failure,
  operationId,
  retry,
  gone,
}: {
  failure: Failure;
  operationId:
    | "lookupPasswordReset"
    | "confirmPasswordReset"
    | "lookupInvitation"
    | "acceptInvitation";
  retry?: () => void;
  gone: { to: string; label: string };
}) {
  const isGone = failure.kind === "problem" && failure.status === 410;
  return (
    <>
      <FailureBox failure={failure} context={{ ...context, operationId }} />
      {isGone ? (
        <TextLink to={gone.to}>{gone.label}</TextLink>
      ) : (
        retry && (
          <Button type="button" variant="outline" className="justify-self-start" onClick={retry}>
            Try again
          </Button>
        )
      )}
    </>
  );
}

export function NoToken() {
  return (
    <Notice tone="idle" title="Open the link of the mail again.">
      This page removes the link from the address after it reads it, so a reload cannot read it.
    </Notice>
  );
}

// The field error of a new password from an answer of the backend (400 invalid_request). The
// text is the rule that the sent password breaks.
export function passwordField(
  failure: Failure | null,
  sent: string | undefined,
  field = "password",
): string | null {
  if (failure?.kind !== "problem" || failure.code !== "invalid_request") return null;
  if (!failure.fields.some((f) => f.location === `body.${field}`)) return null;
  return checkPassword(sent ?? "") ?? passwordHint;
}
