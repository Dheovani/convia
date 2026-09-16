import { useRef, useState } from 'react'

import { ApiError, NetworkError } from '../api/client'
import type { Words } from '../i18n/en'
import { refused, useWords } from '../i18n/language'
import { Button } from './controls'

/*
bodyLimit is a courtesy, not the rule.

Convia bounds a message at four thousand characters and counts them as
characters; the browser counts the units it stores them in, which differ for
emoji and for anything outside the basic plane. So this stops most of the
over-long messages at the keyboard, and the server stops all of them.
*/
const bodyLimit = 4000

interface ComposerProps {
  roomName: string
  disabled: boolean
  onSend: (body: string) => Promise<void>
}

function explain(words: Words, error: unknown): string {
  const said = words.composer
  if (error instanceof NetworkError) {
    return said.unreachable
  }
  if (error instanceof ApiError) {
    switch (error.code) {
      case 'conflict':
        return said.closed
      case 'not_found':
        return said.gone
    }
  }
  return refused(words, error, said.failed)
}

/*
Composer is where somebody says something.

Enter sends and Shift+Enter breaks a line, which is what every messaging
interface has taught people to expect. What was typed is only cleared once
Convia has taken it: clearing on submit loses the words on a failure, and the
words are the one thing here that cannot be recovered.
*/
export function Composer({ roomName, disabled, onSend }: ComposerProps) {
  const [body, setBody] = useState('')
  const [failure, setFailure] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const field = useRef<HTMLTextAreaElement>(null)
  const words = useWords()
  const said = words.composer

  async function send() {
    const written = body.trim()
    if (written === '' || sending || disabled) {
      return
    }

    setSending(true)
    setFailure(null)
    try {
      await onSend(written)
      setBody('')
    } catch (error) {
      setFailure(explain(words, error))
    } finally {
      setSending(false)
      field.current?.focus()
    }
  }

  return (
    <div className="px-3 pb-4 md:px-5">
      {failure !== null && (
        <p className="mt-0 mb-2 text-[0.8rem] text-danger" role="alert">
          {failure}
        </p>
      )}
      <form
        className="flex items-end gap-2 rounded-md border border-line bg-surface-raised p-2
          focus-within:border-accent"
        onSubmit={(event) => {
          event.preventDefault()
          void send()
        }}
      >
        <label className="sr-only" htmlFor="composer-body">
          {said.label(roomName)}
        </label>
        <textarea
          id="composer-body"
          ref={field}
          className="max-h-[40vh] flex-1 resize-y border-0 bg-transparent p-2 focus:outline-none"
          rows={1}
          maxLength={bodyLimit}
          placeholder={disabled ? said.closedPlaceholder : said.label(roomName)}
          disabled={disabled}
          value={body}
          onChange={(event) => setBody(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey) {
              event.preventDefault()
              void send()
            }
          }}
        />
        <Button
          tone="primary"
          type="submit"
          className="flex-none"
          disabled={disabled || sending || body.trim() === ''}
        >
          {said.send}
        </Button>
      </form>
    </div>
  )
}
