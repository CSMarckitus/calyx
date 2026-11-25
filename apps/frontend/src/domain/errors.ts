import type { components } from "../api/generated/types.ts";
import type { OperationId } from "../api/operations.ts";
import type { Failure } from "../api/request.ts";

type ProblemCode = components["schemas"]["Problem"]["code"];
type PairingCode = components["schemas"]["Failed"]["code"];

export type Role = "owner" | "client" | null;

export type ErrorContext = {
  operationId: OperationId;
  role: Role;
  // The name of the organization. Only a client user reads it.
  manager?: string;
  // The pairing was a reconnection (true) or a new number (false).
  reconnect?: boolean;
};

export type PairingFailure = { kind: "pairing"; code: PairingCode; detail: string };

export type Explained = {
  cause: string;
  nextStep: string;
  // For "Technical details". An empty value is absent.
  details: { code?: string; detail?: string; requestId?: string };
};

type Text = { cause: string; nextStep: string };

// What a text can read. A page with no role (a public path) gets the text of the client user.
type Ask = {
  operationId: string;
  owner: boolean;
  manager: string;
  retryAfter: number | undefined;
  reconnect: boolean | undefined;
};

const serverAdmin = "the server admin";
// The name of the organization is not known yet.
export const unknownManager = "the organization admin";
const general = (a: Ask) => (a.owner ? serverAdmin : a.manager);
const send = (a: Ask) =>
  `Try again. If it continues, copy the "Technical details". Send them to ${general(a)}.`;
const refused = (a: Ask): Text => ({ cause: "Calyx refused this.", nextStep: send(a) });

const invitation = ["acceptInvitation", "lookupInvitation"];
const reset = ["confirmPasswordReset", "lookupPasswordReset"];

// Typed with the codes of the API: a code with no text does not compile.
const problems: Record<ProblemCode, (a: Ask) => Text> = {
  invalid_request: () => ({
    cause: "Some values are not valid.",
    nextStep: "Correct the marked fields.",
  }),
  unauthenticated: (a) =>
    a.operationId === "login"
      ? {
          cause: "The email or the password is not correct.",
          nextStep: 'Try again, or select "Forgot password".',
        }
      : { cause: "Your login ended.", nextStep: "Log in again." },
  csrf_check_failed: (a) => ({
    cause: "The address of this page is not the address of Calyx.",
    nextStep: a.owner ? "Open Calyx at the address in `PUBLIC_URL`." : "Use the link of your mail.",
  }),
  owner_only: (a) => ({
    cause: "Only the organization admin can do this.",
    nextStep: `Ask ${a.manager}.`,
  }),
  instance_creation_not_allowed: (a) => ({
    cause: "You cannot connect numbers now.",
    nextStep: `Ask ${a.manager} to allow it.`,
  }),
  whatsapp_web_not_allowed: (a) => ({
    cause: "WhatsApp Web is blocked for this organization.",
    nextStep: a.owner ? `Ask ${serverAdmin}.` : `Ask ${a.manager}.`,
  }),
  whatsapp_web_not_acknowledged: (a) => ({
    cause: "The terms of WhatsApp Web are not accepted yet.",
    nextStep: a.owner ? "Read and accept the terms." : `Ask ${a.manager}.`,
  }),
  not_found: (a) => {
    switch (a.operationId) {
      case "cancelPairing":
        return { cause: "This attempt already ended.", nextStep: "Look at the state now." };
      case "cancelBulkJob":
        return { cause: "This bulk job does not exist now.", nextStep: "Select Check again." };
      case "revokeAPIKey":
        return { cause: "This key does not exist now. It was revoked.", nextStep: "" };
      case "removeClientAccess":
        return { cause: "This client has no user now.", nextStep: "" };
      default:
        return { cause: "This does not exist, or it was removed.", nextStep: "Go to the list." };
    }
  },
  quota_exceeded: (a) => ({
    cause: "There is no free place for a number.",
    nextStep: a.owner
      ? `Raise the limit of the client. When the organization is full, ask ${serverAdmin}.`
      : `Ask ${a.manager}.`,
  }),
  pairing_in_progress: () => ({
    cause: "A phone is being linked to this number now.",
    nextStep: "Finish the link of the phone, or cancel it. This page shows the steps.",
  }),
  pairing_not_supported: () => ({
    cause: "This number uses the Meta API. It has no phone to link.",
    nextStep: "Change its credentials with the API.",
  }),
  instance_in_error: () => ({
    cause: "WhatsApp refused the session of this number.",
    nextStep: "Remove the number and connect it again. This does not remove a ban.",
  }),
  instance_connected: () => ({
    cause: "This number does not need a new link.",
    nextStep: "Look at its state now.",
  }),
  client_has_instances: () => ({
    cause: "This client still has numbers.",
    nextStep: "Remove its numbers first.",
  }),
  default_client: () => ({
    cause: "This is your own group of numbers.",
    nextStep: "It has no user, and it cannot be deleted.",
  }),
  email_in_use: () => ({
    cause: "This email already has an account.",
    nextStep: "Log in, or ask for an invitation to another email.",
  }),
  token_invalid: (a) => ({
    cause: "This link was used, replaced or cancelled, or it expired.",
    nextStep: invitation.includes(a.operationId)
      ? "Log in, or ask the person who invited you for a new one."
      : reset.includes(a.operationId)
        ? 'Select "Forgot password".'
        : 'For an invitation, log in, or ask the person who invited you for a new one. For a reset, select "Forgot password".',
  }),
  rate_limited: (a) => ({
    cause: "Too many attempts.",
    nextStep:
      a.retryAfter === undefined
        ? "Wait some seconds and try again."
        : `Wait ${a.retryAfter} ${a.retryAfter === 1 ? "second" : "seconds"} and try again.`,
  }),
  internal: (a) => ({ cause: "Calyx had an error.", nextStep: send(a) }),
  core_rejected_token: (a) =>
    a.owner
      ? {
          cause: "The backend and the core do not have the same token.",
          nextStep: "Check `CORE_TOKEN` of both.",
        }
      : {
          cause: "Calyx has a fault in its settings.",
          nextStep: `Contact ${a.manager}.`,
        },
  core_unavailable: (a) => {
    switch (a.operationId) {
      case "listBulkJobs":
      case "getBulkJob":
        return {
          cause: "Calyx cannot read the bulk jobs now.",
          nextStep: "Select Check again later.",
        };
      case "cancelBulkJob":
        return {
          cause: "Calyx could not stop the job now.",
          nextStep: "Select Check again. If the job still sends, select Stop sending again.",
        };
      default:
        return {
          cause: "Calyx cannot reach WhatsApp now.",
          nextStep: `Wait until the top of the page shows that Calyx works. Then do it again.${
            a.owner ? " Check that the core runs." : ""
          }`,
        };
    }
  },
  change_outcome_unknown: () => ({
    cause: "Calyx did not confirm the change.",
    nextStep: "Look at the state now, before you try again.",
  }),
  conflict: (a) => {
    switch (a.operationId) {
      case "createInstance":
        return {
          cause: "WhatsApp or the core refused this start.",
          nextStep:
            "With a code, check the phone number. If it is correct, choose another linked device in the optional settings. If you cancelled an attempt, wait some seconds and try again.",
        };
      case "pairInstance":
        return {
          cause: "This number cannot be linked now.",
          nextStep:
            "With a code, check the phone number. If you cancelled an attempt, wait some seconds and try again.",
        };
      case "updateInstance":
      case "removeInstance":
        return {
          cause: "The number changed, or another change of it runs.",
          nextStep: "Look at its state now and try again.",
        };
      case "inviteClientUser":
        return {
          cause: "This client already has a user, or another invitation is being made.",
          nextStep: "Look at the client now.",
        };
      case "acceptInvitation":
        return {
          cause: "The client already has an active user.",
          nextStep: "Log in, or ask the person who invited you.",
        };
      default:
        return { cause: "The data changed.", nextStep: "Look at it now and try again." };
    }
  },
  // Sends, bulk jobs, API keys and Meta: the panel does not use them.
  api_key_not_allowed: refused,
  bulk_outcome_unknown: refused,
  duplicate_message: refused,
  idempotency_key_conflict: refused,
  idempotency_key_outcome_unknown: refused,
  instance_not_connected: refused,
  instance_not_ready: refused,
  instance_queue_full: refused,
  phone_number_id_in_use: refused,
  send_in_progress: refused,
  send_outcome_unknown: refused,
  too_many_messages: refused,
};

