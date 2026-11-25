import { ConnectMcp, ConnectProgram } from "@/components/program-help";
import { Section } from "@/components/section";
import { useMe } from "../api/queries.ts";

export function ConnectScreen() {
  const me = useMe().data;
  if (!me) return null;
  return (
    <>
      <h1>Connect a program</h1>
      <Section>
        <ConnectProgram level={2} />
      </Section>
      <Section>
        <ConnectMcp level={2} owner={me.user.role === "owner"} open />
      </Section>
    </>
  );
}
