// The three people of Calyx. The Organization screen and /docs show this table.
export const people: readonly { name: string; text: string }[] = [
  {
    name: "Server admin (operator)",
    text: "The server admin installs and runs Calyx on a server, and uses the command line, not this panel.",
  },
  {
    name: "Organization admin (owner)",
    text: "The organization admin manages one organization in this panel: its numbers, its clients and its settings.",
  },
  {
    name: "Client user",
    text: "A client user is the person of one client, and sees only the numbers of that client.",
  },
];
