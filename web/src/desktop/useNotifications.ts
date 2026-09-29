import { useEffect, useRef } from 'react'

import type { ConviaEvent, RemoteRoom, SidebarRoom } from '../api/types'
import { useWords } from '../i18n/language'
import type { Events } from '../state/events'
import { desktop, inApplication } from './bridge'

export function useNotifications(
  events: Events,
  rooms: SidebarRoom[],
  remoteRooms: RemoteRoom[],
  selfId: string,
): void {
  const words = useWords()

  // Keep changing room data out of the subscription dependencies so events
  // cannot be lost while the listener is replaced.
  const current = useRef({ rooms, remoteRooms, selfId, words })
  current.current = { rooms, remoteRooms, selfId, words }

  useEffect(() => {
    if (!inApplication()) {
      return
    }

    return events.listen((event: ConviaEvent) => {
      const { rooms: here, remoteRooms: elsewhere, selfId: mine, words: said } = current.current

      const roomId = typeof event.data?.room_id === 'string' ? event.data.room_id : undefined
      if (roomId === undefined) {
        return
      }

      const name =
        here.find((room) => room.id === roomId)?.name ??
        elsewhere.find((room) => room.id === roomId)?.name
      if (name === undefined) {
        return
      }

      if (event.type === 'call.started') {
        void desktop.notify(name, said.notifications.call).catch(() => {})
        return
      }

      if (event.type === 'message.posted' && event.data?.user_id !== mine) {
        void desktop.notify(name, said.notifications.message).catch(() => {})
      }
    })
  }, [events])
}
