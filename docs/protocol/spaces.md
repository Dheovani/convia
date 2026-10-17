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
| `delete-channel`, `open-channel`                   | channel                                         | administrator |
| `restrict-channel`                                 | channel, roles — at most 100, sorted, once each | administrator |
| `add-member`, `remove-member`                      | member                                          | administrator |
| `leave`                                            | —                                               | member        |
| `create-role`, `rename-role`                       | role, name                                      | administrator |
| `delete-role`                                      | role                                            | administrator |
| `give-role`, `take-role`                           | role, member                                    | administrator |
| `make-admin`, `unmake-admin`, `transfer-ownership` | member                                          | owner         |
| `delete-space`                                     | —                                               | owner         |

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

## Group messages

An entry of a channel is a `GroupMessage`: version 1, the space's and the channel's identifiers, its author, the author's sequence number in that channel, from 1, a random 16-byte identifier, a hybrid logical clock moment, its content — at most 64 KiB — and the content's SHA-256, signed by its author. A member verifies the signature of every group message it receives, whoever delivered it, and rejects one that does not verify, or whose content does not match its digest. A message whose content was wiped since is sent without it, and still verifies.

A message is accepted only if its author is a member who sees its channel at the moment it arrives: a removed member's further messages are refused, and so, since nothing tells them from earlier ones, are the earlier ones a member did not yet hold. Two messages in the same place of an author's sequence, or with the same identifier, are refused as a conflict.

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
- **A member removed** is sent the operation that removed them, and nothing more.

## Invitations

When an administrator adds a person ([ADR 0039](../adr/0039-a-person-joins-a-space-when-an-administrator-adds-a-contact.md)), the person is sent `space_invitation`: the genesis and every operation, which must add them and come from an administrator. They are sent nothing else until they accept. Accepting keeps the space and opens it with every member connected; declining sends the inviter a `leave` operation, as soon as the inviter can be reached.
