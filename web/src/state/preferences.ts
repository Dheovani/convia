import type { Choice } from '../media/connection'

// key is where this browser keeps what a person chose for calls.
const key = 'convia.call'

/*
defaultChoice is what somebody who never chose gets: speaking and unseen, on the
system's devices.
*/
export const defaultChoice: Choice = {
  microphone: true,
  camera: false,
  audioInput: '',
  videoInput: '',
  audioOutput: '',
}

/*
remembered reads what this person chose in this browser.

It is kept in the browser rather than the account on purpose: a device identifier
means something only to the browser that saw the device, so carrying it to
another computer would choose nothing there. Anything unreadable — storage that is
blocked, a value from an older build — is the default rather than an error.
*/
export function remembered(): Choice {
  try {
    const stored: unknown = JSON.parse(window.localStorage.getItem(key) ?? 'null')
    if (typeof stored !== 'object' || stored === null) {
      return defaultChoice
    }

    const read = stored as Record<string, unknown>
    const text = (value: unknown) => (typeof value === 'string' ? value : '')

    return {
      microphone: read['microphone'] !== false,
      camera: read['camera'] === true,
      audioInput: text(read['audioInput']),
      videoInput: text(read['videoInput']),
      audioOutput: text(read['audioOutput']),
    }
  } catch {
    return defaultChoice
  }
}

// remember keeps a choice for the next call. Storage that refuses is not worth a failure.
export function remember(choice: Choice): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(choice))
  } catch {
    // The choice still holds for this page.
  }
}
