import { cn } from "@/lib/utils";
import { Button } from "../ui/button.tsx";

// One answer of a question with two answers: a button that stays pressed.
export function Choice({
  pressed,
  title,
  text,
  onClick,
}: {
  pressed: boolean;
  title: string;
  text: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className={cn(
        "grid min-h-23 cursor-pointer content-start gap-1 rounded-md border border-border bg-card p-4.5 text-left",
        "aria-pressed:border-primary aria-pressed:bg-accent aria-pressed:shadow-[inset_0_0_0_1px_var(--brand)]",
      )}
    >
      <b className="font-display font-semibold">{title}</b>
      <span className="text-[0.9375rem] text-muted-foreground">{text}</span>
    </button>
  );
}

// "What is it for?": send and receive, or only send.
export function UseStep({
  sendOnly,
  onChoose,
  onContinue,
}: {
  sendOnly: boolean;
  onChoose: (sendOnly: boolean) => void;
  onContinue: () => void;
}) {
  return (
    <div className="grid gap-4">
      <fieldset
        aria-label="What is it for?"
        className="m-0 min-w-0 border-0 p-0 grid gap-3 sm:grid-cols-2"
      >
        <Choice
          pressed={!sendOnly}
          title="Send and receive"
          text="It stays connected all the time, so your programs can also read the messages that arrive, with webhooks."
          onClick={() => onChoose(false)}
        />
        <Choice
          pressed={sendOnly}
          title="Only send"
          text="It starts by itself each time there is a message to send, and rests in between to save resources. You link the phone only once. It receives nothing and has no webhooks. Good for invoices or reminders."
          onClick={() => onChoose(true)}
        />
      </fieldset>
      <p className="m-0 text-[0.9375rem] text-muted-foreground">You can change it later.</p>
      <div className="flex flex-wrap gap-2">
        <Button onClick={onContinue}>Continue</Button>
      </div>
    </div>
  );
}
