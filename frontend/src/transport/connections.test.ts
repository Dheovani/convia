import { beforeEach, describe, expect, it } from "vitest";
import { Connections, type Core } from "./connections";
import { carry, fromBase64, toBase64 } from "./wails";

class FakeChannel {
  readyState: RTCDataChannelState = "connecting";
  binaryType = "blob";
  sent: unknown[] = [];
  onopen?: () => void;
  onmessage?: (event: MessageEvent) => void;
  onclose?: () => void;
  send(data: unknown) {
    this.sent.push(data);
  }
  open() {
    this.readyState = "open";
    this.onopen?.();
  }
}

class FakePeerConnection {
  calls: string[] = [];
  configuration: RTCConfiguration;
  channel = new FakeChannel();
  channelOptions?: RTCDataChannelInit;
  localDescription: RTCSessionDescriptionInit | null = null;
  remoteDescription: RTCSessionDescriptionInit | null = null;
  connectionState: RTCPeerConnectionState = "new";
  failRemote = false;
  stats = new Map<string, Record<string, unknown>>();
  onicecandidate?: (event: { candidate: unknown }) => void;
  onconnectionstatechange?: () => void;
  onnegotiationneeded?: () => void;

  constructor(configuration: RTCConfiguration) {
    this.configuration = configuration;
  }
  createDataChannel(label: string, options: RTCDataChannelInit) {
    this.calls.push(`channel ${label}`);
    this.channelOptions = options;
    return this.channel;
  }
  setLocalDescription() {
    const type = this.remoteDescription?.type === "offer" ? "answer" : "offer";
    this.localDescription = { type, sdp: `local ${type}` };
    this.calls.push(`local ${type}`);
    return Promise.resolve();
  }
  setRemoteDescription(description: RTCSessionDescriptionInit) {
    if (this.failRemote) return Promise.reject(new Error("refused"));
    this.remoteDescription = description;
    this.calls.push(`remote ${description.type ?? ""}`);
    return Promise.resolve();
  }
  addIceCandidate(candidate: RTCIceCandidateInit) {
    this.calls.push(`candidate ${candidate.candidate ?? ""}`);
    return Promise.resolve();
  }
  close() {
    this.calls.push("close");
  }
  setConfiguration(configuration: RTCConfiguration) {
    this.configuration = configuration;
    this.calls.push("configure");
  }
  getConfiguration() {
    return this.configuration;
  }
  restartIce() {
    this.calls.push("restart");
  }
  getStats() {
    return Promise.resolve(this.stats);
  }
  relayed() {
    this.stats.set("t", { type: "transport", selectedCandidatePairId: "p" });
    this.stats.set("p", { localCandidateId: "l", remoteCandidateId: "r" });
    this.stats.set("l", { candidateType: "relay" });
    this.stats.set("r", { candidateType: "host" });
  }
}

let created: FakePeerConnection[];
let reported: string[];
let states: string[];
let connections: Connections;

const core: Core = {
  local: (id, kind, sdp) => reported.push(`${id} local ${kind} ${sdp}`),
  candidate: (id, c) => reported.push(`${id} candidate ${c.candidate ?? ""}`),
  opened: (id) => reported.push(`${id} opened`),
  received: (id, data) => reported.push(`${id} received ${data.join(",")}`),
  closed: (id) => reported.push(`${id} closed`),
  state: (id, state) => states.push(`${id} ${state}`),
};

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

beforeEach(() => {
  created = [];
  reported = [];
  states = [];
  connections = new Connections(core, (configuration) => {
    const pc = new FakePeerConnection(configuration);
    created.push(pc);
    return pc as unknown as RTCPeerConnection;
  });
});

