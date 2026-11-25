import { Component, type ReactNode } from "react";
import { Button } from "@/components/ui/button";

// A screen that throws shows this, and the header stays usable.
export class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <section role="alert" className="grid justify-items-start gap-3">
        <h1>This page stopped</h1>
        <Button type="button" onClick={() => location.reload()}>
          Reload
        </Button>
      </section>
    );
  }
}
