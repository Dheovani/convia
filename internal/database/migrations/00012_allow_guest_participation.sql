-- +goose Up

-- A guest is somebody taking part in a call who has no Convia user.
--
-- Until now every participation named one, and that was right: an application
-- admitting its own people already knows who they are, so Convia recording a
-- second copy would have been a second thing to keep correct. A guest is the
-- case that assumption does not cover — somebody the application cannot name
-- as one of its users, because they are not one.
--
-- The only way a guest gets in is by presenting something Convia issued, which
-- is an invitation. That is why this waited for invitations rather than
-- arriving with participants: admitting a guest on the application's word
-- alone would have been indistinguishable from the application resolving them
-- as a user first, and would have modelled nothing.
--
-- **Convia learns nothing about a guest.** No name, no address, no identity of
-- any kind: a guest participation is identified by the invitation it was
-- redeemed with, and the application knows who it sent that invitation to. A
-- roster already refuses to carry a display name for known users, and a guest
-- is not the place to start.

-- An invitation may now name nobody, which is what makes it a guest invitation.
ALTER TABLE invitations ALTER COLUMN user_id DROP NOT NULL;

-- A participation is identified by a person or by the invitation that produced
-- it, and exactly one of the two.
ALTER TABLE participants ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE participants ADD COLUMN invitation_id TEXT REFERENCES invitations (id);

ALTER TABLE participants ADD CONSTRAINT participants_identified CHECK (
    (user_id IS NOT NULL AND invitation_id IS NULL)
    OR (user_id IS NULL AND invitation_id IS NOT NULL)
);

-- One invitation is one presence, which is what makes redeeming again return
-- the participation a guest already had rather than seating them twice.
--
-- The existing index on (call_id, user_id) does not cover guests and does not
-- need to: PostgreSQL treats NULLs as distinct in a unique index, so guest
-- rows never collide with each other there. This is the guest equivalent, and
-- like that one it ignores departed rows, so a guest who left and was invited
-- again keeps both stints in the call's history.
CREATE UNIQUE INDEX participants_call_invitation_present_key
    ON participants (call_id, invitation_id)
    WHERE status = 'joined' AND invitation_id IS NOT NULL;

-- +goose Down

-- Restoring NOT NULL fails if any guest has ever taken part, which is correct:
-- there is no way to un-model guests while guest participations exist, and
-- inventing users for them or deleting their history would both be worse than
-- refusing.
DROP INDEX participants_call_invitation_present_key;

ALTER TABLE participants DROP CONSTRAINT participants_identified;
ALTER TABLE participants DROP COLUMN invitation_id;
ALTER TABLE participants ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE invitations ALTER COLUMN user_id SET NOT NULL;
