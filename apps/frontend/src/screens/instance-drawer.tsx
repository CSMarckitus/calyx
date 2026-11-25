import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  useLocation,
  useNavigate,
  useOutletContext,
  useParams,
  useSearchParams,
} from "react-router";
import { InstanceCard } from "@/components/instance-card";
import { savedQuestion } from "@/components/shown-once";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { readDraft, startedRecently } from "../domain/draft.ts";
import { BulkJobsView } from "./bulk-jobs.tsx";
import { ConnectDrawer, type ConnectKind } from "./connect-drawer.tsx";
import type { ListOutlet } from "./instances.tsx";
import { views, WebhooksView } from "./webhooks.tsx";

// Under 640 px the drawer comes from the bottom.
const narrow = "(max-width: 639px)";
function useNarrow() {
  return useSyncExternalStore(
    (changed) => {
      const media = matchMedia(narrow);
      media.addEventListener("change", changed);
      return () => media.removeEventListener("change", changed);
    },
    () => matchMedia(narrow).matches,
  );
}

// The drawer of one instance, at the side of the list, or at the bottom on a phone, at
// `/instances/:id`, or its view at `/instances/:id/:view`. "Close" goes to `/`. The list stays
// mounted below, so its position stays, and its live reads also change the drawer.
// `/instances/new?client=` and `/instances/:id` are one route, so the steps of a new number stay
// mounted when the address changes to the new instance.
export function InstanceDrawer() {
  const { id: param, view } = useParams();
  const isNew = param === "new";
  const id = isNew ? undefined : param;
  // A path with a view that does not exist, or with the id `new`, is a page that does not exist.
  const unknownView = view !== undefined && (isNew || !views.includes(view));
  const showView = view !== undefined && !unknownView;
  const [search] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const { rows, viewing, now, single, connectTo } = useOutletContext<ListOutlet>();
  const found = id === undefined ? undefined : rows.find((r) => r.instance.id === id);
  // The client of a new number: from the address, then from its row.
  const target = isNew
    ? connectTo(search.get("client"))
    : found && connectTo(found.instance.client_id);

  // The steps of a pairing show for a new number, after "Reconnect", for a draft that says
  // "started", and for an instance in `creating` ("Continue"). The kind stays until Close.
  const reconnectAsked = (location.state as { reconnect?: boolean } | null)?.reconnect === true;
  const kindNow = (): ConnectKind | null => {
    if (isNew) return { reconnect: false };
    if (id === undefined) return null;
    if (view !== undefined) return null;
    const creating = found?.instance.state === "creating";
    if (creating) return { reconnect: false };
    if (reconnectAsked) return { reconnect: true };
    // The draft knows the kind: a new number that linked has an active row, and stays new.
    if (startedRecently({ instance: id }, viewing.userId, Date.now()))
      return { reconnect: readDraft({ instance: id }, viewing.userId)?.kind === "reconnect" };
    return null;
  };
  const [kind, setKind] = useState(kindNow);
  // "Reconnect" or "Continue" on the open drawer: the same path with a new location.
  const [seen, setSeen] = useState(location.key);
  if (seen !== location.key) {
    setSeen(location.key);
    if (kind === null) setKind(kindNow());
  }
  const shownKind = showView
    ? null
    : (kind ?? (found?.instance.state === "creating" ? { reconnect: false } : null));
  const done = useCallback(() => setKind(null), []);
  // The router keeps its state in the history, so a reload or Back would ask for a reconnection
  // again. The state is read once, and then the entry loses it. A running reconnection opens
  // again from its draft.
  const path = location.pathname + location.search;
  useEffect(() => {
    if (reconnectAsked) void navigate(path, { replace: true, state: null });
  }, [reconnectAsked, navigate, path]);
  // An open drawer keeps its result until Close, also when the instance goes away.
  const last = useRef(found);
  if (found) last.current = found;
  const shown = found ?? last.current;
  const phone = useNarrow();
  const popup = useRef<HTMLDivElement>(null);
  const [asking, setAsking] = useState(false);
  // A generated secret of the webhooks view with no "I saved it". The view
  // reports it from a passive effect. A setState there gets the default priority and renders in a
  // later task, so an Escape that comes first runs the old `onOpenChange` closure. The ref is
  // read at the time of the key event.
  const unsaved = useRef(false);
  const onUnsaved = useCallback((value: boolean) => {
    unsaved.current = value;
    if (!value) setAsking(false);
  }, []);
  const closeButton = useRef<HTMLButtonElement>(null);
  const goBack = useRef<HTMLButtonElement>(null);
  // "Go back" unmounts with the question, so the focus moves to "Close" by hand. An "I saved it"
  // in the view also ends the question, and the focus then stays on that button.
  const wentBack = useRef(false);
  useEffect(() => {
    if (asking) goBack.current?.focus();
    else if (wentBack.current) {
      wentBack.current = false;
      closeButton.current?.focus();
    }
  }, [asking]);

  // The focus returns to its origin, for example the cell. The drawer closes by a change of the
  // path, so it unmounts and cannot do it itself. The first render runs before the focus moves.
  const origin = useRef<Element | null>(null);
  origin.current ??= document.activeElement;
  useEffect(
    () => () => {
      const from = origin.current;
      if (from instanceof HTMLElement && from !== document.body)
        requestAnimationFrame(() => {
          if (from.isConnected) from.focus();
        });
    },
    [],
  );

  // A client user with one number has its screen at this path, and no drawer, except for
  // the steps of a pairing.
  if (id !== undefined && id === single && shownKind === null && !unknownView) return null;

  // An unknown id has no view: the drawer then says that the page does not exist.
  const connectTitle =
    showView && shown
      ? `${view === "bulk-jobs" ? "Bulk jobs" : "Webhooks"} of ${shown.instance.display_name}`
      : shownKind
        ? shownKind.reconnect
          ? `Reconnect ${(found ?? last.current)?.instance.display_name ?? ""}`
          : "Connect a WhatsApp number"
        : undefined;

  const close = () => void navigate("/");
  const tryClose = () => (unsaved.current ? setAsking(true) : close());
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) tryClose();
      }}
    >
      <SheetContent
        ref={popup}
        side={phone ? "bottom" : "right"}
        showCloseButton={false}
        aria-label={connectTitle ?? shown?.instance.display_name ?? "This page does not exist"}
        // Focus goes to the title of a drawer (Accessibility).
        initialFocus={() => popup.current?.querySelector<HTMLElement>("h2") ?? true}
        finalFocus={false}
        className="gap-5 data-[side=bottom]:max-h-[94dvh] overflow-auto px-4.5 pt-5 pb-8 data-[side=bottom]:rounded-t-[14px] data-[side=right]:w-full data-[side=right]:sm:max-w-[520px] sm:px-7.5"
      >
        <div className="flex justify-end">
          <Button ref={closeButton} variant="outline" onClick={tryClose}>
            Close
          </Button>
        </div>
        {asking && (
          <div role="alert" className="grid gap-3 rounded-md bg-wait-soft px-4 py-3.5">
            <b className="font-semibold">{savedQuestion}</b>
            <div className="flex flex-wrap gap-2">
              <Button onClick={close}>Yes, close</Button>
              <Button
                ref={goBack}
                variant="outline"
                onClick={() => {
                  wentBack.current = true;
                  setAsking(false);
                }}
              >
                Go back
              </Button>
            </div>
          </div>
        )}
        {showView && shown ? (
          view === "bulk-jobs" ? (
            <BulkJobsView instance={shown.instance} level={2} />
          ) : (
            <WebhooksView instance={shown.instance} level={2} onUnsaved={onUnsaved} />
          )
        ) : unknownView ? (
          <h2 tabIndex={-1}>This page does not exist</h2>
        ) : shownKind && (!isNew || target) ? (
          <ConnectDrawer
            id={id}
            clientId={target?.id}
            clientName={target?.name}
            problem={target?.problem ?? null}
            row={found?.instance}
            kind={shownKind}
            viewing={viewing}
            now={now}
            onDone={done}
          />
        ) : shown && !isNew ? (
          <InstanceCard
            instance={shown.instance}
            described={shown.d}
            viewing={viewing}
            now={now}
            level={2}
            onRemoved={() => void navigate("/", { replace: true })}
          />
        ) : (
          <h2 tabIndex={-1}>This page does not exist</h2>
        )}
      </SheetContent>
    </Sheet>
  );
}
