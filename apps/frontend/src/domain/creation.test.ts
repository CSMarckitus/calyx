import { describe, expect, it } from "vitest";
import { creationProblem } from "./creation.ts";

const org = {
  whatsapp_web_allowed: true,
  whatsapp_web_acknowledged_at: "2026-11-01T10:00:00Z" as string | null,
  instance_count: 1,
  max_instances: 5,
};
const client = { instance_count: 1, max_instances: 0, can_create_instances: true };

describe("creationProblem", () => {
  it("gives null when no rule refuses", () => {
    expect(creationProblem(org, client)).toBeNull();
  });

  it("rule 1: WhatsApp Web is blocked", () => {
    expect(creationProblem({ ...org, whatsapp_web_allowed: false }, client)).toBe(
      "whatsapp_web_not_allowed",
    );
  });

  it("rule 2: no acknowledgement", () => {
    expect(creationProblem({ ...org, whatsapp_web_acknowledged_at: null }, client)).toBe(
      "whatsapp_web_not_acknowledged",
    );
  });

  it("keeps the order of the backend: blocked wins over not acknowledged and over quota", () => {
    const all = {
      ...org,
      whatsapp_web_allowed: false,
      whatsapp_web_acknowledged_at: null,
      instance_count: 5,
    };
    expect(creationProblem(all, client)).toBe("whatsapp_web_not_allowed");
    expect(creationProblem({ ...all, whatsapp_web_allowed: true }, client)).toBe(
      "whatsapp_web_not_acknowledged",
    );
    expect(
      creationProblem(
        { ...all, whatsapp_web_allowed: true, whatsapp_web_acknowledged_at: "x" },
        client,
      ),
    ).toBe("quota_exceeded");
  });

  it("rule 3: the count of the organization reaches its limit", () => {
    expect(creationProblem({ ...org, instance_count: 4 }, client)).toBeNull();
    expect(creationProblem({ ...org, instance_count: 5 }, client)).toBe("quota_exceeded");
    expect(creationProblem({ ...org, instance_count: 9 }, client)).toBe("quota_exceeded");
  });

  it("a limit of 0 of the organization allows none", () => {
    expect(creationProblem({ ...org, max_instances: 0, instance_count: 0 }, client)).toBe(
      "quota_exceeded",
    );
  });

  it("the count of the client reaches its own limit", () => {
    expect(creationProblem(org, { ...client, max_instances: 2, instance_count: 1 })).toBeNull();
    expect(creationProblem(org, { ...client, max_instances: 2, instance_count: 2 })).toBe(
      "quota_exceeded",
    );
  });

  it("a limit of 0 of the client means no limit of its own", () => {
    expect(creationProblem(org, { ...client, max_instances: 0, instance_count: 100 })).toBeNull();
  });

  it("can_create_instances false does not stop an owner", () => {
    const noCreate = { ...client, can_create_instances: false };
    expect(creationProblem(org, noCreate)).toBeNull();
  });
});
