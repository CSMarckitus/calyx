import { type ReactNode, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useNavigate } from "react-router";
import { DoneStep, type MailTo } from "@/components/connect/done-step";
import { LinkResult, LinkStep, Waiting } from "@/components/connect/link-step";
import { type Method, MethodStep, type PhoneDraft } from "@/components/connect/method-step";
import { NameStep } from "@/components/connect/name-step";
import { OptionsStep, optionsText } from "@/components/connect/options-step";
import { UseStep } from "@/components/connect/use-step";
import { Failure } from "@/components/failure";
import { type Instance, roleOf, StateWord, type Viewing } from "@/components/instance-card";
import { Notice } from "@/components/notice";
import { Button } from "@/components/ui/button";
import { Trace, type TraceStep } from "@/components/ui/trace";
import { Words } from "@/components/words";
import { useHealth } from "../api/health.ts";
import { autoMethod, createPairing, type PairingView } from "../api/pairing.ts";
import { refreshNow, useInstances, useMe } from "../api/queries.ts";
import { useSession } from "../app/session.tsx";
import { dialCode } from "../domain/countries.ts";
import {
  type Draft,
  endDraft,
  newDraft,
  readDraft,
  saveDraft,
  type Where,
} from "../domain/draft.ts";
import { describe } from "../domain/labels.ts";
import { autoLocale, splitLocale } from "../domain/locale.ts";
import { fullNumber, isPhoneValid, showNumber } from "../domain/phone.ts";
import { CreationReason } from "./instances.tsx";

type Before = "name" | "use" | "options" | "method";
type StepId = Before | "link" | "done";

const titles: Record<StepId, string> = {
  name: "Name",
  use: "What is it for?",
  options: "Optional settings",
  method: "Where is WhatsApp?",
  link: "Link the phone",
  done: "Connected",
};

// A new number has six steps. A reconnection has the last three.
const newSteps: StepId[] = ["name", "use", "options", "method", "link", "done"];
const reconnectSteps: StepId[] = ["method", "link", "done"];

// The views of the steps before Start: no attempt runs, so a step can change.
const beforeStart = new Set<PairingView["step"]>(["ready", "lostStart", "refused"]);

function firstPhone(draft: Draft): PhoneDraft {
  if (draft.phone) return draft.phone;
  const { region } = splitLocale(autoLocale());
  return { country: dialCode(region) ? region : "", text: "" };
}

const phoneNumber = (phone: PhoneDraft | null) => {
  if (!phone) return null;
  const number = fullNumber(phone.text, dialCode(phone.country));
  return isPhoneValid(number) ? showNumber(number) : null;
};

export type ConnectKind = { reconnect: boolean };

