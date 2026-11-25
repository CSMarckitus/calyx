import { Component, type ReactNode } from "react";
import { Notice } from "@/components/notice";

// Scalar is a large dependency. When it throws, the page keeps its header and gives the raw document.
export class ScalarBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <Notice tone="bad" title="The page cannot show the reference.">
        <a href="/openapi.json" className="font-semibold text-primary underline">
          /openapi.json
        </a>
      </Notice>
    );
  }
}
