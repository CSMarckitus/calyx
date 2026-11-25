import { useMutation } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
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
import { Notice } from "@/components/notice";
import { Button } from "@/components/ui/button";
import { useMe, useSingle } from "../api/queries.ts";
import { type Failure as RequestFailure, request } from "../api/request.ts";
import { checkPassword } from "../domain/password.ts";

const lookupInvitation = (token: string, signal?: AbortSignal) =>
  request("lookupInvitation", { body: { token } }, signal);

const gone = { to: "/login", label: "Log in" };

export function Invite() {
  const token = useLinkToken();
  const { lookup, retry } = useLookup(token, lookupInvitation);
  const me = useMe();
  // The acceptance replaces the login session, so a logged-in person reads that first.
  const [goOn, setGoOn] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const accept = useSingle(
    useMutation<void, RequestFailure, string>({
      mutationFn: async (password) => {
        const outcome = await request("acceptInvitation", {
          body: { token: token ?? "", password },
        });
        if (!outcome.ok) throw outcome.failure;
      },
      // A new load of the page: no data of a person before stays in it.
      onSuccess: () => window.location.replace("/"),
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
    accept.mutate(password);
  }

  if (lookup.kind === "none") {
    return (
      <AccessPage>
        <h1>Welcome</h1>
        <NoToken />
        <TextLink to="/login">Log in</TextLink>
      </AccessPage>
    );
  }
  if (lookup.kind === "loading" || me.isPending) {
    return (
      <AccessPage>
        <h1>Welcome</h1>
        <p role="status">Reading the link.</p>
      </AccessPage>
    );
  }
  if (lookup.kind === "failed") {
    return (
      <AccessPage>
        <h1>Welcome</h1>
        <LinkFailure
          failure={lookup.failure}
          operationId="lookupInvitation"
          retry={retry}
          gone={gone}
        />
      </AccessPage>
    );
  }

  const invitation = lookup.data;
  if (me.isSuccess && !goOn) {
    return (
      <AccessPage>
        <h1>Welcome</h1>
        <Notice
          tone="wait"
          title={
            <>
              You are logged in as <span translate="no">{me.data?.user.email}</span>.
            </>
          }
        >
          This invitation logs you in as <span translate="no">{invitation?.email}</span>. Your login
          on this browser ends.
        </Notice>
        <div className="flex flex-wrap items-center gap-3">
          <Button type="button" onClick={() => setGoOn(true)}>
            Continue with the invitation
          </Button>
          <TextLink to="/">
            Back to <span translate="no">Calyx</span>
          </TextLink>
        </div>
      </AccessPage>
    );
  }

  return (
    <AccessPage>
      <div className="grid gap-2">
        <h1>Welcome</h1>
        <p className="text-muted-foreground">
          <span translate="no">{invitation?.organization_name}</span> invites you to use{" "}
          <span translate="no">Calyx</span>.
        </p>
      </div>
      <form onSubmit={submit} noValidate className="grid gap-4">
        <Field
          label="Email"
          name="email"
          type="email"
          autoComplete="username"
          value={invitation?.email ?? ""}
          translate="no"
          readOnly
        />
        <NewPassword
          label="Choose a password"
          error={fieldError ?? passwordField(accept.error, accept.variables)}
        />
        {accept.isError && (
          <LinkFailure failure={accept.error} operationId="acceptInvitation" gone={gone} />
        )}
        <Button type="submit" disabled={accept.isPending || accept.isSuccess}>
          Set password and continue
        </Button>
      </form>
    </AccessPage>
  );
}