// The steps of "Connect a number" and "Reconnect", in the drawer of the list. The drawer
// keeps this component when the address changes from the client to the new instance, so the
// pairing and its view stay.
export function ConnectDrawer({
  id,
  clientId,
  clientName,
  problem,
  row,
  kind,
  viewing,
  now,
  onDone,
}: {
  // Absent for a new number before Start.
  id: string | undefined;
  clientId: string | undefined;
  // The name of the client, for an owner: "For Shop".
  clientName: string | undefined;
  // Why the person cannot connect a number in this client, for a new number.
  problem: string | null;
  // The row of the list for `id`.
  row: Instance | undefined;
  kind: ConnectKind;
  viewing: Viewing;
  now: number;
  // The drawer shows the instance.
  onDone: () => void;
}) {
  const navigate = useNavigate();
  const session = useSession();
  const health = useHealth();
  const instances = useInstances();
  const me = useMe();
  const role = roleOf(viewing);
  const { reconnect } = kind;
  const userId = viewing.userId;

  // The router can give a new function at each render. The pairing keeps the last one.
  const go = useRef(navigate);
  go.current = navigate;

  const [pairing] = useState(() =>
    createPairing({
      userId,
      clientId,
      instanceId: id,
      reconnect,
      // With no row the page does not know the stored locale, so it sends none.
      instanceLocale: reconnect && row ? row.locale : undefined,
      onUnauthorized: session.onUnauthorized,
      navigate: (to) =>
        void go.current(
          "client" in to ? `/instances/new?client=${to.client}` : `/instances/${to.instance}`,
          { replace: true },
        ),
    }),
  );
  const view = useSyncExternalStore(pairing.subscribe, pairing.view);

  const where = (): Where | undefined => {
    const instance = pairing.instanceId();
    if (instance !== undefined) return { instance };
    return clientId === undefined ? undefined : { client: clientId };
  };

  const [draft, setDraft] = useState<Draft>(() => {
    const at = where();
    const stored = at && readDraft(at, userId);
    if (stored) return stored;
    const base = newDraft({
      userId,
      clientId: row?.client_id ?? clientId ?? "",
      kind: reconnect ? "reconnect" : "new",
    });
    return { ...base, name: row?.display_name ?? "", method: autoMethod() };
  });
  const [step, setStep] = useState<Before>(() =>
    reconnect || draft.name !== "" ? "method" : "name",
  );
  const method: Method = draft.method ?? autoMethod();
  const phone = firstPhone(draft);

  useEffect(() => {
    pairing.open();
    return () => pairing.close();
  }, [pairing]);

  // Each read of the list that ended: a drawer of an instance in `creating` attaches, and an
  // attempt that ends sees its row go away.
  const readAt = instances.dataUpdatedAt;
  const items = instances.data?.items;
  useEffect(() => {
    if (readAt === 0) return;
    const instance = pairing.instanceId();
    pairing.listRead(items?.find((i) => i.id === instance));
  }, [pairing, readAt, items]);

  // A refusal, or a start with no answer: the page reads the data again and shows it.
  const shown = view.step;
  useEffect(() => {
    if (shown === "lostStart" || shown === "refused") refreshNow();
  }, [shown]);

  // A cancelled new number goes away. A cancelled reconnection shows the instance.
  useEffect(() => {
    if (shown !== "cancelled") return;
    if (reconnect) onDone();
    else void go.current("/", { replace: true });
  }, [shown, reconnect, onDone]);

  function update(patch: Partial<Draft>) {
    const next = { ...draft, ...patch };
    setDraft(next);
    const at = where();
    if (at && beforeStart.has(pairing.view().step)) saveDraft(at, { ...next, startedAt: null });
  }

  function cancelBefore() {
    const at = where();
    if (at) endDraft(at);
    void navigate("/", { replace: true });
  }

  async function switchMethod() {
    const other: Method = method === "code" ? "qr" : "code";
    if (await pairing.switchMethod()) {
      setStep("method");
      update({ method: other });
    }
  }

  function retry() {
    pairing.retry();
    setStep("method");
  }

  const order = reconnect ? reconnectSteps : newSteps;
  const before = beforeStart.has(view.step);
  const linkFailed = view.step === "failed" || view.step === "notLinked" || view.step === "removed";
  const complete = view.step === "connected" || view.step === "linked";
  const currentId: StepId = before ? step : complete ? "done" : "link";
  const current = order.indexOf(currentId);
  const number = phoneNumber(draft.phone);
  const to: MailTo = {
    viewer: viewing.viewer,
    email: me.data?.user.email ?? "",
    manager: viewing.manager,
  };

  // The name and the phone number keep their form under a translator.
  const values: Partial<Record<StepId, ReactNode>> = {
    name: <span translate="no">{draft.name}</span>,
    use: draft.sendOnly ? "Only send" : "Send and receive",
    options: optionsText(draft.locale, draft.device),
    method:
      method === "code" ? (
        <Words
          text={`On this device${number ? `, ${number}` : ""}`}
          keep={number ? [number] : []}
        />
      ) : (
        "On another phone"
      ),
  };

  function body(s: StepId) {
    if (s === "name")
      return (
        <NameStep
          name={draft.name}
          onCancel={cancelBefore}
          onContinue={(name) => {
            update({ name });
            setStep("use");
          }}
        />
      );
    if (s === "use")
      return (
        <UseStep
          sendOnly={draft.sendOnly}
          onChoose={(sendOnly) => update({ sendOnly })}
          onContinue={() => setStep("options")}
        />
      );
    if (s === "options")
      return (
        <OptionsStep
          locale={draft.locale}
          device={draft.device}
          onChange={(locale, device) => update({ locale, device })}
          onContinue={() => setStep("method")}
        />
      );
    if (s === "method") {
      let notice = null;
      if (view.step === "lostStart")
        notice = (
          <Notice tone="wait" title="Calyx did not confirm the start.">
            If the new number is in the list, select 'Linking. Continue'. If not, try again.
          </Notice>
        );
      else if (view.step === "refused")
        notice = (
          <Failure
            failure={view.failure}
            context={{ operationId: view.operationId, role, manager: viewing.manager, reconnect }}
          />
        );
      return (
        <MethodStep
          method={method}
          phone={phone}
          disabled={viewing.down}
          notice={notice}
          onChange={(m, p) => update({ method: m, phone: p })}
          onStart={() => void pairing.start({ ...draft, method, phone, startedAt: null })}
        />
      );
    }
    if (s === "link") return linkBody();
    return doneBody();
  }

  function linkBody() {
    switch (view.step) {
      case "starting":
      case "waiting":
      case "code":
      case "qr":
      case "checking":
      case "cancelling":
        return (
          <LinkStep
            view={view}
            method={method}
            number={number}
            onOther={() => void switchMethod()}
            onCancel={() => void pairing.cancel()}
          />
        );
      case "failed":
        return (
          <LinkResult
            failure={
              <Failure
                failure={view.failure}
                context={{
                  operationId: "followPairing",
                  role,
                  manager: viewing.manager,
                  reconnect,
                }}
              />
            }
            onRetry={retry}
            onCancel={() => void pairing.cancel()}
          />
        );
      case "notLinked":
        return (
          <LinkResult
            title="The phone was not linked."
            text="Nothing was saved."
            onRetry={retry}
            onCancel={cancelBefore}
          />
        );
      case "removed":
        return <LinkResult title="This number was removed." />;
      case "ending":
        return <Waiting title="Calyx ends this attempt. It can take 5 minutes." />;
      case "instance": {
        const d = describe(view.instance, health, now, viewing);
        return (
          <div className="grid justify-items-start gap-3">
            <div role="status">
              <StateWord color={d.color} className="text-[1.125rem]">
                {d.label}
              </StateWord>
            </div>
            <p className="m-0">{d.cause}</p>
            <Button onClick={onDone}>Done</Button>
          </div>
        );
      }
      default:
        return null;
    }
  }

  function doneBody() {
    const linked =
      view.step === "linked" ? describe(view.instance, health, now, viewing) : undefined;
    return (
      <DoneStep
        name={draft.name || row?.display_name || ""}
        linked={linked}
        mail={health.mail}
        to={to}
        onDone={onDone}
      />
    );
  }

  const steps: TraceStep[] = order.map((s, i) => ({
    id: s,
    title: titles[s],
    value: values[s],
    // "Change" shows on a finished step before Start only.
    action:
      before && i < current && s !== "link" && s !== "done" ? (
        <Button
          variant="link"
          className="min-h-6 px-0 font-medium"
          onClick={() => setStep(s as Before)}
        >
          Change
        </Button>
      ) : undefined,
    children: i === current ? body(s) : undefined,
  }));

  const name = draft.name || row?.display_name || "";
  const blocked =
    !reconnect && pairing.instanceId() === undefined && view.step === "ready" && problem !== null;

  return (
    <div className="grid gap-5">
      <div className="grid gap-1">
        <h2 tabIndex={-1}>
          {reconnect ? (
            <>
              <span>Reconnect</span> <span translate="no">{name}</span>
            </>
          ) : (
            "Connect a WhatsApp number"
          )}
        </h2>
        {!reconnect && clientName !== undefined && (
          <p className="m-0 text-muted-foreground">
            <span>For</span> <span translate="no">{clientName}</span>
          </p>
        )}
      </div>
      {blocked && problem !== null ? (
        <CreationReason code={problem} who={role} manager={viewing.manager} />
      ) : (
        <>
          <p className="m-0 text-[0.9375rem] text-muted-foreground">{`Step ${current + 1} of ${order.length}`}</p>
          <Trace steps={steps} current={current} complete={complete} failed={linkFailed} />
        </>
      )}
    </div>
  );
}
