import { expect, test } from '@playwright/test'

// The page speaks the language the browser prefers, and says so to the browser.
test('a browser that prefers Portuguese is spoken to in Portuguese', async ({ browser }) => {
  const context = await browser.newContext({ locale: 'pt-BR' })
  const page = await context.newPage()
  await page.goto('/')

  await expect(page.getByRole('button', { name: 'Entrar' })).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('lang', 'pt-BR')
  await context.close()
})
