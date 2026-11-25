import { describe, expect, it } from "vitest";
import type { components } from "../api/generated/types.ts";
import type { Failure } from "../api/request.ts";
import { explain } from "./errors.ts";

type ProblemCode = components["schemas"]["Problem"]["code"];
type Ctx = Parameters<typeof explain>[1];

const problem = (code: string, extra: Partial<Failure> = {}): Failure =>
  ({
    kind: "problem",
    status: 400,
    code,
    detail: "The detail.",
    fields: [],
    requestId: "req-1",
    ...extra,
  }) as Failure;

const texts = (code: string, ctx: Partial<Ctx> = {}, extra: Partial<Failure> = {}) => {
  const e = explain(problem(code, extra), { operationId: "getMe", role: "client", ...ctx });
  return [e.cause, e.nextStep];
};

// The compiler fails here when a code of the API is missing, so the test lists them all.
const all: Record<ProblemCode, true> = {
  api_key_not_allowed: true,
  bulk_outcome_unknown: true,
  change_outcome_unknown: true,
  client_has_instances: true,
  conflict: true,
  core_rejected_token: true,
  core_unavailable: true,
  csrf_check_failed: true,
  default_client: true,
  duplicate_message: true,
  email_in_use: true,
  idempotency_key_conflict: true,
  idempotency_key_outcome_unknown: true,
  instance_connected: true,
  instance_creation_not_allowed: true,
  instance_in_error: true,
  instance_not_connected: true,
  instance_not_ready: true,
  instance_queue_full: true,
  internal: true,
  invalid_request: true,
  not_found: true,
  owner_only: true,
  pairing_in_progress: true,
  pairing_not_supported: true,
  phone_number_id_in_use: true,
  quota_exceeded: true,
  rate_limited: true,
  send_in_progress: true,
  send_outcome_unknown: true,
  token_invalid: true,
  too_many_messages: true,
  unauthenticated: true,
  whatsapp_web_not_acknowledged: true,
  whatsapp_web_not_allowed: true,
};

const operations = [
  "getMe",
  "createInstance",
  "pairInstance",
  "updateInstance",
  "removeInstance",
  "cancelPairing",
  "login",
  "acceptInvitation",
  "confirmPasswordReset",
  "inviteClientUser",
  "listClients",
] as const;
const roles = ["owner", "client", null] as const;

describe("explain: every code has a text", () => {
  for (const code of Object.keys(all))
    for (const operationId of operations)
      for (const role of roles)
        it(`${code} ${operationId} ${String(role)}`, () => {
          const e = explain(problem(code), { operationId, role });
          expect(e.cause).not.toBe("");
          expect(e.nextStep).not.toBe("");
        });
});

