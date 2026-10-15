import {
  NetworkChanged,
  TransportCandidate,
  TransportClosed,
  TransportLocal,
  TransportOpened,
  TransportReceived,
  TransportReset,
} from "../../wailsjs/go/app/Bridge";
import { peers } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Connections, type Core, type Kind } from "./connections";

// An order from the core, as internal/app/network.go sends it.
export interface Order {
  op: "open" | "describe" | "candidate" | "send" | "close" | "configure";
  id?: string;
  offerer?: boolean;
  ice?: RTCIceServer[];
  kind?: Kind;
  sdp?: string;
  candidate?: RTCIceCandidateInit;
  data?: string;
}

export function toBase64(data: Uint8Array): string {
  let text = "";
  for (let i = 0; i < data.length; i += 0x8000) {
    text += String.fromCharCode(...data.subarray(i, i + 0x8000));
  }

  return btoa(text);
}

export function fromBase64(text: string): Uint8Array {
  return Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
}

export function carry(connections: Connections, order: Order) {
  const id = order.id ?? "";
  switch (order.op) {
    case "open":
      connections.open(id, order.offerer ?? false, order.ice ?? []);
      break;
    case "describe":
      if (order.kind && order.sdp !== undefined)
        connections.describe(id, order.kind, order.sdp);
      break;
    case "candidate":
      if (order.candidate)
        connections.candidate(id, order.candidate);
      break;
    case "send":
      connections.send(id, fromBase64(order.data ?? ""));
      break;
    case "close":
      connections.close(id);
      break;
    case "configure":
      connections.configure(order.ice ?? []);
      break;
  }
}

const core: Core = {
  local: (id, kind, sdp) => void TransportLocal(id, kind, sdp),
  candidate: (id, c) =>
    void TransportCandidate(
      id,
      peers.Candidate.createFrom({
        candidate: c.candidate ?? "",
        sdpMid: c.sdpMid ?? "",
        sdpMLineIndex: c.sdpMLineIndex ?? 0,
        usernameFragment: c.usernameFragment ?? "",
      }),
    ),
  opened: (id) => void TransportOpened(id),
  received: (id, data) => void TransportReceived(id, toBase64(data)),
  closed: (id) => void TransportClosed(id),
};

// startTransport carries out the core's orders for as long as the page lives.
export function startTransport() {
  const connections = new Connections(core);
  EventsOn("transport", (order: Order) => carry(connections, order));

  // A page that loads again has lost every connection the core thinks it holds.
  void TransportReset();
  window.addEventListener("online", () => void NetworkChanged());
}
