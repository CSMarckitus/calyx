import { defineConfig } from "vitest/config";

// The test of the built image and of the gateway. It builds the image, so it is slow.
export default defineConfig({
  test: {
    include: ["test/image.test.ts"],
    environment: "node",
    globalSetup: ["test/image-global.ts"],
    hookTimeout: 600_000,
    testTimeout: 60_000,
  },
});
