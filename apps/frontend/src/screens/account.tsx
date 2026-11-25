import { type FormEvent, useState } from "react";
import { Field, fieldFailed, NewPassword, passwordField } from "@/components/access-page";
import { Failure } from "@/components/failure";
import { Notice } from "@/components/notice";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { useChange, useMe } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { useSession } from "../app/session.tsx";
import type { Role } from "../domain/errors.ts";
import { checkPassword } from "../domain/password.ts";

export function AccountSection({
  email,
  role,
  manager,
}: {
  email: string;
  role: Role;
  manager: string;
}) {
  const change = useChange("changePassword");
  const session = useSession();
  const [newError, setNewError] = useState<string | null>(null);
  const [leaving, setLeaving] = useState(false);
  const [logoutFailure, setLogoutFailure] = useState<RequestFailure | null>(null);

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const data = new FormData(form);
    const next = String(data.get("password"));
    const problem = checkPassword(next);
    setNewError(problem);
    if (problem) {
      form.querySelector<HTMLInputElement>("[name=password]")?.focus();
      return;
    }
    change.mutate(
      { body: { current_password: String(data.get("current")), new_password: next } },
      { onSuccess: () => form.reset() },
    );
  }

  async function logOut() {
    setLeaving(true);
    setLogoutFailure(await session.logout());
    setLeaving(false);
  }

  const sent = change.variables?.body.new_password;
  return (
    <Section title="Your account">
      <p className="m-0" translate="no">
        {email}
      </p>
      <form onSubmit={submit} noValidate className="grid gap-4">
        <Field
          label="Current password"
          name="current"
          type="password"
          autoComplete="current-password"
          error={
            fieldFailed(change.error, "current_password") ? "The password is not correct." : null
          }
          required
        />
        <NewPassword
          label="New password"
          error={newError ?? passwordField(change.error, sent, "new_password")}
        />
        {change.isError && (
          <Failure
            failure={change.error}
            context={{ operationId: "changePassword", role, manager }}
            checking={change.checking}
          />
        )}
        {change.isSuccess && (
          <Notice tone="ok" title="Your password is changed.">
            Your other login sessions ended.
          </Notice>
        )}
        {logoutFailure && (
          <Failure failure={logoutFailure} context={{ operationId: "logout", role, manager }} />
        )}
        <div className="flex flex-wrap gap-2">
          <Button type="submit" disabled={change.isPending}>
            Change password
          </Button>
          <Button type="button" variant="outline" disabled={leaving} onClick={() => void logOut()}>
            Log out
          </Button>
        </div>
      </form>
    </Section>
  );
}

export function AccountScreen() {
  const data = useMe().data;
  if (!data) return null;
  const role = data.user.role === "owner" ? "owner" : "client";
  return (
    <>
      <h1>Account</h1>
      <AccountSection email={data.user.email} role={role} manager={data.organization.name} />
    </>
  );
}
