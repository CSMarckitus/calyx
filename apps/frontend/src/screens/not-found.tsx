import { Link } from "react-router";

// Also for a client user on a path of the owner: the API answers 404 to it too.
export function NotFound() {
  return (
    <div className="grid gap-3">
      <h1>This page does not exist</h1>
      <Link
        to="/"
        className="inline-flex min-h-11 items-center justify-self-start font-semibold text-primary underline underline-offset-3"
      >
        Go to the list
      </Link>
    </div>
  );
}
