import { Link } from "react-router";
import { Notice } from "./notice.tsx";

// The terms notice. The Instances screen shows it in place of the list, and the layout
// shows it as the banner of the other screens.
export function TermsNotice({ blocked }: { blocked: boolean }) {
  return (
    <Notice tone="brand" title="Accept the WhatsApp Web terms before you connect a number.">
      You accept them once, also if you use only the Meta Cloud API.{" "}
      {blocked &&
        "The server admin blocked WhatsApp Web for this organization. You can still accept the terms now. "}
      <Link to="/organization/whatsapp-web" className="font-semibold text-primary underline">
        Read and accept the terms
      </Link>
    </Notice>
  );
}