describe("explain: the texts of the panel", () => {
  it("invalid_request", () => {
    expect(texts("invalid_request")).toEqual([
      "Some values are not valid.",
      "Correct the marked fields.",
    ]);
  });
  it("unauthenticated of login and of any other operation", () => {
    expect(texts("unauthenticated", { operationId: "login", role: null })).toEqual([
      "The email or the password is not correct.",
      'Try again, or select "Forgot password".',
    ]);
    expect(texts("unauthenticated")).toEqual(["Your login ended.", "Log in again."]);
  });
  it("csrf_check_failed by role", () => {
    const cause = "The address of this page is not the address of Calyx.";
    expect(texts("csrf_check_failed", { role: "owner" })).toEqual([
      cause,
      "Open Calyx at the address in `PUBLIC_URL`.",
    ]);
    expect(texts("csrf_check_failed", { role: "client" })).toEqual([
      cause,
      "Use the link of your mail.",
    ]);
    expect(texts("csrf_check_failed", { role: null })).toEqual([
      cause,
      "Use the link of your mail.",
    ]);
  });
  it("owner_only and instance_creation_not_allowed name the Manager", () => {
    expect(texts("owner_only", { manager: "Bakery Lluna" })).toEqual([
      "Only the organization admin can do this.",
      "Ask Bakery Lluna.",
    ]);
    expect(texts("instance_creation_not_allowed")).toEqual([
      "You cannot connect numbers now.",
      "Ask the organization admin to allow it.",
    ]);
  });
  it("whatsapp_web_not_allowed by role", () => {
    const cause = "WhatsApp Web is blocked for this organization.";
    expect(texts("whatsapp_web_not_allowed", { role: "owner" })).toEqual([
      cause,
      "Ask the server admin.",
    ]);
    expect(texts("whatsapp_web_not_allowed", { manager: "Acme" })).toEqual([cause, "Ask Acme."]);
  });
  it("whatsapp_web_not_acknowledged by role", () => {
    const cause = "The terms of WhatsApp Web are not accepted yet.";
    expect(texts("whatsapp_web_not_acknowledged", { role: "owner" })).toEqual([
      cause,
      "Read and accept the terms.",
    ]);
    expect(texts("whatsapp_web_not_acknowledged", { manager: "Acme" })).toEqual([
      cause,
      "Ask Acme.",
    ]);
  });
  it("not_found of cancelPairing and of any other operation", () => {
    expect(texts("not_found", { operationId: "cancelPairing" })).toEqual([
      "This attempt already ended.",
      "Look at the state now.",
    ]);
    expect(texts("not_found")).toEqual([
      "This does not exist, or it was removed.",
      "Go to the list.",
    ]);
  });
  it("quota_exceeded by role", () => {
    const cause = "There is no free place for a number.";
    expect(texts("quota_exceeded", { role: "owner" })).toEqual([
      cause,
      "Raise the limit of the client. When the organization is full, ask the server admin.",
    ]);
    expect(texts("quota_exceeded", { manager: "Acme" })).toEqual([cause, "Ask Acme."]);
  });
  it.each([
    [
      "pairing_in_progress",
      "A phone is being linked to this number now.",
      "Finish the link of the phone, or cancel it. This page shows the steps.",
    ],
    [
      "pairing_not_supported",
      "This number uses the Meta API. It has no phone to link.",
      "Change its credentials with the API.",
    ],
    [
      "instance_in_error",
      "WhatsApp refused the session of this number.",
      "Remove the number and connect it again. This does not remove a ban.",
    ],
    ["instance_connected", "This number does not need a new link.", "Look at its state now."],
    ["client_has_instances", "This client still has numbers.", "Remove its numbers first."],
    [
      "default_client",
      "This is your own group of numbers.",
      "It has no user, and it cannot be deleted.",
    ],
    [
      "email_in_use",
      "This email already has an account.",
      "Log in, or ask for an invitation to another email.",
    ],
    [
      "change_outcome_unknown",
      "Calyx did not confirm the change.",
      "Look at the state now, before you try again.",
    ],
  ])("%s", (code, cause, next) => {
    expect(texts(code)).toEqual([cause, next]);
  });
  it("token_invalid by operation", () => {
    const cause = "This link was used, replaced or cancelled, or it expired.";
    expect(texts("token_invalid", { operationId: "acceptInvitation", role: null })).toEqual([
      cause,
      "Log in, or ask the person who invited you for a new one.",
    ]);
    expect(texts("token_invalid", { operationId: "lookupInvitation", role: null })[1]).toBe(
      "Log in, or ask the person who invited you for a new one.",
    );
    expect(texts("token_invalid", { operationId: "confirmPasswordReset", role: null })).toEqual([
      cause,
      'Select "Forgot password".',
    ]);
    expect(texts("token_invalid", { operationId: "lookupPasswordReset", role: null })[1]).toBe(
      'Select "Forgot password".',
    );
  });
  it("rate_limited uses Retry-After", () => {
    expect(texts("rate_limited", {}, { retryAfter: 30 })).toEqual([
      "Too many attempts.",
      "Wait 30 seconds and try again.",
    ]);
    expect(texts("rate_limited", {}, { retryAfter: 1 })[1]).toBe("Wait 1 second and try again.");
    expect(texts("rate_limited")[1]).toBe("Wait some seconds and try again.");
  });
  it("internal and the general text name who gets the details", () => {
    const next = (who: string) =>
      `Try again. If it continues, copy the "Technical details". Send them to ${who}.`;
    expect(texts("internal", { role: "owner" })).toEqual([
      "Calyx had an error.",
      next("the server admin"),
    ]);
    expect(texts("internal", { manager: "Acme" })).toEqual(["Calyx had an error.", next("Acme")]);
    for (const code of ["send_outcome_unknown", "api_key_not_allowed", "a_future_code"])
      expect(texts(code, { role: "owner" })).toEqual([
        "Calyx refused this.",
        next("the server admin"),
      ]);
  });
  it("core_rejected_token by role", () => {
    expect(texts("core_rejected_token", { role: "owner" })).toEqual([
      "The backend and the core do not have the same token.",
      "Check `CORE_TOKEN` of both.",
    ]);
    expect(texts("core_rejected_token", { manager: "Acme" })).toEqual([
      "Calyx has a fault in its settings.",
      "Contact Acme.",
    ]);
  });
  it("core_unavailable", () => {
    const base = "Wait until the top of the page shows that Calyx works. Then do it again.";
    expect(texts("core_unavailable")).toEqual(["Calyx cannot reach WhatsApp now.", base]);
    expect(texts("core_unavailable", { role: "owner" })[1]).toBe(
      `${base} Check that the core runs.`,
    );
  });
  it("pairing codes", () => {
    const pair = (code: string, reconnect?: boolean) => {
      const e = explain({ kind: "pairing", code, detail: "d" } as never, {
        operationId: "followPairing",
        role: "client",
        ...(reconnect === undefined ? {} : { reconnect }),
      });
      return [e.cause, e.nextStep];
    };
    expect(pair("pairing_expired", false)).toEqual([
      "The code expired. Nobody used it in time. Nothing was saved.",
      "Try again.",
    ]);
    expect(pair("pairing_expired", true)).toEqual([
      "The code expired. Nobody used it in time. Your settings are kept.",
      "Try again.",
    ]);
    expect(pair("pairing_failed")).toEqual([
      "The link did not finish.",
      "Try again. With a code, check the phone number.",
    ]);
    expect(pair("pairing_cancelled")).toEqual([
      "The attempt was cancelled.",
      "Start again when you want.",
    ]);
  });
  it("a pairing failure gives its code and detail as details", () => {
    const e = explain({ kind: "pairing", code: "pairing_failed", detail: "No luck." } as never, {
      operationId: "followPairing",
      role: "client",
    });
    expect(e.details).toEqual({ code: "pairing_failed", detail: "No luck." });
  });
});

