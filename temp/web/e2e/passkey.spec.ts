import { expect, test, type Browser, type Page } from '@playwright/test';

import {
  acceptInvite,
  admin,
  invite,
  loginAs,
  newPerson,
  personCard,
  withVirtualAuthenticator,
} from './support';

// A guest of the admin's, signed in with a password, in a browser holding a virtual authenticator.
async function guestWithAuthenticator(page: Page, browser: Browser, label: string) {
  const guest = newPerson(label);
  await loginAs(page, admin.email, admin.password);
  const guestPage = await acceptInvite(browser, await invite(page, guest), guest);
  await withVirtualAuthenticator(guestPage);
  await personCard(guestPage, guest.name).click();
  await expect(guestPage).toHaveURL('/login');
  await loginAs(guestPage, guest.email, guest.password);
  return { guest, guestPage };
}

async function addPasskey(page: Page, name: string) {
  await page.getByLabel('Название').fill(name);
  await page.getByRole('button', { name: 'Добавить passkey' }).click();
}

const passkeyRow = (page: Page, name: string) => page.getByRole('row').filter({ hasText: name });

test('a passkey added in the profile signs in without an email', async ({ page, browser }) => {
  const { guest, guestPage } = await guestWithAuthenticator(page, browser, 'passkey');

  await guestPage.goto('/profile');
  await addPasskey(guestPage, 'E2E');
  await expect(guestPage.getByText('Passkey «E2E» добавлен')).toBeVisible();
  await expect(passkeyRow(guestPage, 'E2E')).toContainText('ещё не использовался');

  await personCard(guestPage, guest.name).click();
  await expect(guestPage).toHaveURL('/login');
  await expect(guestPage.getByLabel('Email')).toHaveValue('');
  await guestPage.getByRole('button', { name: 'Войти по passkey' }).click();
  await expect(guestPage).toHaveURL('/users');
  await expect(personCard(guestPage, guest.name)).toBeVisible();

  await guestPage.goto('/profile');
  await expect(passkeyRow(guestPage, 'E2E')).toContainText('только что');
});

test('the same authenticator cannot add a second passkey', async ({ page, browser }) => {
  const { guestPage } = await guestWithAuthenticator(page, browser, 'passkey-twice');

  await guestPage.goto('/profile');
  await addPasskey(guestPage, 'E2E');
  await expect(guestPage.getByText('Passkey «E2E» добавлен')).toBeVisible();
  await addPasskey(guestPage, 'E2E again');
  await expect(guestPage.getByText('Этот passkey уже добавлен')).toBeVisible();
  await expect(guestPage.getByRole('row').filter({ hasText: 'E2E again' })).toHaveCount(0);
});

test('a deleted passkey no longer signs in', async ({ page, browser }) => {
  const { guest, guestPage } = await guestWithAuthenticator(page, browser, 'passkey-deleted');

  await guestPage.goto('/profile');
  await addPasskey(guestPage, 'E2E');
  await passkeyRow(guestPage, 'E2E').getByRole('button', { name: 'Удалить' }).click();
  const dialog = guestPage.getByRole('dialog', { name: 'Удалить passkey' });
  await dialog.getByRole('button', { name: 'Удалить' }).click();
  await expect(dialog).toBeHidden();
  await expect(guestPage.getByText('Passkey ещё нет')).toBeVisible();

  await personCard(guestPage, guest.name).click();
  await expect(guestPage).toHaveURL('/login');
  await guestPage.getByRole('button', { name: 'Войти по passkey' }).click();
  await expect(guestPage.getByRole('alert')).toHaveText(
    'Не удалось подтвердить passkey. Попробуйте ещё раз',
  );
  await expect(guestPage).toHaveURL('/login');
});
