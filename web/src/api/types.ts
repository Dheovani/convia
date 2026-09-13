/*
The shapes Convia's session surface answers with.

They are written by hand rather than generated. The surface a person reaches is
small and deliberately narrow, and a generator would pull in every operator and
tenant operation alongside it — most of which this interface must never call.
Generation is worth revisiting when the SDK arrives (M19); until then the cost
of keeping thirty lines in step with the contract is lower than the cost of the
tooling.
*/

// Account is the person the session belongs to.
export interface Account {
  account_id: string
  user_id: string
  email: string
  display_name: string
}

export type RoomStatus = 'open' | 'closed' | 'deleted'

// SidebarRoom is one row of the room list: what to show, and whether to look.
export interface SidebarRoom {
  id: string
  alias?: string
  name: string
  status: RoomStatus
  unread: number
}

export interface Sidebar {
  data: SidebarRoom[]
  next_cursor?: string
}

/*
Message carries an author that may be absent.

A withdrawn message keeps its place in the order and loses its body and its
author, so `body` and `user_id` are optional for a reason that is part of the
domain rather than an accident of serialisation.
*/
export interface Message {
  id: string
  application_id: string
  room_id: string
  sequence: number
  user_id?: string
  invitation_id?: string
  body?: string
  deleted: boolean
  created_at: string
  edited_at?: string
  deleted_at?: string
}

export interface MessagePage {
  data: Message[]
  next_cursor?: string
}

export interface ReadState {
  room_id: string
  user_id: string
  sequence: number
  unread: number
  updated_at?: string
}

export type HistoryDirection = 'older' | 'newer'

// OwnRoom is a room as the person who opened it sees it.
export interface OwnRoom {
  id: string
  name: string
  status: RoomStatus
  created_at: string
}

/*
Person is somebody this person can see, by the name they go by.

It exists on this surface and not on the application's, where a member carries
no name: an application owns its people's names and reads them itself, and a
person has no other way to learn what to call somebody.
*/
export interface Person {
  user_id: string
  display_name: string
}

export interface PersonPage {
  data: Person[]
  next_cursor?: string
}

export interface RoomMember {
  application_id: string
  room_id: string
  user_id: string
  created_at: string
}
