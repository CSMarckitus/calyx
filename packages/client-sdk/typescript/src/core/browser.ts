import { CalyxError } from "../errors/index.js";
// Type only: the build removes it, so no gRPC code reaches a web build.
import type { CoreClientOptions } from "./client.js";

// A bundler takes this file for the browser. It keeps gRPC code out of a web build.
export function createCoreClient(_options?: CoreClientOptions): never {
  throw new CalyxError({
    source: "client",
    code: "unsupported_runtime",
    status: 0,
    detail: "The core client needs Node, Bun or Deno.",
  });
}
