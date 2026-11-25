import { describe, expect, it } from "vitest";
import { type OperationId, operations, type Params } from "./operations.ts";

describe("operations", () => {
  it("has every operation of the API, and only the right limits", () => {
    expect(Object.keys(operations)).toHaveLength(37);
    expect(operations.getMe).toMatchObject({
      method: "GET",
      path: "/api/v1/me",
      timeoutMs: 20_000,
    });
    expect(operations.updateInstance.timeoutMs).toBe(45_000);
    expect(operations.removeInstance.timeoutMs).toBe(75_000);
    expect(operations.followEvents.events).toEqual(["changed", "replaced"]);
  });

  // tsc checks these lines: each @ts-expect-error needs a real error.
  it("does not compile a wrong operation, path or parameter", () => {
    const id: OperationId = "getMe";
    expect(id).toBe("getMe");
    // @ts-expect-error: this operation does not exist
    const missing: OperationId = "noSuchOperation";
    expect(missing).toBeDefined();
    // @ts-expect-error: getInstance needs a path parameter
    const noPath: Params<"getInstance"> = {};
    expect(noPath).toBeDefined();
    // @ts-expect-error: a GET has no body
    const body: Params<"getMe"> = { body: {} };
    expect(body).toBeDefined();
  });
});