const pairings: Record<PairingCode, (a: Ask) => Text> = {
  pairing_expired: (a) => ({
    cause: `The code expired. Nobody used it in time.${
      a.reconnect === undefined
        ? ""
        : a.reconnect
          ? " Your settings are kept."
          : " Nothing was saved."
    }`,
    nextStep: "Try again.",
  }),
  pairing_failed: () => ({
    cause: "The link did not finish.",
    nextStep: "Try again. With a code, check the phone number.",
  }),
  pairing_cancelled: () => ({
    cause: "The attempt was cancelled.",
    nextStep: "Start again when you want.",
  }),
};

function has<T extends object>(table: T, key: string): key is Extract<keyof T, string> {
  return Object.hasOwn(table, key);
}

const clean = (d: Explained["details"]): Explained["details"] =>
  Object.fromEntries(Object.entries(d).filter(([, v]) => v)) as Explained["details"];

// A cause and a next step for a failure. An unknown code gets the general text, and keeps
// its request id.
export function explain(failure: Failure | PairingFailure, context: ErrorContext): Explained {
  const ask: Ask = {
    operationId: context.operationId,
    owner: context.role === "owner",
    manager: context.manager ?? unknownManager,
    retryAfter: failure.kind === "problem" ? failure.retryAfter : undefined,
    reconnect: context.reconnect,
  };
  const loggedIn = context.role !== null;

  switch (failure.kind) {
    case "unreachable":
      return {
        cause: "Calyx does not answer.",
        nextStep: loggedIn
          ? "This page reads the data again by itself."
          : "Check your network, then try again.",
        details: {},
      };
    case "timeout":
      return {
        cause: "Calyx did not answer in time.",
        nextStep: loggedIn ? "Look at the state now, before you try again." : "Try again.",
        details: {},
      };
    case "pairing": {
      const text = has(pairings, failure.code) ? pairings[failure.code](ask) : refused(ask);
      return {
        ...text,
        details: clean({ code: failure.code, detail: failure.detail }),
      };
    }
    case "problem": {
      const text = has(problems, failure.code) ? problems[failure.code](ask) : refused(ask);
      return {
        ...text,
        details: clean({
          code: failure.code,
          detail: failure.detail,
          requestId: failure.requestId,
        }),
      };
    }
  }
}
