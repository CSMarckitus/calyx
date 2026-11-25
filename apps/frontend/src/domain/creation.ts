export type CreationProblem =
  | "whatsapp_web_not_allowed"
  | "whatsapp_web_not_acknowledged"
  | "quota_exceeded";

type Org = {
  whatsapp_web_allowed: boolean;
  whatsapp_web_acknowledged_at: string | null;
  instance_count: number;
  max_instances: number;
};
type ClientLimits = { instance_count: number; max_instances: number };

// The problem of a creation for an owner, in the order of `tenancy/quota.go`. The backend gives
// `create_instance_problem` only to a client user, so the page calculates the rest. The server
// stays the authority: the page reads again when it refuses. `can_create_instances` is not here,
// because it does not limit an owner.
export function creationProblem(org: Org, client: ClientLimits): CreationProblem | null {
  if (!org.whatsapp_web_allowed) return "whatsapp_web_not_allowed";
  if (org.whatsapp_web_acknowledged_at === null) return "whatsapp_web_not_acknowledged";
  if (org.instance_count >= org.max_instances) return "quota_exceeded";
  if (client.max_instances !== 0 && client.instance_count >= client.max_instances)
    return "quota_exceeded";
  return null;
}
