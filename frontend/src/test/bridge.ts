// A stand-in for the Wails bridge. Plain functions rather than vi.fn: Vitest fails a test when a spy's promise
// rejects, even one the code under test handles.
export const calls: Record<string, unknown[][]> = {};
export const answers: Record<string, (...args: unknown[]) => Promise<unknown>> =
  {};

function stub(name: string) {
  return (...args: unknown[]) => {
    (calls[name] ??= []).push(args);
    const answer = answers[name];
    return answer ? answer(...args) : Promise.resolve(undefined);
  };
}

export function reset() {
  for (const name of Object.keys(calls)) Reflect.deleteProperty(calls, name);
  for (const name of Object.keys(answers))
    Reflect.deleteProperty(answers, name);
}

export const bridge = Object.fromEntries(
  [
    "StorageProblem",
    "StartOver",
    "Me",
    "CreateIdentity",
    "RenameIdentity",
    "ExportIdentity",
    "ChooseIdentityFile",
    "ImportIdentity",
    "DeleteIdentity",
    "StartsWithWindows",
    "SetStartsWithWindows",
    "MyInvitation",
    "Contacts",
    "Requests",
    "Blocked",
    "AddContact",
    "AcceptRequest",
    "DeclineRequest",
    "DismissRequest",
    "RemoveContact",
    "BlockContact",
    "Unblock",
    "SetVerified",
    "Conversation",
    "SendMessage",
    "Conversations",
    "Viewing",
    "Notify",
    "OpenLink",
    "TrayWords",
    "SearchMessages",
    "React",
    "EditMessage",
    "DeleteMessage",
    "ForgetMessage",
    "ChooseFile",
    "Spaces",
    "SpaceMembers",
    "SpaceRoles",
    "RenameSpace",
    "RenameChannel",
    "DeleteChannel",
    "SetChannelRoles",
    "SetChannelWriters",
    "CreateRole",
    "RenameRole",
    "DeleteRole",
    "SetMemberRole",
    "SetAdministrator",
    "RemoveMember",
    "MuteMember",
    "TransferOwnership",
    "LeaveSpace",
    "DeleteSpace",
    "SpaceInvitations",
    "CreateSpace",
    "CreateChannel",
    "InviteToSpace",
    "AcceptSpace",
    "DeclineSpace",
    "ChannelMessages",
    "SayInChannel",
    "ReactInChannel",
    "EditInChannel",
    "DeleteInChannel",
    "SendFile",
    "SaveFile",
  ].map((name) => [name, stub(name)]),
);
