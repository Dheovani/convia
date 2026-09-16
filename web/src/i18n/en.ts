import type { DeviceKind, Refusal } from '../media/connection'
import { plural } from './plural'

/*
en is every word the interface says, in English.

It is the shape every other language follows: a phrase that takes something —
a name, a count, a device — is a function, so each language builds its own
sentence around it rather than filling a slot in an English one.
*/

const devices: Record<DeviceKind, string> = {
  audioinput: 'microphone',
  videoinput: 'camera',
  audiooutput: 'speaker',
}

const deviceNames: Record<DeviceKind, string> = {
  audioinput: 'Microphone',
  videoinput: 'Camera',
  audiooutput: 'Speaker',
}

export const en = {
  brand: 'Convia',

  common: {
    loading: 'Loading…',
    cancel: 'Cancel',
    save: 'Save',
    saving: 'Saving…',
    remove: 'Remove',
    closed: 'closed',
    you: 'You',
    somebody: 'Somebody',
    unnamed: 'Somebody unnamed',
    unreachable: 'Convia could not be reached. Try again.',
  },

  // refusals say what a refusal means by its code, for the failures nothing more specific explains.
  refusals: {
    invalid_request: 'Convia did not accept that.',
    malformed_json: 'Convia did not accept that.',
    forbidden: 'You are not allowed to do that.',
    conflict: 'That cannot be done right now.',
    precondition_failed: 'That changed in the meantime. Try again.',
    rate_limited: 'Too many attempts. Wait a moment and try again.',
    payload_too_large: 'That is too long.',
    internal_error: 'Something went wrong in Convia. Try again.',
  } as Partial<Record<string, string>>,

  app: {
    loading: 'Loading Convia',
  },

  recovery: {
    pageBroken: 'Convia stopped working.',
    zoneBroken: 'This part of Convia stopped working.',
    soundBroken: "The call's sound stopped working.",
    hint: 'Trying again usually fixes it. Reloading the page also ends a call you are in.',
    tryAgain: 'Try again',
    reload: 'Reload the page',
    details: 'Details to report',
    copy: 'Copy details',
    copied: 'Copied',
    unexpected: 'Something went wrong. If the page stops responding, reload it.',
    dismiss: 'Dismiss',
  },

  signIn: {
    unreachable: 'Convia could not be reached. Check your connection and try again.',
    notAccepted: 'Convia did not accept that username or password. Check the rules under each field.',
    mismatch: 'That username and password do not match an account.',
    foreignPage: 'This page could not prove it came from Convia. Reload and try again.',
    taken: 'That username is taken. Choose another.',
    tooManyAccounts: 'Too many accounts were attempted from here. Try again later.',
    tooManyAttempts: 'Too many attempts from here. Wait a moment and try again.',
    busy: 'Convia is busy right now. Try again in a moment.',
    registerFailed: 'Convia could not create the account. Try again.',
    signInFailed: 'Convia could not sign you in. Try again.',
    badUsername: 'That username is not one Convia accepts. Check the rule under it.',
    shortPassword: (minimum: number) =>
      plural('en', minimum, {
        one: `A password must be at least ${minimum} character.`,
        other: `A password must be at least ${minimum} characters.`,
      }),
    passwordsDiffer: 'The two passwords do not match.',
    registerTitle: 'Create your account',
    signInTitle: 'Sign in',
    registerLead: 'Your account lives on this Convia, and nowhere else.',
    signInLead: 'Talk, meet, and stay in touch.',
    username: 'Username',
    usernameRule:
      '3 to 32 characters: letters, digits, dots, dashes and underscores, starting with a letter or a digit.',
    password: 'Password',
    passwordRule: (minimum: number) =>
      plural('en', minimum, {
        one: `At least ${minimum} character. That is the only rule.`,
        other: `At least ${minimum} characters. That is the only rule.`,
      }),
    confirmation: 'Confirm password',
    keepSafe: 'Keep this password safe.',
    keepSafeWhy:
      "It locks your account's key, so nobody can reset it, not even whoever runs this Convia. If you forget it, the account is lost.",
    createAccount: 'Create account',
    creatingAccount: 'Creating account…',
    signIn: 'Sign in',
    signingIn: 'Signing in…',
    haveAccount: 'I already have an account',
    wantAccount: 'Create an account',
  },

  presence: {
    online: 'Available',
    busy: 'Busy',
    away: 'Away',
    offline: 'Offline',
    yours: 'Your status',
    current: (state: string) => `Your status: ${state}`,
    idle: 'An idle page shows you as away.',
  },

  rail: {
    chat: 'Chat',
    calls: 'Calls',
    settings: 'Settings',
    signOut: 'Sign out',
  },

  settings: {
    title: 'Settings',
    sections: {
      account: 'Account',
      appearance: 'Appearance',
      language: 'Language',
      calls: 'Calls',
    },
    back: 'Back to settings',
    keptHere: 'Kept in this browser.',

    handleHeading: 'Your handle',
    handleHint: 'People on any Convia invite you with it. Send it to whoever wants to invite you.',
    copyHandle: 'Copy handle',
    copied: 'Copied',

    passwordHeading: 'Change your password',
    currentPassword: 'Current password',
    newPassword: 'New password',
    confirmPassword: 'Confirm new password',
    changePassword: 'Change password',
    changingPassword: 'Changing…',
    passwordChanged: 'Your password was changed. Everywhere else you were signed in has been signed out.',
    wrongPassword: 'That is not your current password.',
    tooManyAttempts: 'Too many attempts from here. Wait a minute and try again.',
    passwordNotAccepted: 'Convia did not accept that password. Check the rule under it.',
    passwordFailed: 'The password could not be changed. Try again.',

    everywhereHeading: 'Sign out everywhere',
    everywhereHint: 'Ends every session of this account, on every device, including this one.',
    everywhere: 'Sign out everywhere',
    confirmEverywhere: 'Sign out on every device, including this one?',
    signingOut: 'Signing out…',
    stay: 'Stay signed in',

    theme: 'Theme',
    system: 'Match the system',
    dark: 'Dark',
    light: 'Light',

    languageLegend: 'Language the interface speaks',
    browserLanguage: (name: string) => `The browser's language (${name})`,

    callsHint: 'How calls start, and which devices they use.',
    startMicrophone: 'Start calls with the microphone on',
    startCamera: 'Start calls with the camera on',
    checkDevices: 'Check my devices',
    stopChecking: 'Stop checking',
    checking: 'Checking your devices',
    inCall: 'You are in a call, so its devices cannot be checked here. What you choose applies to the call at once.',
  },

  workspace: {
    skip: 'Skip to the conversation',
    skipToSettings: 'Skip to the settings',
    conversations: 'Conversations',
    retrying: 'Convia could not be reached. Retrying.',
    nothingOpen: 'Nothing is open.',
    pickOne: 'Pick a conversation on the left, or open a new one.',
    aRoom: 'a room',
    roomDeleted: (room: string) => `${room} was deleted.`,
  },

  sidebar: {
    heading: 'Conversations',
    cannotOpen: 'You cannot open rooms right now.',
    openFailed: 'The room could not be opened.',
    notALink: 'That is not a Convia invitation link.',
    unusableLink: 'That invitation cannot be used. It may have expired, been used, or been meant for somebody else.',
    alreadyIn: 'You are already in that room.',
    homeUnreachable: 'The Convia that link points to could not be reached. Try again in a moment.',
    linkFailed: 'That link could not be used. Try again.',
    nameLabel: 'Conversation name',
    namePlaceholder: 'Name it',
    opening: 'Opening…',
    create: 'Create',
    linkLabel: 'Invitation link',
    linkPlaceholder: 'Paste the link you were sent',
    looking: 'Looking…',
    look: 'Look',
    invitedBy: (inviter: string, host: string) => `Invited by ${inviter}, on ${host}.`,
    joining: 'Joining…',
    join: 'Join',
    joinWithLink: 'Join with a link',
    newConversation: 'New conversation',
    empty: 'You are not in any conversation yet. Open one, or join one with a link somebody sent you.',
    unread: (count: number) =>
      plural('en', count, { one: `${count} unread message`, other: `${count} unread messages` }),
    manyUnread: '99+',
    roomClosed: 'This room is closed',
    elsewhereLabel: 'Rooms on other Convias',
    elsewhere: 'Elsewhere',
  },

  conversation: {
    removedByOwner: "Removed by the room's owner or a moderator.",
    withdrawn: 'This message was withdrawn.',
    editLabel: 'Edit message',
    edited: '(edited)',
    editedAt: (at: string) => `Edited ${at}`,
    edit: 'Edit',
    withdraw: 'Withdraw',
    speaker: (name: string) => `${name}: `,
    guest: 'A guest',
    somebodyWhoLeft: 'Somebody who left',
    back: 'Back to conversations',
    people: 'People',
    unreadable: 'This conversation could not be read. Convia will try again.',
    empty: 'Nothing has been said here yet.',
  },

  composer: {
    unreachable: 'That did not send — Convia could not be reached.',
    closed: 'This room is closed. Nothing more can be said here.',
    gone: 'This room is no longer available to you.',
    failed: 'That did not send.',
    label: (room: string) => `Message ${room}`,
    closedPlaceholder: 'This room is closed',
    send: 'Send',
  },

  people: {
    label: (room: string) => `People in ${room}`,
    livesOn: (host: string) => `This room lives on ${host}. You take part through your own Convia.`,
    inRoom: 'In this room',
    membersUnreadable: 'Who is here could not be read.',
    owner: 'owner',
    moderator: 'moderator',
    youTag: '(you)',
    remove: (name: string) => `Remove ${name}`,
    removeFailed: (name: string) => `${name} could not be removed.`,
    ban: 'Ban',
    banNamed: (name: string) => `Ban ${name}`,
    banFailed: (name: string) => `${name} could not be banned.`,
    nameModerator: 'Make moderator',
    nameModeratorNamed: (name: string) => `Make ${name} a moderator`,
    unnameModerator: 'Unmake moderator',
    unnameModeratorNamed: (name: string) => `Stop ${name} moderating`,
    roleFailed: (name: string) => `What ${name} may do could not be changed.`,
    handOver: 'Make owner',
    handOverNamed: (name: string) => `Make ${name} the owner`,
    confirmHandOver: (name: string, room: string) =>
      `Make ${name} the owner of ${room}? You stay in it as a member, and only ${name} can give it back.`,
    handOverConfirm: 'Make owner',
    handingOver: 'Handing over…',
    keepOwning: 'Keep it',
    handOverFailed: (name: string) => `${name} could not be made the owner.`,
    addHeading: 'Add somebody',
    candidatesUnreadable: 'The people you could add could not be read.',
    nobodyShared: 'You do not share a room with anybody else yet.',
    everybodyHere: 'Everybody you share a room with is already here.',
    add: 'Add',
    addNamed: (name: string) => `Add ${name}`,
    adding: 'Adding…',
    cannotAdd: (name: string) => `${name} cannot be added to this room.`,
    addHint: 'You can add people you already share a room with.',
    banned: 'Banned',
    bansUnreadable: 'Who is banned could not be read.',
    nobodyBanned: 'Nobody is banned from this room.',
    unban: 'Unban',
    unbanNamed: (name: string) => `Unban ${name}`,
    unbanFailed: (name: string) => `${name} could not be unbanned.`,
    bansHint: 'Nobody can add or invite somebody banned until you lift it. Somebody you only remove can be added back.',
    inviteHeading: 'Invite by handle',
    handle: 'Handle',
    handlePlaceholder: 'name#IDENTIFIER',
    inviting: 'Inviting…',
    invite: 'Invite',
    badHandle: 'That handle is not right. Check it with the person — every character counts.',
    noLongerIn: 'You are no longer in this room.',
    alreadyIn: 'That person is already in this room.',
    inviteFailed: 'The invitation could not be made. Try again.',
    sendLink: (invitee: string) => `Send this link to ${invitee}. It works once, for a day, and only for them.`,
    linkLabel: 'Invitation link',
    copied: 'Copied',
    copy: 'Copy link',
    thisComputer:
      'This link names this computer, so it only works for somebody using Convia on this same machine. Open Convia at an address others can reach to invite them.',
    anyConvia: 'People on any Convia can join with their handle. They find it in their settings.',
    pendingHeading: 'Waiting to join',
    pendingUnreadable: 'The invitations you made could not be read.',
    until: (time: string) => `until ${time}`,
    copyFor: (invitee: string) => `Copy the link for ${invitee}`,
    withdraw: 'Withdraw',
    withdrawFor: (invitee: string) => `Withdraw the invitation for ${invitee}`,
    withdrawFailed: 'The invitation could not be withdrawn. Try again.',
    notConfirmed:
      'The Convia this room lives on did not confirm that you left, so you are still in it. Try again later.',
    forgetFailed: 'This room could not be forgotten. Try again.',
    forgetInstead: (host: string) =>
      `You can forget it here instead. It leaves your list, but ${host} still counts you as a member, and nothing here can take you out of it later.`,
    forgetting: 'Forgetting…',
    forget: 'Forget it here',
    confirmLeave: (room: string) => `Leave ${room}? What you said stays.`,
    leaving: 'Leaving…',
    leave: 'Leave',
    stay: 'Stay',
    leaveRoom: 'Leave this room',
  },

  roomSettings: {
    open: 'Room',
    label: (room: string) => `Settings for ${room}`,
    failed: 'That could not be done. Try again.',
    name: 'Room name',
    confirmDelete: (room: string) => `Delete ${room} for everybody in it?`,
    deleting: 'Deleting…',
    delete: 'Delete',
    keep: 'Keep it',
    rename: 'Rename',
    reopen: 'Reopen',
    close: 'Close',
    deleteRoom: 'Delete room',
  },

  call: {
    start: 'Start call',
    join: 'Join call',
    cannotStart: 'A closed room does not start new calls.',
    dismiss: 'Dismiss',

    // refused says why a device could not be had while getting ready to join.
    refused: (refusal: Refusal, kind: 'audioinput' | 'videoinput') => {
      const device = devices[kind]
      switch (refusal) {
        case 'denied':
          return `Convia is not allowed to use your ${device}. Allow it from the site settings beside the address bar, then try again.`
        case 'missing':
          return `No ${device} was found.`
        case 'busy':
          return `Your ${device} is being used by another app.`
        case 'failed':
          return `Your ${device} could not be started.`
      }
    },
    // failed says what happened to a device in the middle of a call.
    failed: (refusal: Refusal, kind: DeviceKind | undefined) => {
      const device = kind === undefined ? 'microphone or camera' : devices[kind]
      switch (refusal) {
        case 'denied':
          return `Convia is no longer allowed to use your ${device}.`
        case 'missing':
          return `Your ${device} was disconnected.`
        case 'busy':
          return `Your ${device} is being used by another app.`
        case 'failed':
          return `Your ${device} stopped working.`
      }
    },
    replaced: (kind: DeviceKind) =>
      `Your ${devices[kind]} was disconnected, so the call is using the system default.`,
    unheard: 'Your microphone is not available, so nobody can hear you.',
    unseen: 'Your camera is not available, so nobody can see you.',
    microphoneUnavailable: 'Your microphone is not available.',
    cameraUnavailable: 'Your camera is not available.',
    deviceUnusable: 'That device could not be used.',

    device: (kind: DeviceKind) => deviceNames[kind],
    numbered: (kind: DeviceKind, position: number) => `${deviceNames[kind]} ${position}`,
    systemDefault: 'System default',
    level: 'Microphone level',

    prepare: 'Prepare to join',
    startingDevices: 'Starting your devices…',
    cameraOff: 'Your camera is off',
    microphoneOn: 'Turn microphone on',
    microphoneOff: 'Turn microphone off',
    cameraOn: 'Turn camera on',
    cameraOffAction: 'Turn camera off',
    tryAgain: 'Try again',

    weakConnection: 'weak connection',
    lostConnection: 'connection lost',
    muted: 'muted',
    takeOut: (name: string) => `Take ${name} out of the call`,

    stage: 'Call',
    joiningCall: 'Joining the call…',
    reconnecting: 'Reconnecting to the call…',
    weak: 'Your connection is weak. Others may not hear or see you well.',
    audioOnly: 'Audio only: video is paused to spare your connection.',
    resumeVideo: 'Turn video back on',
    weakForAWhile: 'Your connection has been weak for a while.',
    goAudioOnly: 'Continue with audio only',
    notNow: 'Not now',
    people: 'People in the call',
    joiningPerson: 'Joining…',
    controls: 'Call controls',
    mute: 'Mute',
    unmute: 'Unmute',
    startCamera: 'Start camera',
    stopCamera: 'Stop camera',
    devices: 'Devices',
    leave: 'Leave call',

    notices: 'Call notices',
    joined: (name: string) => `${name} joined the call.`,
    left: (name: string) => `${name} left the call.`,

    current: 'Current call',
    joiningIn: 'Joining the call in ',
    inCallIn: 'In a call in ',
    reconnectingShort: ' — reconnecting…',
    return: 'Return',

    list: 'Calls',
    noneRunning: 'No call is running in your rooms. Start one from a conversation.',
    since: (time: string) => `since ${time}`,

    forbidden: 'You cannot join this call. You may have been taken out of it.',
    roomGone: 'This room is no longer available.',
    cannotJoin: 'This call cannot be joined right now. A closed room does not start new calls.',
    noCalls: 'Calls cannot be held here right now.',
    joinFailed: 'The call could not be joined. Try again.',
    notOwner: "Only the room's owner can take somebody out of the call.",
    notInCall: 'That person is no longer in the call.',
    joinFirst: 'Join the call to take somebody out of it.',
    removeFailed: 'That person could not be taken out of the call.',
    removed: 'You were taken out of the call.',
    ended: 'The call ended.',
    elsewhere: 'You joined this call somewhere else, so it closed here.',
    lost: 'The connection to the call was lost.',
    mediaUnreachable: "The call's media server could not be reached.",
  },
}

/*
Words is the shape of a language: the English one, with every phrase allowed to
say anything. A language that leaves out a phrase, or adds one, does not compile.
*/
type Widen<T> = T extends string
  ? string
  : T extends (...args: infer A) => string
    ? (...args: A) => string
    : { [K in keyof T]: Widen<T[K]> }

export type Words = Widen<typeof en>
