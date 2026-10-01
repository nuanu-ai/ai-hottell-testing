import { expect, test } from '@playwright/test';

import { acceptInvite, admin, invite, loginAs, newPerson, readCopyField, userRow } from './support';

test('an invited user sets a password and signs in; the link works once', async ({
  page,
  browser,
}) => {
  const guest = newPerson('invite');
  await loginAs(page, admin.email, admin.password);
  const link = await invite(page, guest);

  const guestPage = await acceptInvite(browser, link, guest);
  await expect(guestPage.locator('.крупно')).toHaveText('Пользователи');

  const again = await (await browser.newContext()).newPage();
  await again.goto(link);
  await expect(again.getByText('Ссылка уже использована')).toBeVisible();
});

test('a reissued invite makes the old link unknown', async ({ page, browser }) => {
  const guest = newPerson('reissue');
  await loginAs(page, admin.email, admin.password);
  const oldLink = await invite(page, guest);

  await userRow(page, guest.email).getByRole('button', { name: 'Новая ссылка' }).click();
  const dialog = page.getByRole('dialog', { name: 'Новая ссылка' });
  await dialog.getByRole('button', { name: 'Выпустить новую' }).click();
  const newLink = await readCopyField(dialog);
  expect(newLink).not.toBe(oldLink);

  const other = await (await browser.newContext()).newPage();
  await other.goto(oldLink);
  await expect(other.getByText('Ссылка не найдена')).toBeVisible();
  await other.goto(newLink);
  await expect(other.getByText(`Приглашение для ${guest.name}`)).toBeVisible();
});