describe("a connection", () => {
  it("has the one reliable, ordered data channel, the same on both sides", () => {
    connections.open("a", true, [{ urls: "stun:turn.example" }]);
    expect(created[0].channelOptions).toEqual({
      negotiated: true,
      id: 0,
      ordered: true,
    });
    expect(created[0].channel.binaryType).toBe("arraybuffer");
    expect(created[0].configuration.iceServers).toEqual([
      { urls: "stun:turn.example" },
    ]);
  });

  it("offers when it is the offerer", async () => {
    connections.open("a", true, []);
    created[0].onnegotiationneeded?.();
    await settle();
    expect(reported).toEqual(["a local offer local offer"]);
  });

  it("answers an offer, and applies candidates only after it", async () => {
    connections.open("a", false, []);
    connections.describe("a", "offer", "remote offer");
    connections.candidate("a", { candidate: "c1" });
    await settle();
    expect(created[0].calls).toEqual([
      "channel convia",
      "remote offer",
      "local answer",
      "candidate c1",
    ]);
    expect(reported).toEqual(["a local answer local answer"]);
    expect(created[0].onnegotiationneeded).toBeUndefined();
  });

  it("reports its candidates, its opening and its binary messages", () => {
    connections.open("a", true, []);
    const pc = created[0];
    pc.onicecandidate?.({ candidate: { toJSON: () => ({ candidate: "c1" }) } });
    pc.onicecandidate?.({ candidate: null });
    pc.channel.open();
    pc.channel.onmessage?.(
      new MessageEvent("message", { data: new Uint8Array([1, 2]).buffer }),
    );
    pc.channel.onmessage?.(new MessageEvent("message", { data: "text" }));
    expect(reported).toEqual(["a candidate c1", "a opened", "a received 1,2"]);
  });

  it("sends only once its channel is open", () => {
    connections.open("a", true, []);
    connections.send("a", new Uint8Array([1]));
    created[0].channel.open();
    connections.send("a", new Uint8Array([2]));
    expect(created[0].channel.sent).toEqual([new Uint8Array([2])]);
  });

  it("reports its end once, whatever ends it", () => {
    connections.open("a", true, []);
    const pc = created[0];
    pc.connectionState = "failed";
    pc.onconnectionstatechange?.();
    pc.channel.onclose?.();
    expect(reported).toEqual(["a closed"]);
    expect(pc.calls).toContain("close");
  });

  it("does not report an end the core asked for", () => {
    connections.open("a", true, []);
    connections.close("a");
    created[0].channel.onclose?.();
    expect(reported).toEqual([]);
    expect(created[0].calls).toContain("close");
  });

  it("ends when a description cannot be applied", async () => {
    connections.open("a", false, []);
    created[0].failRemote = true;
    connections.describe("a", "offer", "remote offer");
    await settle();
    expect(reported).toEqual(["a closed"]);
    expect(states).toEqual(["a failed: Error"]);
  });

  it("restarts ICE on new servers only where it offered and is relayed", async () => {
    connections.open("relayed", true, []);
    connections.open("direct", true, []);
    connections.open("answered", false, []);
    created[0].relayed();
    created[2].relayed();
    connections.configure([{ urls: "turn:turn.example", username: "u" }]);
    await settle();
    expect(created.map((pc) => pc.calls.includes("restart"))).toEqual([
      true,
      false,
      false,
    ]);
    expect(created.every((pc) => pc.calls.includes("configure"))).toBe(true);
  });
});

describe("the core's orders", () => {
  it("are carried out", async () => {
    carry(connections, { op: "open", id: "a", offerer: false, ice: [] });
    carry(connections, {
      op: "describe",
      id: "a",
      kind: "offer",
      sdp: "remote offer",
    });
    carry(connections, {
      op: "candidate",
      id: "a",
      candidate: { candidate: "c1" },
    });
    await settle();
    created[0].channel.open();
    carry(connections, {
      op: "send",
      id: "a",
      data: toBase64(new Uint8Array([9, 8])),
    });
    carry(connections, { op: "close", id: "a" });
    expect(created[0].calls).toEqual([
      "channel convia",
      "remote offer",
      "local answer",
      "candidate c1",
      "close",
    ]);
    expect(created[0].channel.sent).toEqual([new Uint8Array([9, 8])]);
  });

  it("carry bytes as Base64, whatever their size", () => {
    const data = Uint8Array.from({ length: 100_000 }, (_, i) => i % 256);
    expect(fromBase64(toBase64(data))).toEqual(data);
    expect(toBase64(new Uint8Array([0, 255]))).toBe("AP8=");
  });
});
