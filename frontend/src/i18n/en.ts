/*
Everything the interface says, in English, which is also the shape every other
language has to fill: a phrase missing from a translation fails to compile.
*/
export const en = {
  brand: "Convia",
  failure: {
    message: "Something went wrong.",
    reload: "Reload",
  },
  refusals: {
    name: "A name has 1 to 32 characters.",
    "weak-password": "The password needs at least 12 characters.",
    password: "That password does not open this file.",
    "export-file": "This is not an exported Convia identity, or it is damaged.",
    newer: "This data belongs to a newer Convia.",
    "no-identity": "There is no identity yet.",
    "invitation-checksum": "This code was not copied whole, or not exactly.",
    "invitation-malformed": "This is not a Convia invitation.",
    "invitation-newer":
      "This invitation was made by a newer Convia. Update Convia to use it.",
    "invitation-signature": "This invitation was changed after it was made.",
    "invitation-yours": "This is your own invitation.",
    "already-contact": "This person is already one of your contacts.",
    blocked: "You blocked this person. Unblock them first.",
    "no-such-contact": "That contact or request no longer exists.",
    "message-too-long": "A message holds at most 4,000 characters.",
    "message-empty": "There is nothing to send.",
    "no-such-message": "That message is not in this conversation.",
    "not-an-emoji": "A reaction is one emoji.",
    unknown: "Something went wrong.",
  },
  home: {
    contacts: "Contacts",
    you: "You",
    outdatedTheirs: (name: string) =>
      `${name} uses an older Convia. You can talk again once they update it.`,
    outdatedMine: (name: string) =>
      `${name} uses a newer Convia. Update Convia to talk to them.`,
    someone: "A contact",
  },
  contacts: {
    invitationTitle: "Your invitation",
    invitationExplanation:
      "Give this code to whoever should be able to ask you. Anybody who has it can ask; only those you accept become contacts.",
    copy: "Copy",
    copied: "Copied.",
    addTitle: "Add a contact",
    addExplanation:
      "Paste the invitation they gave you. They see your request and choose whether to accept it.",
    code: "Their invitation",
    ask: "Send the request",
    asked: (name: string) => `Request sent to ${name}.`,
    safetyCode: "Safety code",
    safetyHint:
      "Compare it with them, in person or on a call. If it matches, nobody is in between.",
    received: "Asking to be your contact",
    accept: "Accept",
    decline: "Decline",
    block: "Block",
    sent: "Your requests",
    waiting: "Waiting for an answer",
    notAccepted: "Not accepted",
    dismiss: "Dismiss",
    list: "Your contacts",
    none: "No contacts yet.",
    verified: "Verified",
    verify: (name: string) => `I compared this code with ${name}`,
    remove: "Remove",
    forget: "Also delete our conversation",
    removeHint: "They are not told.",
    blocked: "Blocked",
    unblock: "Unblock",
    open: "Open the conversation",
  },
  workspace: {
    directMessages: "Direct messages",
    contacts: "Contacts",
    settings: "Settings",
    home: "Direct messages",
    conversations: "Conversations",
    unread: (count: number) => `${count} unread`,
    requests: (count: number) => `${count} waiting for your answer`,
    noConversations:
      "Nobody to talk to yet. Add a contact with their invitation.",
    choose: "Choose a conversation.",
    you: "You",
    verified: "Verified",
    search: "Search messages",
    searchNone: "Nothing found.",
    searchFound: (count: number) =>
      count === 1 ? "1 message found" : `${count} messages found`,
  },
  settings: {
    title: "Settings",
    language: "Language",
    languageSystem: (name: string) => `As the system (${name})`,
    theme: "Theme",
    themes: { system: "As the system", dark: "Dark", light: "Light" },
    notificationsTitle: "Notifications",
    notificationText: "Show what a message says in its notification",
    notificationTextHint:
      "Off, a notification says only who wrote, so nobody near the screen reads it.",
  },
  tray: {
    open: "Open Convia",
    autostart: "Start with Windows",
    quit: "Quit Convia",
  },
  notification: {
    title: (name: string) => `New message from ${name}`,
  },
  link: {
    title: "Open this link?",
    explanation: "It opens in your browser, which the site will see.",
    open: "Open",
    cancel: "Cancel",
  },
  conversation: {
    back: "Back",
    none: "Nothing said yet.",
    field: (name: string) => `Message to ${name}`,
    send: "Send",
    sendHint: "Enter sends; Shift+Enter starts a new line.",
    left: (count: number) => `${count} characters left`,
    over: (count: number) => `${count} characters too many`,
    waiting: "Waiting",
    sent: "Sent",
    delivered: "Delivered",
    reply: "Reply",
    react: "React",
    chooseReaction: "Choose a reaction",
    reactWith: (emoji: string) => `React with ${emoji}`,
    reaction: (emoji: string, count: number, mine: boolean) =>
      `${emoji} ${count}${mine ? ", including you" : ""}`,
    replyingTo: (name: string) => `Replying to ${name}:`,
    cancelReply: "Cancel",
    quoteMissing: "The message replied to is not here.",
    waitingHint: (name: string) =>
      `It waits on this computer until ${name} is online.`,
  },
  welcome: {
    title: "Welcome to Convia",
    explanation:
      "Convia makes your identity here, on this computer. Nobody else issues it or holds it.",
    name: "Your name",
    nameHint: "What your contacts see. It does not need to be unique.",
    create: "Create my identity",
    haveOne: "I have an exported identity",
  },
  importing: {
    title: "Import an identity",
    chooseFile: "Choose the file",
    noFile: "No file chosen",
    password: "Its password",
    import: "Import",
    replaceWarning:
      "This computer already has an identity. Importing sets it aside, with its history, in a folder of its own; nothing is deleted.",
    replace: "Set mine aside and import",
    cancel: "Cancel",
  },
  me: {
    name: "Name",
    rename: "Change name",
    save: "Save",
    publicKey: "Your public key",
    publicKeyHint:
      "This is who you are to your contacts. Your name can change; this cannot.",
    exportTitle: "Export your identity",
    exportExplanation:
      "Keep a copy, protected by a password, to bring your identity to another computer. Without it, losing this computer loses your identity.",
    password: "Password",
    confirmPassword: "The same password again",
    mismatch: "The two passwords differ.",
    export: "Export",
    exported: "Exported.",
    deleteTitle: "Delete your identity",
    deleteExplanation:
      "This destroys your identity, your contacts and your conversations on this computer. Your contacts are not told. Export it first if you may want it back.",
    deleteConfirm: "I understand this cannot be undone",
    delete: "Delete my identity",
  },
  dataProblem: {
    title: {
      newer: "This data belongs to a newer Convia.",
      damaged: "Convia cannot read its data.",
      "key-unreadable": "Convia cannot unlock its data.",
      other: "Convia cannot open its data.",
    },
    explanation: {
      newer:
        "It was written by a newer version of Convia. Update Convia to open it; nothing has been changed.",
      damaged: "The file that holds your conversations is damaged.",
      "key-unreadable":
        "Its key no longer opens, which happens when the Windows account it belonged to has changed.",
      other:
        "Something stopped Convia from opening its data. The log says what.",
    },
    keptAside:
      "Starting over keeps the unreadable files in a folder of their own; nothing is deleted.",
    startOver: "Start over",
    importInstead: "Import an exported identity instead",
  },
};

export type Words = typeof en;
