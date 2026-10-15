// A stand-in for the Wails runtime's events: emit plays the core sending one.
type Handler = (...data: never[]) => void;

const handlers = new Map<string, Set<Handler>>();

export function emit(event: string, ...data: unknown[]) {
  for (const handler of handlers.get(event) ?? []) {
    (handler as (...data: unknown[]) => void)(...data);
  }
}

export function resetEvents() {
  handlers.clear();
}

export const runtime = {
  EventsOn(event: string, handler: Handler) {
    const set = handlers.get(event) ?? new Set<Handler>();
    set.add(handler);
    handlers.set(event, set);
    return () => set.delete(handler);
  },
};
