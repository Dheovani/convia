// The WebView's side of every connection to another peer: it carries out what the core decides (ADR 0006).

export type Kind = "offer" | "answer";

export interface Core {
  local(id: string, kind: Kind, sdp: string): void;
  candidate(id: string, candidate: RTCIceCandidateInit): void;
  opened(id: string): void;
  received(id: string, data: Uint8Array): void;
  // fileReceived is data from the channel for files.
  fileReceived(id: string, data: Uint8Array): void;
  closed(id: string): void;
  // state is what the connection is doing, for the log.
  state(id: string, state: string): void;
}

export type Create = (configuration: RTCConfiguration) => RTCPeerConnection;

interface Entry {
  pc: RTCPeerConnection;
  channel: RTCDataChannel;
  files: RTCDataChannel;
  // Pieces of files waiting for the channel's buffer to empty.
  pending: Uint8Array<ArrayBuffer>[];
  offerer: boolean;
  queue: Promise<void>;
}

/*
How much the channel for files may hold unsent before pieces wait here, and how low it must fall before
they go on: a browser closes a channel whose buffer overflows.
*/
export const fullBuffer = 4 << 20;
export const lowBuffer = 1 << 20;

export class Connections {
  readonly #core: Core;
  readonly #create: Create;
  readonly #entries = new Map<string, Entry>();

  constructor(
    core: Core,
    create: Create = (configuration) => new RTCPeerConnection(configuration),
  ) {
    this.#core = core;
    this.#create = create;
  }

  open(id: string, offerer: boolean, iceServers: RTCIceServer[]) {
    if (this.#entries.has(id)) return;

    const pc = this.#create({ iceServers, bundlePolicy: "max-bundle" });

    // The one reliable, ordered channel of the first version (ADR 0008), the same id on both sides.
    const channel = pc.createDataChannel("convia", {
      negotiated: true,
      id: 0,
      ordered: true,
    });

    channel.binaryType = "arraybuffer";

    // Files travel beside the messages, so a message never waits behind one (ADR 0036).
    const files = pc.createDataChannel("convia-files", {
      negotiated: true,
      id: 1,
      ordered: true,
    });
    files.binaryType = "arraybuffer";
    files.bufferedAmountLowThreshold = lowBuffer;

    const entry: Entry = {
      pc,
      channel,
      files,
      pending: [],
      offerer,
      queue: Promise.resolve(),
    };
    this.#entries.set(id, entry);

    files.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        this.#core.fileReceived(id, new Uint8Array(event.data));
      }
    };
    files.onbufferedamountlow = () => this.#flush(entry);
    files.onopen = () => this.#flush(entry);

    channel.onopen = () => this.#core.opened(id);
    channel.onclose = () => this.#end(id, entry);
    channel.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        this.#core.received(id, new Uint8Array(event.data));
      }
    };
    pc.onicecandidate = (event) => {
      if (event.candidate) this.#core.candidate(id, event.candidate.toJSON());
    };
    pc.oniceconnectionstatechange = () =>
      this.#core.state(id, `ice ${pc.iceConnectionState}`);
    pc.onconnectionstatechange = () => {
      this.#core.state(id, pc.connectionState);
      if (pc.connectionState === "failed" || pc.connectionState === "closed") {
        this.#end(id, entry);
      }
    };

    if (offerer) {
      pc.onnegotiationneeded = () =>
        this.#then(id, entry, async () => {
          await pc.setLocalDescription();
          this.#core.local(id, "offer", pc.localDescription?.sdp ?? "");
        });
    }
  }

  describe(id: string, kind: Kind, sdp: string) {
    const entry = this.#entries.get(id);
    if (!entry) return;

    this.#then(id, entry, async () => {
      await entry.pc.setRemoteDescription({ type: kind, sdp });
      if (kind === "offer") {
        await entry.pc.setLocalDescription();
        this.#core.local(id, "answer", entry.pc.localDescription?.sdp ?? "");
      }
    });
  }

  candidate(id: string, candidate: RTCIceCandidateInit) {
    const entry = this.#entries.get(id);
    if (!entry) return;

    // A candidate the connection cannot use is no reason to end it.
    this.#then(id, entry, () =>
      entry.pc.addIceCandidate(candidate).catch(() => undefined),
    );
  }

  send(id: string, data: Uint8Array) {
    const channel = this.#entries.get(id)?.channel;
    if (channel?.readyState === "open") channel.send(new Uint8Array(data));
  }

  // sendFile sends a piece of a file, holding it back while the channel's buffer is full.
  sendFile(id: string, data: Uint8Array) {
    const entry = this.#entries.get(id);
    if (!entry) return;

    entry.pending.push(new Uint8Array(data));
    this.#flush(entry);
  }

  #flush(entry: Entry) {
    const { files, pending } = entry;
    while (
      files.readyState === "open" &&
      pending.length > 0 &&
      files.bufferedAmount < fullBuffer
    ) {
      const next = pending.shift();
      if (next) files.send(next);
    }
  }

  // close ends a connection the core no longer wants; the core is not told.
  close(id: string) {
    const entry = this.#entries.get(id);
    if (!entry) return;

    this.#entries.delete(id);
    entry.pc.close();
  }

  // configure gives every connection new ICE servers; a relayed one moves to them with an ICE restart (ADR 0023).
  configure(iceServers: RTCIceServer[]) {
    for (const [id, entry] of this.#entries) {
      entry.pc.setConfiguration({
        ...entry.pc.getConfiguration(),
        iceServers,
      });

      if (entry.offerer) {
        this.#then(id, entry, async () => {
          if (await relayed(entry.pc)) entry.pc.restartIce();
        });
      }
    }
  }

  #then(id: string, entry: Entry, step: () => Promise<unknown>) {
    entry.queue = entry.queue
      .then(async () => {
        if (this.#entries.get(id) === entry) await step();
      })
      .catch((error: unknown) => {
        this.#core.state(
          id,
          `failed: ${error instanceof Error ? error.name : "error"}`,
        );
        this.#end(id, entry);
      });
  }

  #end(id: string, entry: Entry) {
    if (this.#entries.get(id) !== entry) return;

    this.#entries.delete(id);
    entry.pc.close();
    this.#core.closed(id);
  }
}

async function relayed(pc: RTCPeerConnection): Promise<boolean> {
  const stats = await pc.getStats();

  for (const report of stats.values()) {
    if (report.type === "transport" && report.selectedCandidatePairId) {
      const pair = stats.get(report.selectedCandidatePairId);
      const local = pair && stats.get(pair.localCandidateId);
      const remote = pair && stats.get(pair.remoteCandidateId);

      return (
        local?.candidateType === "relay" || remote?.candidateType === "relay"
      );
    }
  }

  return false;
}
