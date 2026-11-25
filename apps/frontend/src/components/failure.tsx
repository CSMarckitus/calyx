import { Fragment } from "react";
import { cn } from "@/lib/utils";
import type { Failure as RequestFailure } from "../api/request.ts";
import { type ErrorContext, explain, type PairingFailure } from "../domain/errors.ts";
import { CopyButton } from "./copy-button.tsx";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible.tsx";
import { Words } from "./words.tsx";

// The error texts write a name between backticks. It shows as code, with no HTML parsing.
// The name of the Manager and "Calyx" keep their form under a translator.
function Marked({ text, manager }: { text: string; manager?: string }) {
  return text.split("`").map((part, i) =>
    i % 2 === 1 ? (
      <code key={i} translate="no">
        {part}
      </code>
    ) : (
      <Words key={i} text={part} keep={manager ? [manager] : []} />
    ),
  );
}

const labels = { code: "Code", detail: "Detail", requestId: "Request ID" } as const;

// What a failed action shows: the cause, the next step, and the "Technical details" to copy.
// `checking`: the page reads the data again after a change with no known result.
export function Failure({
  failure,
  context,
  className,
  checking = false,
}: {
  failure: RequestFailure | PairingFailure;
  context: ErrorContext;
  className?: string;
  checking?: boolean;
}) {
  const { cause, nextStep, details } = explain(failure, context);
  const lines = (Object.keys(labels) as (keyof typeof labels)[])
    .filter((k) => details[k])
    .map((k) => [labels[k], details[k]] as const);

  return (
    <div
      data-slot="failure"
      role="alert"
      className={cn("grid gap-1.5 rounded-md bg-bad-soft px-4 py-3.5 text-[0.9375rem]", className)}
    >
      <b className="font-semibold">
        <Marked text={cause} manager={context.manager} />
      </b>
      {/* Some causes have no next step: no empty line in the grid. */}
      {nextStep && (
        <span>
          <Marked text={nextStep} manager={context.manager} />
        </span>
      )}
      {checking && <span role="status">Checking the result</span>}
      {lines.length > 0 && (
        <Collapsible>
          <CollapsibleTrigger className="min-h-11 cursor-pointer underline underline-offset-3">
            Technical details
          </CollapsibleTrigger>
          <CollapsibleContent className="grid gap-2 pt-2">
            <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
              {lines.map(([name, value]) => (
                <Fragment key={name}>
                  <dt>{name}</dt>
                  <dd className="m-0 font-mono break-all">{value}</dd>
                </Fragment>
              ))}
            </dl>
            <div>
              <CopyButton text={lines.map(([n, v]) => `${n}: ${v}`).join("\n")} />
            </div>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}
