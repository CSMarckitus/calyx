import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { Failure } from "@/components/failure";
import { Notice } from "@/components/notice";
import { Risk } from "@/components/risk";
import { Section } from "@/components/section";
import { Button, buttonVariants } from "@/components/ui/button";
import { keys, useChange, useMe } from "../api/queries.ts";
import { NotFound } from "./not-found.tsx";

// The terms of WhatsApp Web: the owner accepts the risk once.
export function Terms() {
  const data = useMe().data;
  const accept = useChange("acknowledgeWhatsAppWeb");
  const navigate = useNavigate();
  const client = useQueryClient();
  const [checked, setChecked] = useState(false);
  if (!data) return null;
  if (data.user.role !== "owner") return <NotFound />;

  // The list waits for the new read of the person. With the old one it shows the notice again.
  async function accepted() {
    await client.refetchQueries({ queryKey: keys.me }, { cancelRefetch: false });
    void navigate("/");
  }

  return (
    <>
      <h1>WhatsApp Web</h1>
      <Section>
        <Risk className="m-0" />
        {data.organization.whatsapp_web_acknowledged_at ? (
          <Notice tone="ok" title="The terms are accepted." />
        ) : (
          <>
            <label className="flex items-start gap-3">
              <input
                type="checkbox"
                className="mt-0.5 size-5.5 flex-none accent-primary"
                checked={checked}
                onChange={(e) => setChecked(e.currentTarget.checked)}
              />
              <span>
                I understand the risk, and I will tell my clients before they link a number.
              </span>
            </label>
            {accept.isError && (
              <Failure
                failure={accept.error}
                context={{ operationId: "acknowledgeWhatsAppWeb", role: "owner" }}
                checking={accept.checking}
              />
            )}
            <div className="flex flex-wrap gap-2">
              <Button
                disabled={!checked || accept.isPending}
                onClick={() =>
                  accept.mutate({ body: { accept: true } }, { onSuccess: () => void accepted() })
                }
              >
                Accept
              </Button>
              <Link to="/" className={buttonVariants({ variant: "outline" })}>
                Not now
              </Link>
            </div>
          </>
        )}
      </Section>
    </>
  );
}
