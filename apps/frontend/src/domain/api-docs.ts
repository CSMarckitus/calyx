// The part of an OpenAPI document that the role filter reads. Other fields pass through.
export type OpenApiOperation = {
  operationId?: string;
  tags?: string[];
  security?: Record<string, string[]>[];
  "x-calyx-roles"?: string[];
  [key: string]: unknown;
};

export type OpenApiDocument = {
  paths: Record<string, Record<string, OpenApiOperation>>;
  tags?: { name: string; [key: string]: unknown }[];
  components?: { securitySchemes?: Record<string, unknown>; [key: string]: unknown };
  [key: string]: unknown;
};

// Keeps the program operations that the role may call: they accept an API key and list the role.
// It removes the session scheme, and the paths and tags that end with no operation.
export function filterForRole(
  apiDocument: OpenApiDocument,
  role: "owner" | "client",
): OpenApiDocument {
  const paths: OpenApiDocument["paths"] = {};
  const usedTags = new Set<string>();

  for (const [path, item] of Object.entries(apiDocument.paths)) {
    const kept: Record<string, OpenApiOperation> = {};
    for (const [method, op] of Object.entries(item)) {
      const acceptsKey = op.security?.some((entry) => "apiKey" in entry) ?? false;
      if (!acceptsKey || !op["x-calyx-roles"]?.includes(role)) continue;
      kept[method] = { ...op, security: op.security?.filter((entry) => !("session" in entry)) };
      for (const tag of op.tags ?? []) usedTags.add(tag);
    }
    if (Object.keys(kept).length > 0) paths[path] = structuredClone(kept);
  }

  const out: OpenApiDocument = { ...structuredClone({ ...apiDocument, paths: {} }), paths };
  if (apiDocument.tags)
    out.tags = structuredClone(apiDocument.tags.filter((t) => usedTags.has(t.name)));
  if (out.components?.securitySchemes) {
    const { session: _session, ...rest } = out.components.securitySchemes as Record<
      string,
      unknown
    >;
    out.components.securitySchemes = rest;
  }
  return out;
}