describe("explain: conflict by operation", () => {
  const c = (operationId: string) => texts("conflict", { operationId: operationId as never });
  it("createInstance and pairInstance", () => {
    expect(c("createInstance")).toEqual([
      "WhatsApp or the core refused this start.",
      "With a code, check the phone number. If it is correct, choose another linked device in the optional settings. If you cancelled an attempt, wait some seconds and try again.",
    ]);
    expect(c("pairInstance")).toEqual([
      "This number cannot be linked now.",
      "With a code, check the phone number. If you cancelled an attempt, wait some seconds and try again.",
    ]);
  });
  it("updateInstance and removeInstance", () => {
    const expected = [
      "The number changed, or another change of it runs.",
      "Look at its state now and try again.",
    ];
    expect(c("updateInstance")).toEqual(expected);
    expect(c("removeInstance")).toEqual(expected);
  });
  it("inviteClientUser and acceptInvitation", () => {
    expect(c("inviteClientUser")).toEqual([
      "This client already has a user, or another invitation is being made.",
      "Look at the client now.",
    ]);
    expect(c("acceptInvitation")).toEqual([
      "The client already has an active user.",
      "Log in, or ask the person who invited you.",
    ]);
  });
  it("any other operation", () => {
    expect(c("updateClient")).toEqual(["The data changed.", "Look at it now and try again."]);
  });
});

