import { expect, test } from '@playwright/test';

import { admin, loginAs, personCard, submitLogin } from './support';

test('password sign-in opens the users page with the name in the head', async ({ page }) => {
  await loginAs(page, admin.email, admin.password);
  await expect(page.locator('.крупно')).toHaveText('Пользователи');
  await expect(personCard(page, admin.name)).toContainText(admin.name);
});

test('a wrong password is refused with a message', async ({ page }) => {
  await page.goto('/login');
  await submitLogin(page, admin.email, `${admin.password}-wrong`);
  await expect(page.getByRole('alert')).toHaveText('Неверный email или пароль');
  await expect(page).toHaveURL('/login');
});

test('a closed page sends to sign-in and back after it', async ({ page }) => {
  await page.goto('/profile');
  await expect(page).toHaveURL('/login?next=%2Fprofile');
  await submitLogin(page, admin.email, admin.password);
  await expect(page).toHaveURL('/profile');
});

test('the person card signs out', async ({ page }) => {
  await loginAs(page, admin.email, admin.password);
  await personCard(page, admin.name).click();
  await expect(page).toHaveURL('/login');
  await page.goto('/users');
  await expect(page).toHaveURL('/login?next=%2Fusers');
});
