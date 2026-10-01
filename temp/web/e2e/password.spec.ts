import { expect, test } from '@playwright/test';

import {
  acceptInvite,
  admin,
  invite,
  loginAs,
  newPerson,
  personCard,
  readCopyField,
  setNewPassword,
  submitLogin,
  userRow,
} from './support';

test('a reset link sets a new password and closes the open sessions', async ({ page, browser }) => {
  const guest = newPerson('reset');
  await loginAs(page, admin.email, admin.password);
  const guestPage = await acceptInvite(browser, await invite(page, guest), guest);

  await page.reload();
  await userRow(page, guest.email).getByRole('button', { name: 'Ссылка сброса пароля' }).click();
  const dialog = page.getByRole('dialog', { name: 'Ссылка сброса пароля' });
  await dialog.getByRole('button', { name: 'Выдать ссылку' }).click();
  const resetLink = await readCopyField(dialog);

  const newPassword = `${guest.password}-reset`;
  const resetPage = await (await browser.newContext()).newPage();
  await resetPage.goto(resetLink);
  await setNewPassword(resetPage, newPassword, 'Сохранить пароль и войти');
  await expect(resetPage).toHaveURL('/users');

  // The session opened before the reset is closed.
  await guestPage.reload();
  await expect(guestPage).toHaveURL('/login?next=%2Fusers');

  await submitLogin(guestPage, guest.email, guest.password);
  await expect(guestPage.getByRole('alert')).toHaveText('Неверный email или пароль');
  await submitLogin(guestPage, guest.email, newPassword);
  await expect(guestPage).toHaveURL('/users');
  await expect(personCard(guestPage, guest.name)).toBeVisible();
});

test('a user changes their own password in the profile', async ({ page, browser }) => {
  const guest = newPerson('profile');
  await loginAs(page, admin.email, admin.password);
  const guestPage = await acceptInvite(browser, await invite(page, guest), guest);

  const newPassword = `${guest.password}-changed`;
  await guestPage.goto('/profile');
  await guestPage.getByLabel('Текущий пароль').fill(guest.password);
  await guestPage.getByLabel('Новый пароль', { exact: true }).fill(newPassword);
  await guestPage.getByLabel('Повторите новый пароль').fill(newPassword);
  await guestPage.getByRole('button', { name: 'Сменить пароль' }).click();
  await expect(guestPage.getByText('Пароль изменён. Остальные входы закрыты')).toBeVisible();

  await personCard(guestPage, guest.name).click();
  await expect(guestPage).toHaveURL('/login');
  await submitLogin(guestPage, guest.email, guest.password);
  await expect(guestPage.getByRole('alert')).toHaveText('Неверный email или пароль');
  await submitLogin(guestPage, guest.email, newPassword);
  await expect(guestPage).toHaveURL('/users');
});
