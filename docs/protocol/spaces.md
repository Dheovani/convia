# The state of a space

A space is state its members replicate, changed only by operations signed by those whose rank allows them ([ADR 0013](../adr/0013-a-space-is-replicated-by-its-members.md), [ADR 0038](../adr/0038-a-space-has-three-ranks-and-roles-that-open-channels.md)). [`internal/spaces`](../../internal/spaces) is the implementation; what is signed is encoded as [canonical encodings](canonical-encodings.md) says.

## The genesis

`SpaceGenesis` begins a space: version 1, a random 16-byte identifier, the creator's identity, the space's name — 1 to 100 characters, trimmed, with no control character — and the hybrid logical clock moment of its creation, signed by the creator, who is its first owner. The identifier is never derived from a key, so it is the same space after ownership moves.

## Operations

Every change after the genesis is a `SpaceOperation`: version 1, the space's identifier, a random 16-byte identifier of its own, its author, a hybrid logical clock moment, its parents, and its change, signed by its author.

- **Hash:** a genesis or an operation is named by `SHA-256(canonical(signed fields) | signature)`, both encoded as two fields.
- **Parents** are the hashes of the operations its author had seen that no other names — or, for the first, the genesis. They are sorted byte by byte, once each, and there is at least one.
- **The change** is a kind and the fields that kind uses; every other field is empty.

| Kind                                               | Fields                                          | Rank needed   |
| -------------------------------------------------- | ----------------------------------------------- | ------------- |
| `rename-space`                                     | name                                            | administrator |
| `create-channel`, `rename-channel`                 | channel, name                                   | administrator |
| `create-voice-channel`                             | channel, name                                   | administrator |
| `delete-channel`, `open-channel`                   | channel                                         | administrator |
| `restrict-channel`                                 | channel, roles — at most 100, sorted, once each | administrator |
| `add-member`, `remove-member`                      | member                                          | administrator |
| `leave`                                            | —                                               | member        |
| `create-role`, `rename-role`                       | role, name                                      | administrator |
| `delete-role`                                      | role                                            | administrator |
| `give-role`, `take-role`                           | role, member                                    | administrator |
| `make-admin`, `unmake-admin`, `transfer-ownership` | member                                          | owner         |
| `delete-space`                                     | —                                               | owner         |
| `introduce`                                        | name — the name its author goes by in the space | member        |
| `restrict-writing`                                 | channel, roles — at most 100, sorted, once each | administrator |
| `open-writing`                                     | channel                                         | administrator |
| `mute-member`, `unmute-member`                     | member                                          | administrator |

Channels and roles are named by 16 bytes their creator chooses at random; names are as a space's are.

## Order

Every member applies the operations it knows in the same order: an operation after every operation it names, and otherwise by clock, then by identifier compared byte by byte. An operation naming one not yet known waits, and is applied once that one arrives.

## Applying an operation

An operation takes effect only if, in the state the operations before it left, its author is a member holding the rank its kind needs, and:

- a channel or role it creates has never existed in the space; one it is about exists, and so do the roles a channel is restricted to;
- a member it adds is not one, and the space has fewer than 50 members; a member it is about is one;
- the owner is never removed and never leaves; an administrator is removed only by the owner;
- `make-admin` is about a member, `unmake-admin` about an administrator, and `transfer-ownership` about anyone but the owner, who then becomes an administrator;
- the space has not been deleted.

One that does not is kept, with why, and changes nothing. A deleted role is taken from every member and every channel; a channel restricted to roles that no longer exist stays restricted, to the owner and administrators alone.

## Who sees a channel

The owner and the administrators see every channel. A member sees an open channel, and a restricted one when they hold one of its roles. Nobody sees a channel of a deleted space.

## Who writes in a channel

Whoever sees a channel reacts in it, unless muted. They write in it — messages, edits, deletions — unless muted, and, in a channel `restrict-writing` limited to roles, only if they hold one of them or are the owner or an administrator ([ADR 0038](../adr/0038-a-space-has-three-ranks-and-roles-that-open-channels.md)). `open-writing` lets every reader write again. A deleted role is taken from a channel's writers too.

## Moderation

A moderator is the owner, who moderates everyone else, or an administrator, who moderates members; both moderate those no longer in the space ([ADR 0041](../adr/0041-moderators-mute-members-and-delete-their-messages.md)).

- **`mute-member`** is made by a moderator about one they moderate; every member refuses every entry of a member muted, reactions included, until `unmute-member`. A muted member still reads.
- **A deletion** of another's message counts when its author moderates the message's author, as the space stands when it is read: the message shows as deleted by a moderator, and every member wipes its text. A deletion by anybody else is held, and changes nothing.

## Group messages

An entry of a channel is a `GroupMessage`: version 1, the space's and the channel's identifiers, its author, the author's sequence number in that channel, from 1, a random 16-byte identifier, a hybrid logical clock moment, its entry, and the entry's SHA-256, signed by its author; and, beside them, the entry's text.

- **The entry** is a `ChannelEntry` of at most 1 KiB, as its author encoded it: a message, answering the message `reply_to` names if it is set; a `reaction`; an `edit`; or a `deletion` ([ADR 0042](../adr/0042-a-channel-holds-what-a-conversation-holds-but-files.md)). A message or an edit names its text by its SHA-256; a reaction holds its emoji, and a deletion nothing more.
- **The text** travels beside the entry, and must hash to what the entry says. Once an edit or a deletion supersedes it, every member wipes it ([ADR 0034](../adr/0034-a-message-is-edited-and-deleted-by-its-author.md)), and the entry travels without it, still verifying, still holding its place.
- **The rules** are a conversation's: a message of 1 to 4,000 characters, a reaction of one emoji, and edits and deletions that count only from the message's author.

