import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import type { RemoteRoom } from '../api/types'
import { useRemoteRooms } from './useRemoteRooms'

afterEach(() => vi.unstubAllGlobals())

const forgotten: RemoteRoom = {
  id: 'rrm_7KQZP4XN2VJH6TBWMDR3YAFC5E',
  home: 'https://elsewhere.example',
  room_id: 'room_7KQZP4XN2VJH6TBWMDR3YAFC5E',
  user_id: 'usr_ANATHERE7KQZP4XN2VJH6TBWMD',
  name: 'Their room',
}

function Probe() {
  const { remoteRooms, refresh, forget } = useRemoteRooms(() => {})
  return (
    <>
      <p>{remoteRooms.length === 0 ? 'nothing elsewhere' : remoteRooms.map((room) => room.name).join(', ')}</p>
      <button onClick={refresh}>read again</button>
      <button onClick={() => forget(forgotten.id)}>forget</button>
    </>
  )
}

function listing(rooms: RemoteRoom[]): Response {
  return new Response(JSON.stringify({ data: rooms }), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

/*
A read that left before a room was forgotten here does not put it back.

This is the defect the quarantined Peers test had been reporting for a month as
a flake: it showed only when a read happened to be in flight at the moment of
forgetting, which a full suite made likely and a single file never did. Here
the read is held open on purpose, so the order that used to happen sometimes
happens every time.
*/
it('does not let a read from before forgetting a room put it back', async () => {
  let release: (response: Response) => void = () => {}
  const reads: string[] = []

  vi.stubGlobal('fetch', () => {
    reads.push('read')
    if (reads.length === 1) {
      return Promise.resolve(listing([forgotten]))
    }
    if (reads.length === 2) {
      // Left while the room still existed, and answered after it was forgotten.
      return new Promise<Response>((resolve) => (release = resolve))
    }
    return Promise.resolve(listing([]))
  })

  render(<Probe />)
  expect(await screen.findByText('Their room')).toBeInTheDocument()

  const person = userEvent.setup()
  await person.click(screen.getByRole('button', { name: 'read again' }))
  await person.click(screen.getByRole('button', { name: 'forget' }))
  expect(screen.getByText('nothing elsewhere')).toBeInTheDocument()

  await act(async () => release(listing([forgotten])))

  await waitFor(() => expect(reads.length).toBeGreaterThanOrEqual(3))
  expect(screen.getByText('nothing elsewhere')).toBeInTheDocument()
})
