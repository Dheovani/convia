import { useCallback, useEffect, useRef, useState } from 'react'

import { api, sourceKey, type RoomSource } from '../api/client'
import type { Account, SidebarRoom } from '../api/types'
import { CallAudio, CallBar, CallNotices, CallsList } from '../components/Call'
import { Conversation } from '../components/Conversation'
import { Rail, type Mode } from '../components/Rail'
import { OnShelf, Recoverable, toastClass } from '../components/Recovery'
import { Button } from '../components/controls'
import { SettingsNav, SettingsPage, type Section } from '../components/Settings'
import { Sidebar } from '../components/Sidebar'
import { useInvitation } from '../desktop/useInvitation'
import { useNotifications } from '../desktop/useNotifications'
import { useWords } from '../i18n/language'
import { CallContext, useCallSession } from '../state/call'
import { EventsContext, useEventStream } from '../state/events'
import { useNarrow } from '../state/useNarrow'
import { useOwnPresence } from '../state/presence'
import { useCalls } from '../state/useCalls'
import { useRemoteRooms } from '../state/useRemoteRooms'
import { useRooms } from '../state/useRooms'

/*
refreshDelay gathers a burst of events into one read of the sidebar.

A busy room announces a message every moment, and the sidebar only needs to know
that something changed since it last looked. A quarter of a second is below
what anybody notices and above the gap between events in a burst.
*/
const refreshDelay = 250

// goneFor is how long the notice that a room was deleted stays up unless dismissed.
const goneFor = 8_000

// parseKey turns a selection back into where the room lives.
function parseKey(key: string | null): RoomSource | null {
  if (key === null) {
    return null
  }
  const [kind, id] = key.split(':', 2)
  if ((kind !== 'local' && kind !== 'remote') || id === undefined) {
    return null
  }
  return { kind, id }
}

