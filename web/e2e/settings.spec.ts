import { expect, test } from '@playwright/test'

import { register, signedIn } from './support'

/*
Changing a password through the page: a wrong current password keeps the person
signed in, and a right one keeps this browser signed in and nowhere else.
*/
test('changing the password signs out everywhere else, and a wrong one signs out nobody', async ({ browser }) => {
  const ana = await register('ana')
  const here = await signedIn(browser, ana)
  const elsewhere = await signedIn(browser, ana)

  await here.page.getByRole('navigation', { name: 'Convia' }).getByRole('button', { name: 'Settings' }).click()

  const change = async (current: string, next: string) => {
    await here.page.getByLabel('Current password', { exact: true }).fill(current)
    await here.page.getByLabel('New password', { exact: true }).fill(next)
    await here.page.getByLabel('Confirm new password', { exact: true }).fill(next)
    await here.page.getByRole('button', { name: 'Change password' }).click()
  }

  await change('not the password at all', 'a replacement password')
  await expect(here.page.getByText('That is not your current password.')).toBeVisible()

  await change('correct horse battery staple', 'a replacement password')
  await expect(here.page.getByText(/Your password was changed/)).toBeVisible()

  await here.page.reload()
  await expect(here.page.getByRole('navigation', { name: 'Convia' })).toBeVisible()

  await elsewhere.page.reload()
  await expect(elsewhere.page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})
