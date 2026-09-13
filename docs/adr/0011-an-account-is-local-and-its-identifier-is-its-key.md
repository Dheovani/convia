# ADR 0011 — An account is local, and its identifier is its key

**Status:** Accepted. Supersedes the *Accounts are created by an operator* section of [ADR 0007](0007-a-session-is-a-person-not-a-tenants-authority.md).
**Date:** 2026-09-13
**Milestone:** M18 — Standalone Web Application

## Context

ADR 0007 made accounts something an operator created: an email address, a password Convia generated and printed once, and the first-party application configured by identifier in `CONVIA_FIRST_PARTY_APPLICATION`. It justified the absence of sign-up by the absence of a mailer.

That shape assumed an administrator and a mailer, and the person Convia is for has neither. Somebody who installs Convia on their own machine to talk to people met a sign-in form that refused everyone, a 404 that said nothing useful when the variable was unset, and no way to get an account without running a command against the database. The decision was made without the product owner, and it was wrong for the product.

The product owner's direction: **each installation holds as many accounts as people create on it; a person registers a username and a password in the app; there is no email; an account has an automatically generated identifier and a chosen username; people are invited by the combination of the two; the model is a password manager's local file.** Invitations will travel directly between installations, by a link carrying the inviting installation's address.

## Decision

### A person creates their own account, and there is nothing to configure

`POST /v1/accounts` takes a username and a password, creates the account, and signs its owner in. The first-party application is a fixed row, `app_CONVIAAAAAAAAAAAAAAAAAAAAA`, which Convia makes on its first start and never touches again. `CONVIA_FIRST_PARTY_APPLICATION` and `convia account create` are gone; `convia account suspend` and `activate` stay.

### The identifier is the fingerprint of a key

Each account has an Ed25519 key pair generated at registration. Its identifier is `acc_` followed by the first sixteen bytes of the SHA-256 digest of the public key, in base32 — the same shape as every other Convia identifier.

The alternative considered was a random identifier, with the current time in milliseconds mixed in to make collisions less likely. It was rejected for two reasons, and the second is the one that matters:

- **Collisions were never the risk.** 128 random bits collide with probability around 10⁻²² across a billion accounts. Mixing in the time does not lower that in any way that matters, and it publishes when each account was created to everybody it is shared with.
- **Forgery is the risk.** Once invitations travel between installations, each installation controls its own database and can write any identifier and username into it — including somebody else's, copied from a message. An identifier that proves nothing is copied, not guessed. An identifier that is a key's fingerprint can be challenged: whoever claims it signs with the private half, and producing a different key with the same fingerprint takes 2¹²⁸ work.

### The password seals the private key

The private key is stored only encrypted: AES-256-GCM under a key derived from the password with argon2id, using a salt of its own and the public key as associated data, so a sealed key copied onto another row does not open there. The encoded value carries its derivation parameters, as the password digest does.

This is the password-manager property the product owner asked for, and its cost is stated in the product rather than in a footnote: **nobody can reset a password.** Not an operator, not whoever has the database — nothing but the password opens the key, and the account's identity is the key. The registration form says so before the account exists.

Signing in still verifies an argon2id digest and does not open the key, so it costs what it cost before. Changing a password verifies the current one, opens the key with it, and stores a new digest and a newly sealed key together.

### A handle names a person to somebody else

`username#IDENTIFIER`, where the identifier is the account's without its prefix, followed by one check character computed with the Luhn mod 32 algorithm over the identifier alphabet. The username is what a person recognizes; the identifier is what cannot be forged; together, a mismatch between them is a refusal rather than a guess. The check character catches every single mistyped character and every swap of two neighbours except `A`↔`7`, before anything is sent.

### A username is plain ASCII

Lowercase letters, digits, dots, dashes and underscores, 3 to 32 characters, beginning with a letter or digit, unique on the installation and fixed once chosen. A wider alphabet would let two names look identical — Cyrillic `а` and Latin `a` — which is exactly how an invitation reaches the wrong person when somebody checked carefully.

### Registration is rationed by use, not by failure

Signing in is budgeted by its failures. Registering is limited by every attempt, successes included — twenty an hour per address — because what it guards against is somebody succeeding too often. A taken username answers `409`, which says the name exists; a registration form cannot avoid that, and a username is half of a handle meant to be shared. The sign-in form still refuses every failure identically, in words and in time.

## Consequences

- **Every account created under ADR 0007 is gone.** Migration `00019` replaces the `accounts` and `sessions` tables: an old account's identifier cannot become a key's fingerprint, and its key cannot be sealed by a password Convia never held. The user rows those accounts pointed at, and the rooms and messages that name them, are left alone.
- **A forgotten password loses the account.** Deliberate, and the one consequence a person will actually meet.
- **Whoever runs the machine still decides who signs in**, through suspension, and can still insert rows. What they cannot do is use somebody's key.
- **Nothing uses the key yet.** Invitations between installations are the next piece of work; the identifier had to be a key's fingerprint from the first account, because it cannot be changed afterwards.
- **Not adopted, and left for the product owner:** a short verification code two people compare out of band on first contact, which would stop somebody in the middle substituting an invitation.