/*
Workspace is the three zones: where in Convia, which conversation, and the
conversation itself.

The zones are a grid rather than nested flex boxes because they are peers — the
rail does not contain the sidebar, and the sidebar does not contain the stage —
and a layout that says so is one that can be rearranged for a narrow screen by
changing the grid.

**On a narrow screen one zone is shown at a time**, which is what the product
owner decided: the list, or what was chosen from it, with a way back from the
second to the first, and the rail as a bar along the bottom. Choosing a room or a
section of the settings shows it; choosing a destination, leaving a room, or
pressing back shows the list. The zone that is not shown is not rendered, so it is
neither reachable nor read out.

Settings take the same two zones: the sections where the conversations are
listed, and the section where a conversation is read.

It also holds the event stream, one for the page, and hands it down through
context: the sidebar, the open room, and its member list all listen to the same
connection rather than each opening one.

What is open is a source — a room here, or a room on another installation — kept
as one string, so the two kinds cannot be confused by sharing an identifier.
*/
export function Workspace({
  account,
  onSignedOut,
}: {
  account: Account
  onSignedOut: () => void
}) {
  const [mode, setMode] = useState<Mode>('chat')
  const [selected, setSelected] = useState<string | null>(null)

  /*
  An invitation somebody clicked, which only Convia's own application can hand
  over. It opens the join panel with the link already in it, on whichever
  screen they were on.
  */
  const invitation = useInvitation()

  const narrow = useNarrow()
  const words = useWords()
  const said = words.workspace
  const [showing, setShowing] = useState<'list' | 'chosen'>('list')
  const [section, setSection] = useState<Section>('account')

  // choose opens a room, which on a narrow screen replaces the list.
  function choose(key: string) {
    setSelected(key)
    setShowing('chosen')
  }

  function chooseSection(next: Section) {
    setSection(next)
    setShowing('chosen')
  }

  const stream = useEventStream(onSignedOut)
  const { live, listen } = stream
  const { rooms, loading, failed, refresh, remember, forget } = useRooms(onSignedOut, live)
  const elsewhere = useRemoteRooms(onSignedOut)

  useNotifications(stream, rooms, elsewhere.remoteRooms, account.user_id)

  /*
  The call this page is in, and the calls it could join. The call is held here
  rather than in a conversation, because it goes on while another room is read.
  */
  const call = useCallSession(onSignedOut, listen)
  const presence = useOwnPresence(onSignedOut, call.phase !== 'idle')
  const running = useCalls(onSignedOut, live, listen)
  const refreshCalls = running.refresh

  // Joining a call may have started one, and leaving may have ended it.
  useEffect(() => {
    refreshCalls()
  }, [call.phase, refreshCalls])

  const pending = useRef<number | undefined>(undefined)
  const refreshSoon = useCallback(() => {
    if (pending.current !== undefined) {
      return
    }

    pending.current = window.setTimeout(() => {
      pending.current = undefined
      refresh()
    }, refreshDelay)
  }, [refresh])

  useEffect(() => () => window.clearTimeout(pending.current), [])

  /*
  What the sidebar hears.

  Anything said or any change of membership may change a row, so each one asks
  for the list again, gathered. Losing one's own place is the exception worth
  acting on at once: the room goes before the read, and if it was open, nothing
  stays open that the person can no longer read.
  */
  /*
  A room deleted by its owner is said, by the name it had, because it disappears
  from the list without anybody here having done anything.
  */
  const [gone, setGone] = useState<{ id: string; name: string } | null>(null)
  const known = useRef(rooms)
  known.current = rooms

  useEffect(() => {
    if (gone === null) {
      return
    }
    const timer = window.setTimeout(() => setGone(null), goneFor)
    return () => window.clearTimeout(timer)
  }, [gone])

  useEffect(
    () =>
      listen((event) => {
        const aboutARoom = event.type.startsWith('message.') || event.type.startsWith('room.')
        if (!aboutARoom) {
          return
        }
        const roomId = event.subject.id
        const leaves =
          event.type === 'room.deleted' ||
          (event.type === 'room.member_removed' && event.data?.['user_id'] === account.user_id)
        if (leaves) {
          const room = known.current.find((candidate) => candidate.id === roomId)
          if (event.type === 'room.deleted' && room !== undefined) {
            setGone({ id: roomId, name: room.name })
          }
          forget(roomId)
          setSelected((current) => (current === sourceKey({ kind: 'local', id: roomId }) ? null : current))
        }
        refreshSoon()
      }),
    [listen, account.user_id, forget, refreshSoon],
  )

  /*
  Something has to be open, and the first room is the only defensible guess
  until the interface remembers where somebody was.

  Only an empty choice is filled. A chosen room that is momentarily missing from
  the list — opened a moment before the read that will include it, or read by a
  request that started before it existed — is left chosen, and the conversation
  reappears when the list catches up rather than jumping somewhere else first.
  */
  useEffect(() => {
    if (selected !== null) {
      return
    }
    const first = rooms[0]
    if (first !== undefined) {
      setSelected(sourceKey({ kind: 'local', id: first.id }))
      return
    }
    const firstElsewhere = elsewhere.remoteRooms[0]
    if (firstElsewhere !== undefined) {
      setSelected(sourceKey({ kind: 'remote', id: firstElsewhere.id }))
    }
  }, [rooms, elsewhere.remoteRooms, selected])

  async function create(name: string) {
    const room = await api.createRoom(name)
    remember({ id: room.id, name: room.name, status: room.status, unread: 0, owned: room.owned, moderator: false })
    choose(sourceKey({ kind: 'local', id: room.id }))
    refresh()
  }

  /*
  Joining by link opens what was joined.

  A room elsewhere becomes a pointer in the list below; a room that turned out
  to live here is simply one of this person's rooms now, and the sidebar is read
  again to show it.
  */
  async function join(link: string) {
    const joined = await api.join(link)
    if (joined.remote_room !== undefined) {
      elsewhere.remember(joined.remote_room)
      choose(sourceKey({ kind: 'remote', id: joined.remote_room.id }))
      return
    }
    remember({ id: joined.room_id, name: joined.room_name, status: 'open', unread: 0, owned: false, moderator: false })
    choose(sourceKey({ kind: 'local', id: joined.room_id }))
    refresh()
  }

  async function leave(source: RoomSource) {
    if (source.kind === 'local') {
      await api.leave(source.id)
      forget(source.id)
      refresh()
    } else {
      await api.leaveRemote(source.id)
      elsewhere.forget(source.id)
    }
    settleAfter(source)
  }

  /*
  Forgetting drops a room elsewhere from this Convia without its home. The
  interface offers it only once leaving that room has failed.
  */
  async function forgetElsewhere(source: RoomSource) {
    await api.forgetRemote(source.id)
    elsewhere.forget(source.id)
    settleAfter(source)
  }

  // settleAfter opens something else once a room has gone from the lists.
  function settleAfter(source: RoomSource) {
    setShowing('list')
    const next = rooms.find((room) => source.kind !== 'local' || room.id !== source.id)
    const nextElsewhere = elsewhere.remoteRooms.find((room) => source.kind !== 'remote' || room.id !== source.id)
    if (next !== undefined) {
      setSelected(sourceKey({ kind: 'local', id: next.id }))
    } else if (nextElsewhere !== undefined) {
      setSelected(sourceKey({ kind: 'remote', id: nextElsewhere.id }))
    } else {
      setSelected(null)
    }
  }

  // A room its owner deleted leaves the list at once, and something else opens.
  function deleted(source: RoomSource) {
    forget(source.id)
    refresh()
    settleAfter(source)
  }

  async function signOut() {
    try {
      await api.signOut()
    } finally {
      /*
      The interface returns to the sign-in form whatever the server said. The
      cookie is cleared by the response when there was one, and a sign-out that
      failed to reach Convia should still not leave somebody's conversations on
      the screen.
      */
      onSignedOut()
    }
  }

  const source = parseKey(selected)
  let open: { source: RoomSource; room: SidebarRoom; selfId: string; home?: string } | null = null
  if (source?.kind === 'local') {
    const room = rooms.find((candidate) => candidate.id === source.id)
    if (room !== undefined) {
      open = { source, room, selfId: account.user_id }
    }
  } else if (source?.kind === 'remote') {
    const remote = elsewhere.remoteRooms.find((candidate) => candidate.id === source.id)
    if (remote !== undefined) {
      open = {
        source,
        room: { id: remote.id, name: remote.name, status: 'open', unread: 0, owned: false, moderator: false },
        selfId: remote.user_id,
        home: remote.home,
      }
    }
  }

  /*
  The bar shows the call whenever its stage is not on screen: another room is
  open, or another destination is.
  */
  const callRoom = call.roomId === null ? undefined : rooms.find((candidate) => candidate.id === call.roomId)
  const settings = mode === 'settings'
  const mainShown = !narrow || (showing === 'chosen' && (settings || open !== null))
  const listShown = !narrow || !mainShown
  const stageShown = mainShown && mode === 'chat' && open?.source.kind === 'local' && open.source.id === call.roomId
  const callRoomId = call.roomId

  function openRoom(roomId: string) {
    setMode('chat')
    choose(sourceKey({ kind: 'local', id: roomId }))
  }

  const callBar =
    call.phase !== 'idle' && !stageShown && callRoomId !== null ? (
      <CallBar roomName={callRoom?.name ?? said.aRoom} onReturn={() => openRoom(callRoomId)} />
    ) : null

  return (
    <EventsContext.Provider value={stream}>
      <CallContext.Provider value={call}>
      <Recoverable zone="call sound" variant="sound" resetKey={call.roomId ?? ''}>
        <CallAudio />
      </Recoverable>
      <CallNotices />
      {gone !== null && (
        <OnShelf>
          <div role="status" className={toastClass}>
            <span>{words.workspace.roomDeleted(gone.name)}</span>
            <Button size="small" onClick={() => setGone(null)}>
              {words.recovery.dismiss}
            </Button>
          </div>
        </OnShelf>
      )}
      <h1 className="sr-only">{words.brand}</h1>
      {mainShown && (settings || open !== null) && (
        <a
          href="#conversation"
          className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-20
            focus:rounded-md focus:bg-surface-raised focus:px-3 focus:py-2"
        >
          {settings ? said.skipToSettings : said.skip}
        </a>
      )}
      <div
        className={
          narrow
            ? 'grid h-full grid-cols-1 grid-rows-[minmax(0,1fr)_auto] bg-surface'
            : 'grid h-full grid-cols-[var(--rail-width)_var(--sidebar-width)_minmax(0,1fr)] bg-surface'
        }
      >
        <Recoverable zone="rail" variant="toast" placement={narrow ? 'row-start-2' : undefined}>
          <Rail
            narrow={narrow}
            mode={mode}
            onMode={(next) => {
              setMode(next)
              setShowing('list')
            }}
            displayName={account.username}
            status={presence.status}
            said={presence.said}
            onStatus={presence.choose}
            onSignOut={() => void signOut()}
          />
        </Recoverable>

        {listShown && (
        <aside
          className={
            narrow
              ? 'row-start-1 flex min-h-0 flex-col bg-surface-deep'
              : 'flex min-h-0 flex-col border-r border-line bg-surface-deep'
          }
          aria-label={settings ? words.settings.title : said.conversations}
        >
          {narrow && callBar}
          <Recoverable zone={`${mode} list`} resetKey={mode}>
            {settings ? (
              <SettingsNav section={section} onSection={chooseSection} />
            ) : mode === 'calls' ? (
              <CallsList calls={running.calls} rooms={rooms} onOpen={openRoom} />
            ) : (
              <Sidebar
                rooms={rooms}
                remoteRooms={elsewhere.remoteRooms}
                selected={selected}
                loading={loading}
                onSelect={choose}
                onCreate={create}
                onLook={(link) => api.look(link)}
                onJoin={join}
                invitation={invitation}
              />
            )}
          </Recoverable>
          {failed && (
            <p className="m-0 border-t border-line px-4 py-2 text-[0.75rem] text-ink-faint" role="status">
              {said.retrying}
            </p>
          )}
        </aside>
        )}

        {mainShown && (
        <main
          id="conversation"
          tabIndex={-1}
          className={`flex min-h-0 min-w-0 flex-col focus:outline-none ${narrow ? 'row-start-1' : ''}`}
        >
          {callBar}
          <Recoverable
            zone={settings ? `settings ${section}` : 'conversation'}
            resetKey={settings ? `settings:${section}` : `${mode}:${selected ?? ''}`}
          >
            {settings ? (
              <SettingsPage
                section={section}
                account={account}
                onSignedOut={onSignedOut}
                {...(narrow ? { onBack: () => setShowing('list') } : {})}
              />
            ) : open === null ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-1 text-ink-dim">
                <p className="m-0">{said.nothingOpen}</p>
                <p className="m-0 text-[0.85rem] text-ink-faint">{said.pickOne}</p>
              </div>
            ) : (
              <Conversation
                key={sourceKey(open.source)}
                source={open.source}
                room={open.room}
                account={account}
                selfId={open.selfId}
                {...(open.home === undefined ? {} : { home: open.home })}
                onExpired={onSignedOut}
                onActivity={refreshSoon}
                onLeave={() => leave(open.source)}
                {...(open.source.kind === 'remote' ? { onForget: () => forgetElsewhere(open.source) } : {})}
                onRoomChanged={refresh}
                {...(open.source.kind === 'local' ? { onRoomDeleted: () => deleted(open.source) } : {})}
                callRunning={running.calls.some((candidate) => candidate.room_id === open.room.id)}
                {...(narrow ? { onBack: () => setShowing('list') } : {})}
              />
            )}
          </Recoverable>
        </main>
        )}
      </div>
      </CallContext.Provider>
    </EventsContext.Provider>
  )
}