describe("explain: failures that are not a problem", () => {
  const f = (failure: Failure, role: Ctx["role"]) => {
    const e = explain(failure, { operationId: "getMe", role });
    return [e.cause, e.nextStep];
  };
  it("unreachable, logged in and not", () => {
    expect(f({ kind: "unreachable" }, "owner")).toEqual([
      "Calyx does not answer.",
      "This page reads the data again by itself.",
    ]);
    expect(f({ kind: "unreachable" }, null)).toEqual([
      "Calyx does not answer.",
      "Check your network, then try again.",
    ]);
  });
  it("timeout, logged in and not", () => {
    expect(f({ kind: "timeout" }, "client")).toEqual([
      "Calyx did not answer in time.",
      "Look at the state now, before you try again.",
    ]);
    expect(f({ kind: "timeout" }, null)).toEqual(["Calyx did not answer in time.", "Try again."]);
  });
  it("have no technical details", () => {
    expect(explain({ kind: "timeout" }, { operationId: "getMe", role: null }).details).toEqual({});
  });
});

describe("explain: details", () => {
  it("gives the code, the detail and the request id", () => {
    const e = explain(problem("internal"), { operationId: "getMe", role: "owner" });
    expect(e.details).toEqual({ code: "internal", detail: "The detail.", requestId: "req-1" });
  });
  it("leaves out an empty detail and an empty request id", () => {
    const e = explain(problem("internal", { detail: "", requestId: "" }), {
      operationId: "getMe",
      role: null,
    });
    expect(e.details).toEqual({ code: "internal" });
  });
  it("an unknown code keeps its request id", () => {
    const e = explain(problem("future"), { operationId: "getMe", role: null });
    expect(e.details.requestId).toBe("req-1");
  });
});

describe("explain: the texts of keys, webhooks and bulk jobs", () => {
  const textsOf = (operationId: Ctx["operationId"], code: string) => {
    const e = explain(problem(code), { operationId, role: "owner" });
    return [e.cause, e.nextStep];
  };

  it.each([
    [
      "listBulkJobs",
      "core_unavailable",
      "Calyx cannot read the bulk jobs now.",
      "Select Check again later.",
    ],
    [
      "getBulkJob",
      "core_unavailable",
      "Calyx cannot read the bulk jobs now.",
      "Select Check again later.",
    ],
    [
      "cancelBulkJob",
      "core_unavailable",
      "Calyx could not stop the job now.",
      "Select Check again. If the job still sends, select Stop sending again.",
    ],
    ["cancelBulkJob", "not_found", "This bulk job does not exist now.", "Select Check again."],
    ["revokeAPIKey", "not_found", "This key does not exist now. It was revoked.", ""],
    ["removeClientAccess", "not_found", "This client has no user now.", ""],
  ] as const)("%s %s", (operationId, code, cause, next) => {
    expect(textsOf(operationId, code)).toEqual([cause, next]);
  });

  it("keep the general text for the other operations", () => {
    expect(textsOf("listInstances", "core_unavailable")[0]).toBe(
      "Calyx cannot reach WhatsApp now.",
    );
    expect(textsOf("getClient", "not_found")).toEqual([
      "This does not exist, or it was removed.",
      "Go to the list.",
    ]);
  });

  it("keep the general text of api_key_not_allowed", () => {
    expect(textsOf("revokeAPIKey", "api_key_not_allowed")).toEqual([
      "Calyx refused this.",
      'Try again. If it continues, copy the "Technical details". Send them to the server admin.',
    ]);
  });
});
