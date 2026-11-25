import { useMutation } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router";
import {
  AccessPage,
  Field,
  LinkFailure,
  NewPassword,
  NoToken,
  passwordField,
  TextLink,
  useLinkToken,
  useLookup,
} from "@/components/access-page";
import { Button } from "@/components/ui/button";
import { useSingle } from "../api/queries.ts";
import { type Failure as RequestFailure, request } from "../api/request.ts";
import { checkPassword } from "../domain/password.ts";
import type { LoginState } from "./login.tsx";

const lookupReset = (token: string, signal?: AbortSignal) =>
  request("lookupPasswordReset", { body: { token } }, signal);

export function ResetPassword() {
  const token = useLinkToken();
  const { lookup, retry } = useLookup(token, lookupReset);
  const navigate = useNavigate();
  const [fieldError, setFieldError] = useState<string | null>(null);
  const confirm = useSingle(
    useMutation<void, RequestFailure, string>({
      mutationFn: async (password) => {
        const outcome = await request("confirmPasswordReset", {
          body: { token: token ?? "", password },
        });
        if (!outcome.ok) throw outcome.failure;
      },
      onSuccess: () => {
        const email = lookup.kind === "ok" ? lookup.data?.email : undefined;
        const state: LoginState = { passwordChanged: email ?? "" };
        void navigate("/login", { replace: true, state });
      },
    }),
  );

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const password = String(new FormData(e.currentTarget).get("password"));
    const problem = checkPassword(password);
    setFieldError(problem);
    if (problem) {
      e.currentTarget.querySelector<HTMLInputElement>("[name=password]")?.focus();
      return;
    }
    confirm.mutate(password);
  }

  const gone = { to: "/forgot-password", label: "Forgot password" };
  return (
    <AccessPage>
      <h1>Set a new password</h1>
      {lookup.kind === "none" && <NoToken />}
      {lookup.kind === "loading" && <p role="status">Reading the link.</p>}
      {lookup.kind === "failed" && (
        <LinkFailure
          failure={lookup.failure}
          operationId="lookupPasswordReset"
          retry={retry}
          gone={gone}
        />
      )}
      {lookup.kind === "ok" && (
        <form onSubmit={submit} noValidate className="grid gap-4">
          <Field
            label="Email"
            name="email"
            type="email"
            autoComplete="username"
            value={lookup.data?.email ?? ""}
            translate="no"
            readOnly
          />
          <NewPassword
            label="New password"
            error={fieldError ?? passwordField(confirm.error, confirm.variables)}
          />
          {confirm.isError && (
            <LinkFailure failure={confirm.error} operationId="confirmPasswordReset" gone={gone} />
          )}
          <Button type="submit" disabled={confirm.isPending}>
            Set the password
          </Button>
        </form>
      )}
      <TextLink to="/login">Back to log in</TextLink>
    </AccessPage>
  );
}
