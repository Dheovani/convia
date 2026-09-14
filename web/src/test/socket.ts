/*
A stand-in for the browser's WebSocket, driven by the test.

It never connects anywhere. A socket stays connecting until a test opens it, so
every test that does not care about the stream sees the interface as it behaves
when the stream is down — asking on a timer — which is the behavior those tests
were written against.
*/
export class FakeSocket {
  static opened: FakeSocket[] = []

  readonly url: string
  readyState = 0

  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeSocket.opened.push(this)
  }

  // latest is the socket the interface opened most recently.
  static latest(): FakeSocket | undefined {
    return FakeSocket.opened[FakeSocket.opened.length - 1]
  }

  static reset(): void {
    FakeSocket.opened = []
  }

  open(): void {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }

  // deliver sends one event down the socket, serialized as Convia sends it.
  deliver(event: unknown): void {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(event) }))
  }

  close(code = 1000): void {
    if (this.readyState === 3) {
      return
    }
    this.readyState = 3
    this.onclose?.({ code } as CloseEvent)
  }
}
