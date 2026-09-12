import type { SidebarRoom } from '../api/types'

interface SidebarProps {
  rooms: SidebarRoom[]
  selected: string | null
  loading: boolean
  onSelect: (roomId: string) => void
}

/*
The sidebar is the second zone: which conversation.

Every row carries its unread count, because Convia answers both in one request.
A count fetched per row would be a request per line on screen, and this is the
view that is redrawn most.
*/
export function Sidebar({ rooms, selected, loading, onSelect }: SidebarProps) {
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
      <h2
        className="mx-2 mt-0 mb-3 font-display text-[0.72rem] font-semibold tracking-[0.08em]
          text-ink-faint uppercase"
      >
        Conversations
      </h2>

      {loading && rooms.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] text-ink-faint">Loading…</p>
      ) : rooms.length === 0 ? (
        <p className="mx-2 my-0 text-[0.82rem] leading-relaxed text-ink-faint">
          You are not in any conversation yet. Somebody has to add you to a room.
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
          {rooms.map((room) => (
            <li key={room.id}>
              <button
                type="button"
                className="flex w-full cursor-pointer items-center gap-2 rounded-md px-3 py-2
                  text-left text-ink-dim transition-colors hover:bg-surface-hover hover:text-ink
                  aria-[current=true]:bg-accent-soft aria-[current=true]:text-ink"
                aria-current={room.id === selected ? 'true' : undefined}
                onClick={() => onSelect(room.id)}
              >
                <span className="flex-1 truncate">{room.name}</span>
                {room.unread > 0 && (
                  <span
                    className="min-w-5 flex-none rounded-full bg-accent px-2 py-px text-center
                      text-[0.7rem] font-semibold text-on-accent"
                  >
                    <span className="sr-only">
                      {room.unread} unread {room.unread === 1 ? 'message' : 'messages'}
                    </span>
                    <span aria-hidden="true">{room.unread > 99 ? '99+' : room.unread}</span>
                  </span>
                )}
                {room.status === 'closed' && (
                  <span
                    className="flex-none text-[0.65rem] tracking-[0.06em] text-ink-faint uppercase"
                    title="This room is closed"
                  >
                    closed
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
