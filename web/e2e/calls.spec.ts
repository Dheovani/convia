import { expect, test } from '@playwright/test'

import { api, callsOf, callStage, inCall, join, openRoom, register, shareRoom, signedIn } from './support'

/*
The critical journeys of a call, end to end.

Departures the media server has to notice take as long as it takes to notice
them, so those are waited for rather than expected at once.
*/
const noticed = { timeout: 60_000 }

test('two people start and join a call, and hear each other', async ({ browser }) => {
  const ana = await register('ana')
  const bea = await register('bea')
  const room = await shareRoom(ana, bea)

  const a = await signedIn(browser, ana)
  await join(a.page, 'Start call')

  const b = await signedIn(browser, bea)
  await join(b.page, 'Join call')

  const stage = callStage(a.page)
  await expect(stage.getByRole('list', { name: 'People in the call' }).getByRole('listitem')).toHaveCount(2)
  await expect(stage.getByText(bea.username)).toBeVisible()
  await expect(a.page.locator('audio')).toHaveCount(1)
  await expect.poll(() => inCall(ana, room.id)).toBe(2)
})

test("the room's owner takes somebody out of the call, and they cannot come back to it", async ({ browser }) => {
  const ana = await register('ana')
  const bea = await register('bea')
  const room = await shareRoom(ana, bea)

  const a = await signedIn(browser, ana)
  await join(a.page, 'Start call')
  const b = await signedIn(browser, bea)
  await join(b.page, 'Join call')

  await callStage(a.page).getByRole('button', { name: `Take ${bea.username} out of the call` }).click()

  await expect(b.page.getByText('You were taken out of the call.')).toBeVisible()
  await expect.poll(() => inCall(ana, room.id)).toBe(1)

  await b.page.getByRole('button', { name: 'Join call' }).click()
  const ready = b.page.getByRole('region', { name: 'Prepare to join' })
  await ready.getByRole('button', { name: 'Join call' }).click()
  await expect(b.page.getByText('You cannot join this call. You may have been taken out of it.')).toBeVisible()
})

test('a page that goes away is noticed, and the last one to go ends the call', async ({ browser }) => {
  const ana = await register('ana')
  const bea = await register('bea')
  const room = await shareRoom(ana, bea)

  const a = await signedIn(browser, ana)
  await join(a.page, 'Start call')
  const b = await signedIn(browser, bea)
  await join(b.page, 'Join call')
  await expect.poll(() => inCall(ana, room.id)).toBe(2)

  // Nobody asks Convia to leave: only the media server can say these people went.
  await b.context.close()
  await expect.poll(() => inCall(ana, room.id), noticed).toBe(1)
  expect(await callsOf(ana)).toBe(1)

  await a.context.close()
  await expect.poll(() => callsOf(bea), noticed).toBe(0)
})

test('leaving ends a call nobody else is in, and deleting a room ends its call', async ({ browser }) => {
  const ana = await register('ana')
  const room = await openRoom(ana)

  const a = await signedIn(browser, ana)
  await join(a.page, 'Start call')
  await callStage(a.page).getByRole('button', { name: 'Leave call' }).click()
  await expect.poll(() => callsOf(ana)).toBe(0)
  await expect(a.page.getByRole('button', { name: 'Start call' })).toBeFocused()

  await join(a.page, 'Start call')
  expect((await api(ana, 'DELETE', `/v1/me/rooms/${room.id}`)).status).toBe(204)

  await expect(a.page.getByText('The call ended.')).toBeVisible()
  await expect.poll(() => callsOf(ana)).toBe(0)
})

test('on a phone, the call goes on while the list is read, and is one tap away', async ({ browser }) => {
  const ana = await register('ana')
  const room = await openRoom(ana)

  const a = await signedIn(browser, ana, { narrow: true })
  await expect(a.page.getByRole('main')).toHaveCount(0)

  await a.page.getByRole('button', { name: room.name }).click()
  await join(a.page, 'Start call')

  await a.page.getByRole('button', { name: 'Back to conversations' }).click()
  const bar = a.page.getByRole('region', { name: 'Current call' })
  await expect(bar).toBeVisible()
  await expect(bar.getByText(room.name)).toBeVisible()

  await bar.getByRole('button', { name: 'Return' }).click()
  await expect(callStage(a.page)).toBeVisible()
  expect(await callsOf(ana)).toBe(1)
})
