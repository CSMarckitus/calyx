import { type AddressInfo, connect, createServer, type Socket } from "node:net";

export interface Proxy {
  address: string;
  // How many connections the proxy accepted.
  connections(): number;
  // From now on, the open connections stay open but carry no more bytes, as when the host
  // disappears. A new connection works.
  cut(): void;
  close(): void;
}

// A TCP proxy on 127.0.0.1 in front of the fake core.
export async function startProxy(target: string): Promise<Proxy> {
  const { hostname, port } = new URL(`tcp://${target}`);
  const sockets: Socket[] = [];
  let connections = 0;
  let live = { on: true };

  const server = createServer((client) => {
    connections += 1;
    const line = live;
    const core = connect(Number(port), hostname);
    sockets.push(client, core);
    for (const [from, to] of [
      [client, core],
      [core, client],
    ] as const) {
      from.on("data", (bytes) => {
        if (line.on) to.write(bytes);
      });
      from.on("error", () => undefined);
      from.on("close", () => {
        if (line.on) to.destroy();
      });
    }
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    address: `127.0.0.1:${(server.address() as AddressInfo).port}`,
    connections: () => connections,
    cut: () => {
      live.on = false;
      live = { on: true };
    },
    close: () => {
      server.close();
      for (const socket of sockets) socket.destroy();
    },
  };
}
