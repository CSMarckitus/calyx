import { useMutation, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useState } from "react";
import { Navigate, useLocation, useNavigate, useSearchParams } from "react-router";
import { AccessPage, Field, TextLink } from "@/components/access-page";
import { Failure } from "@/components/failure";
import { Notice } from "@/components/notice";
import { Button } from "@/components/ui/button";
import { meOptions, useMe, useSingle } from "../api/queries.ts";
import { type Failure as RequestFailure, request } from "../api/request.ts";
import { outsidePanel, safeNext } from "../app/session.tsx";

// What the reset screen gives to this screen: the email of the new password.
export type LoginState = { passwordChanged?: string } | null;

export function Login() {
  const me = useMe();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const { pathname, search, state } = useLocation();
  // Read once and then removed from the history entry, so a reload does not show the notice
  // again and the email does not stay in the history.
  const [changed] = useState((state as LoginState)?.passwordChanged);
  useEffect(() => {
    if (state) void navigate({ pathname, search }, { replace: true, state: null });
  }, [state, navigate, pathname, search]);
  const login = useSingle(
    useMutation<void, RequestFailure, { email: string; password: string }>({
      // The session reads this key: a 401 of login does not end a login session.
      mutationKey: ["login"],
      mutationFn: async (body) => {
        const outcome = await request("login", { body });
        if (!outcome.ok) throw outcome.failure;
      },
      onSuccess: async () => {
        // The page has a user only after a getMe that succeeded.
        await client.fetchQuery({ ...meOptions(), staleTime: 0 }).catch(() => undefined);
        const next = safeNext(params.get("next"), location.origin);
        // The docs page is not part of the router: it needs a full page load.
        if (outsidePanel(next)) location.replace(next);
        else void navigate(next, { replace: true });
      },
    }),
  );

  // A person who is logged in already does not need this form. After a reset the old login
  // session can be gone, so the form stays with its notice.
  if (me.isSuccess && login.isIdle && changed === undefined) return <Navigate to="/" replace />;

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    login.mutate({ email: String(form.get("email")), password: String(form.get("password")) });
  }

  return (
    <AccessPage>
      <h1>Log in</h1>
      {changed && !login.isError && (
        <Notice tone="ok" title="Your password is changed.">
          Log in with the new password.
        </Notice>
      )}
      <form onSubmit={submit} className="grid gap-4">
        <Field
          label="Email"
          name="email"
          type="email"
          autoComplete="username"
          defaultValue={changed}
          translate="no"
          required
        />
        <Field
          label="Password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
        />
        {login.isError && (
          <Failure failure={login.error} context={{ operationId: "login", role: null }} />
        )}
        <Button type="submit" disabled={login.isPending}>
          Log in
        </Button>
        <TextLink to="/forgot-password">Forgot your password?</TextLink>
      </form>
    </AccessPage>
  );
}
