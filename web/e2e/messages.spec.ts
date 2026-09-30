import { expect, test } from '@playwright/test'

import { register, shareRoom, signedIn } from './support'

/*
The critical journeys of a conversation, end to end.

The component tests cover what the conversation looks like in each state, with
the socket stubbed. What they cannot show is the part that makes a conversation
one thing rather than two: that what somebody writes reaches the other person's
open screen, without a reload and without either of them asking. Every piece of
that -- the announcement after the commit, the relay, the socket, the reducer --
is exercised somewhere on its own and nowhere together.

A defect here does not fail a request. It leaves two people each believing they
can see what the other said.
*/

test("what one person writes reaches the other's open screen, and withdrawing it takes it back", async ({
  browser,
}) => {
  const ana = await register('ana')
  const bea = await register('bea')
  const room = await shareRoom(ana, bea)

  const a = await signedIn(browser, ana)
  const b = await signedIn(browser, bea)

  const compose = (page: (typeof a)['page']) => page.getByRole('textbox', { name: `Message ${room.name}` })

  await compose(a.page).fill('The lift is out again.')
  await a.page.getByRole('button', { name: 'Send' }).click()

  // Nobody reloads: this is the socket or nothing.
  await expect(b.page.getByText('The lift is out again.')).toBeVisible()
  await expect(b.page.getByText(ana.username)).toBeVisible()

  // Bea answers, and it goes the other way too.
  await compose(b.page).fill('Stairs it is.')
  await b.page.getByRole('button', { name: 'Send' }).click()
  await expect(a.page.getByText('Stairs it is.')).toBeVisible()

  /*
  Withdrawing has to reach the other screen as much as writing does. A message
  that stays readable to everybody except the person who took it back is worse
  than one that was never withdrawn, because they believe it is gone.
  */
  const written = a.page.getByText('The lift is out again.')
  await written.hover()
  await a.page.getByRole('button', { name: 'Withdraw' }).click()

  await expect(b.page.getByText('This message was withdrawn.')).toBeVisible()
  await expect(b.page.getByText('The lift is out again.')).toHaveCount(0)
})

test('an edit reaches the other screen carrying the mark that it is one', async ({ browser }) => {
  const ana = await register('ana')
  const bea = await register('bea')
  const room = await shareRoom(ana, bea)

  const a = await signedIn(browser, ana)
  const b = await signedIn(browser, bea)

  await a.page.getByRole('textbox', { name: `Message ${room.name}` }).fill('Half past two.')
  await a.page.getByRole('button', { name: 'Send' }).click()
  await expect(b.page.getByText('Half past two.')).toBeVisible()

  await a.page.getByText('Half past two.').hover()
  await a.page.getByRole('button', { name: 'Edit' }).click()

  // By its own label rather than by position: the composer is a textbox too,
  // and picking the first one would edit or send depending on the layout.
  await a.page.getByRole('textbox', { name: 'Edit message' }).fill('Half past three.')
  await a.page.keyboard.press('Enter')

  /*
  The mark matters as much as the new words. Without it the other person reads
  a message they never saw arrive as one they had already read, and an edit
  becomes a way to change what somebody remembers agreeing to.
  */
  await expect(b.page.getByText('Half past three.')).toBeVisible()
  await expect(b.page.getByText('(edited)')).toBeVisible()
})
