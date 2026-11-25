import { useMutation } from "@tanstack/react-query";
import type { FormEvent } from "react";
import { Navigate } from "react-router";
import { AccessPage, Field, TextLink } from "@/components/access-page";
import { Failure } from "@/components/failure";
import { Notice } from "@/components/notice";
import { Button } from "@/components/ui/button";
import { useHealth } from "../api/health.ts";
import { useMe, useSingle } from "../api/queries.ts";
import { type Failure as RequestFailure, request } from "../api/request.ts";

export function ForgotPassword() {
  const me = useMe();
  const { mail } = useHealth();
  const send = useSingle(
    useMutation<void, RequestFailure, string>({
      mutationFn: async (email) => {
        const outcome = await request("requestPasswordReset", { body: { email } });
        if (!outcome.ok) throw outcome.failure;
      },
    }),
  );

  if (me.isSuccess) return <Navigate to="/" replace />;

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    send.mutate(String(new FormData(e.currentTarget).get("email")));
  }

  return (
    <AccessPage>
      <h1>Reset your password</h1>
      {mail === "off" ? (
        <Notice tone="wait" title="This server sends no mail.">
          Ask your organization admin for a reset link. If you are the organization admin, ask the
          server admin.
        </Notice>
      ) : send.isSuccess ? (
        // The answer is the same for an unknown email, so the text cannot say more.
        <Notice tone="ok" title="Check your mail.">
          If this email has an account, <span translate="no">Calyx</span> sent a link to it. The
          link is valid for one hour.
        </Notice>
      ) : (
        <>
          <p className="text-muted-foreground">We send you a link. It is valid for one hour.</p>
          <form onSubmit={submit} className="grid gap-4">
            <Field
              label="Email"
              name="email"
              type="email"
              autoComplete="username"
              translate="no"
              required
            />
            {send.isError && (
              <Failure
                failure={send.error}
                context={{ operationId: "requestPasswordReset", role: null }}
              />
            )}
            <Button type="submit" disabled={send.isPending}>
              Send the link
            </Button>
          </form>
        </>
      )}
      <TextLink to="/login">Back to log in</TextLink>
    </AccessPage>
  );
}
