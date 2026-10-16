// Two of the WebView's connection managers in one page, each the other's core, over real WebRTC (ADR 0025).
import { Connections, type Core } from "../src/transport/connections";

interface Side {
  connections: Connections;
  pcs: RTCPeerConnection[];
  opened: boolean;
  closed: boolean;
  offers: number;
  received: number[][];
  fileBytes: number;
  filePieces: number;
}

const sides: Side[] = [];

function start(ice: RTCIceServer[], relayOnly: boolean) {
  const core = (i: number): Core => ({
    local: (id, kind, sdp) => {
      if (kind === "offer") sides[i].offers++;
      sides[1 - i].connections.describe(id, kind, sdp);
    },
    candidate: (id, candidate) =>
      sides[1 - i].connections.candidate(id, candidate),
    opened: () => {
      sides[i].opened = true;
    },
    received: (_, data) => {
      sides[i].received.push([...data]);
    },
    fileReceived: (_, data) => {
      sides[i].fileBytes += data.length;
      sides[i].filePieces += 1;
    },
    closed: () => {
      sides[i].closed = true;
    },
    state: () => undefined,
  });
  for (const i of [0, 1]) {
    const pcs: RTCPeerConnection[] = [];
    const create = (configuration: RTCConfiguration) => {
      const pc = new RTCPeerConnection(
        relayOnly
          ? { ...configuration, iceTransportPolicy: "relay" }
          : configuration,
      );
      pcs.push(pc);
      return pc;
    };
    sides[i] = {
      connections: new Connections(core(i), create),
      pcs,
      opened: false,
      closed: false,
      offers: 0,
      received: [],
      fileBytes: 0,
      filePieces: 0,
    };
  }
  sides[1].connections.open("x", false, ice);
  sides[0].connections.open("x", true, ice);
}

async function candidateTypes(i: number): Promise<string[]> {
  const stats = await sides[i].pcs[0].getStats();
  for (const report of stats.values()) {
    if (report.type === "transport" && report.selectedCandidatePairId) {
      const pair = stats.get(report.selectedCandidatePairId);
      return [
        stats.get(pair.localCandidateId)?.candidateType,
        stats.get(pair.remoteCandidateId)?.candidateType,
      ];
    }
  }
  return [];
}

const harness = {
  start,
  candidateTypes,
  opened: () => sides.map((s) => s.opened),
  closed: () => sides.map((s) => s.closed),
  offers: (i: number) => sides[i].offers,
  received: (i: number) => sides[i].received,
  send: (i: number, data: number[]) =>
    sides[i].connections.send("x", new Uint8Array(data)),
  // sendFile sends size bytes in pieces of piece bytes on the channel for files, all at once.
  sendFile: (i: number, size: number, piece: number) => {
    for (let sent = 0; sent < size; sent += piece) {
      sides[i].connections.sendFile(
        "x",
        new Uint8Array(Math.min(piece, size - sent)),
      );
    }
  },
  fileReceived: (i: number) => ({
    bytes: sides[i].fileBytes,
    pieces: sides[i].filePieces,
  }),
  configure: (i: number, ice: RTCIceServer[]) =>
    sides[i].connections.configure(ice),
};

export type Harness = typeof harness;

declare global {
  interface Window {
    harness: Harness;
  }
}

window.harness = harness;