A member verifies the signature of every group message it receives, whoever delivered it, and rejects one that does not verify, whose entry the protocol does not allow, or whose text does not match it.

## Synchronizing

Two connected members of a space each tell the other what they hold, and each supplies what the other lacks:

- **Operations:** the hashes of every operation a member knows; the other sends each one missing. Somebody who is not a member is sent none.
- **Content:** for each channel, and each author in it, the highest sequence number held with every one before it. The other sends every message after it, of the channels the asking member may read now, in order. A member removed is sent nothing; a new member, holding nothing, is sent the history of every channel it may read that the other holds.

## The discovery secret

Members find one another through a secret of the space's current epoch ([ADR 0014](../adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)), from which reachability identifiers are derived as they are for contacts.

- **The epoch** is named by the hash of the latest operation applied that may revoke somebody's access — `remove-member`, `leave`, `take-role`, `delete-role`, `restrict-channel`, `unmake-admin` or `delete-space` — or by the genesis before any.
- **Its secret** is 32 random bytes, made and signed by the owner or an administrator, who makes it on such a change, or on learning of one nobody has made a secret for yet. A secret counts only while its author is the owner or an administrator. Two made for the same epoch are settled alike everywhere: the one whose SHA-256 is lower.
- **It is shared** only with current members, over their connections. A member who was removed, or left, never makes or receives the new one.

## On the connection

A space travels in `PeerMessage`s between members ([peers](peers.md)):

- **`space_sync`** opens each space shared on a connection: the hashes of every operation held. The other answers with each `space_operation` missing, and the current `space_secret`.
- **`space_holding`** follows it: how far each channel is held, author by author. The other answers with each `group_message` after it.
- **Afterwards**, a member passes on at once each operation, entry and secret new to it to every member that opened the space on its connection, but the one it came from — an entry only to those who may read its channel. What it already held it passes on to nobody, so nothing circles.
- **A member removed** is sent the operation that removed them, and nothing more — at once if connected, or when they next open the space with a member.

## Voice channels

A member in a voice channel says so with `voice_presence` ([ADR 0047](../adr/0047-a-call-in-a-space-is-a-voice-channel.md)): the space, the channel, itself, the moment in milliseconds, whether it left, and whether its camera is on, signed by it ([canonical encodings](canonical-encodings.md)). It says so again every 30 seconds, and once more when it leaves.

- **A presence is believed** when it comes from a member of the space connected, names a voice channel its signer may see, is signed by them, is within 90 seconds of this member's clock, and is later than the last one its signer signed. Otherwise it is dropped.
- **A presence believed is passed on** to every member that opened the space on its connection and may see the channel, but the one it came from and its signer; a member that opens the space is sent every presence held that it may see, and the member's own.
- **A member is taken to be in one voice channel**, the one its latest presence names, until it leaves or 90 seconds pass with no new one.
- **A member in a voice channel connects to each other member there**, and lets go of those who left.

At most 12 are in a voice channel, and at most 6 while one of them has a camera on: a member does not join, or turn its camera on, beyond it.

## Invitations

When an administrator adds a person ([ADR 0039](../adr/0039-a-person-joins-a-space-when-an-administrator-adds-a-contact.md)), the person is sent `space_invitation`: the genesis and every operation, which must add them and come from an administrator. They are sent nothing else until they accept. Accepting keeps the space and opens it with every member connected; declining sends the inviter a `leave` operation, as soon as the inviter can be reached.

## Who connects to whom

Each member announces, at the rendezvous service, its reachability identifier under the current epoch's secret, and watches every other member's, for the day before, the day and the day after ([ADR 0014](../adr/0014-members-of-a-space-share-a-secret-to-tell-who-is-online.md)). A member announced is online until its connection is lost.

Each member then connects to a few of the members online ([ADR 0040](../adr/0040-members-of-a-space-connect-to-a-few-of-one-another.md)): the online members and itself are ordered by `SHA-256(epoch | identity)`, and it connects to the one before it and the one after it, and to the 2 others whose `SHA-256(SHA-256(epoch | own identity) | identity)` is lowest. A member who writes in a restricted channel also connects to the readers of that channel who are online and not connected, until they have caught up.

## Files

A file in a channel is an entry: a `ChannelEntry` holding `file` — its size, of 1 byte to 100 MB, its type, its SHA-256 and its orientation, as in a conversation ([peers](peers.md)) — whose text is the file's name ([ADR 0043](../adr/0043-a-spaces-files-are-served-by-whoever-holds-them.md)). It answers nothing, and an edit does not rename it; a deletion deletes it, and every member then removes what it holds of it.

- **Pulled:** a picture of at most 10 MB as soon as its entry arrives; any other file only once the member asks for it.
- **Asked for** a span of at most 1 MiB at a time, with `file_request`, of one member connected who may read the channel and has not said it does not hold it; when none is left, its author is connected to on demand. A member connected again is asked again.
- **Served** as a conversation's file is, in `file_chunk`s on the channel for files, by any member holding it whole to any member who may read its channel. A member that does not hold it whole, or will not serve it to whoever asked, answers `file_missing`.
- **Checked** once whole against the SHA-256 its author signed, whoever served it; a file that does not match is dropped, and asked of another.
