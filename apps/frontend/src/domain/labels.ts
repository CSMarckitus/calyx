import type { Health } from "../api/health.ts";

// The fields that `describe` reads. The values are plain strings, because a newer backend can
// send a value that this page does not know.
export type InstanceRow = {
  state: string;
  status: string | null;
  enabled: boolean;
  send_only: boolean;
  provider: string;
  status_stale: boolean;
  status_changed_at: string | null;
  alert: { kind: string; since: string | null } | null;
};

export type Action = null | "continue" | "remove" | "enable" | "reconnect";
export type Filter = null | "person" | "wait" | "ok" | "off";
export type Color = "ok" | "wait" | "bad" | "idle";

export type Described = {
  label: string;
  color: Color;
  cause: string;
  // Start of the state, in ms. Absent when Calyx does not know it.
  since?: number;
  action: Action;
  filter: Filter;
  // Only for "Unknown": the label of the last known state, and the last good check.
  last?: { label: string; at?: number };
};

type Known = Omit<Described, "last">;

const readySeconds = 60;
const contactMinutes = 5;

// Who reads the text: an organization admin ("owner") contacts the server admin, a client user
// contacts the organization.
export type Reader = { viewer: "owner" | "client"; manager: string };
const contactText = (r: Reader) =>
  `If it continues for more than ${contactMinutes} minutes, contact ${
    r.viewer === "owner" ? "the server admin" : r.manager
  }.`;

const text = {
  linking: "Linking of this number to a phone is not finished.",
  removing: "Calyx is removing this number. This can take some seconds.",
  missing: "Calyx cannot find this number. Remove it, and connect it again.",
  checking: "Calyx has no state for this number yet.",
  unknown: "Calyx cannot check this number now.",
  offLost:
    "Calyx access is off, and the phone unlinked Calyx. Turn on the access first. Then link the phone again.",
  off: "Calyx access is off. Your phone works with WhatsApp as always, but Calyx does not use this number.",
  ready: "This number sends when there is a message. It rests in between.",
  connected: "Messages can go out.",
  reconnecting:
    "The connection to WhatsApp dropped. Calyx is trying again. You do not need to do anything.",
  meta: "The credentials of Meta expired or changed. Change them with the API.",
  lost: "WhatsApp unlinked this number. Link the phone again to continue. Your settings are kept.",
  problem:
    "WhatsApp refused this session. WhatsApp may have banned the number for a time. A new connection does not remove a ban. To use the number again, remove it and connect it. Its webhooks and bulk jobs are lost.",
};

// "Not well since": the alert time of any kind, else the last change of the status. The status
// alone restarts at each change between CONNECTING and DISCONNECTED, so the alert wins.
function startOf(i: InstanceRow): number | undefined {
  const t = i.alert?.since ?? i.status_changed_at;
  if (t === null || t === undefined) return undefined;
  const ms = Date.parse(t);
  return Number.isNaN(ms) ? undefined : ms;
}

function known(i: InstanceRow, now: number, reader: Reader): Known {
  const start = startOf(i);
  const since = start === undefined ? {} : { since: start };
  const row = (
    label: string,
    color: Color,
    cause: string,
    action: Action,
    filter: Filter,
  ): Known => ({ label, color, cause, action, filter, ...since });
  // These states have no connection, so no start of a connection.
  const bare = (label: string, color: Color, cause: string, action: Action, filter: Filter) => ({
    label,
    color,
    cause,
    action,
    filter,
  });

  switch (i.state) {
    case "creating":
      return bare("Linking", "wait", text.linking, "continue", "wait");
    case "removing":
      return bare("Removing", "idle", text.removing, null, null);
    case "missing":
      return bare("Not found", "bad", text.missing, "remove", "person");
    case "active":
      break;
    default:
      return bare("Checking", "idle", text.checking, null, null);
  }

  if (!i.enabled) {
    return i.status === "NEEDS_AUTH"
      ? row("Disconnected", "bad", text.offLost, "enable", "off")
      : row("Not in use", "idle", text.off, "enable", "off");
  }
  if (i.status === "CONNECTED") {
    return i.send_only
      ? row("Ready", "ok", text.ready, null, "ok")
      : row("Connected", "ok", text.connected, null, "ok");
  }
  if (i.status === "CONNECTING" || i.status === "DISCONNECTED") {
    const elapsed = start === undefined ? undefined : now - start;
    if (i.send_only && elapsed !== undefined && elapsed < readySeconds * 1000)
      return row("Ready", "ok", text.ready, null, "ok");
    const long = elapsed !== undefined && elapsed >= contactMinutes * 60_000;
    const cause = long ? `${text.reconnecting} ${contactText(reader)}` : text.reconnecting;
    return row("Reconnecting", "wait", cause, null, "wait");
  }
  if (i.status === "NEEDS_AUTH") {
    return i.provider === "META_CLOUD"
      ? row("New credentials needed", "bad", text.meta, null, "person")
      : row("Disconnected", "bad", text.lost, "reconnect", "person");
  }
  if (i.status === "ERROR") return row("Problem", "bad", text.problem, "remove", "person");
  return bare("Checking", "idle", text.checking, null, null);
}

// The one place that turns the data of an instance into words. `now` is the time of the
// last health answer, so a label changes with no event.
export function describe(
  instance: InstanceRow,
  health: Pick<Health, "state" | "dataIsFresh" | "lastGoodAt">,
  now: number,
  reader: Reader,
): Described {
  const result = known(instance, now, reader);
  const unknown =
    instance.status_stale ||
    !health.dataIsFresh ||
    health.state === "backend" ||
    health.state === "database";
  if (!unknown) return result;
  return {
    label: "Unknown",
    color: "idle",
    cause: text.unknown,
    action: null,
    filter: null,
    last: {
      label: result.label,
      ...(health.lastGoodAt !== undefined ? { at: health.lastGoodAt } : {}),
    },
  };
}
