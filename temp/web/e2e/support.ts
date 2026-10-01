import { expect, type Browser, type Locator, type Page } from '@playwright/test';

function required(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is not set: run the suite through task e2e, which loads e2e/.env.e2e`);
  }
  return value;
}

export type Person = { name: string; email: string; password: string };

// The first user, created by `cli user create-first` from e2e/.env.e2e.
export const admin: Person = {
  name: required('HT_BOOTSTRAP_NAME'),
  email: required('HT_BOOTSTRAP_EMAIL'),
  password: required('HT_BOOTSTRAP_PASSWORD'),
};

// Every scenario shares the stand's database, so each invites people of its own.
export function newPerson(label: string): Person {
  const id = `${label}-${String(Date.now())}-${String(Math.floor(Math.random() * 1e6))}`;
  return { name: `Гость ${id}`, email: `${id}@e2e.example.com`, password: `pass-${id}` };
}

export async function submitLogin(page: Page, email: string, password: string) {
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Пароль').fill(password);
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
}

export async function loginAs(page: Page, email: string, password: string) {
  await page.goto('/login');
  await submitLogin(page, email, password);
  await expect(page).toHaveURL('/users');
}

// The signed-in person's card in the page head; a click on it signs out.
export const personCard = (page: Page, name: string) => page.getByTitle(`${name} — выйти`);

// The one-time link a CopyField inside scope shows.
export async function readCopyField(scope: Locator): Promise<string> {
  const link = await scope.locator('.код code').textContent();
  expect(link).toBeTruthy();
  return link ?? '';
}

export async function setNewPassword(page: Page, password: string, submit: string) {
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByLabel('Повторите пароль').fill(password);
  await page.getByRole('button', { name: submit }).click();
}

export const userRow = (page: Page, email: string) =>
  page.getByRole('row').filter({ hasText: email });

// Invites person from the users page the admin has open; returns the invite link.
export async function invite(page: Page, person: Person): Promise<string> {
  await page.getByRole('button', { name: 'Пригласить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Пригласить пользователя' });
  await dialog.getByLabel('Имя').fill(person.name);
  await dialog.getByLabel('Email').fill(person.email);
  await dialog.getByRole('button', { name: 'Создать ссылку' }).click();
  const link = await readCopyField(dialog);
  await dialog.getByRole('button', { name: 'Готово' }).click();
  return link;
}

// Accepts the invite link in a browser context of its own; returns the page signed in as person.
export async function acceptInvite(browser: Browser, link: string, person: Person): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  await page.goto(link);
  await setNewPassword(page, person.password, 'Задать пароль и войти');
  await expect(page).toHaveURL('/users');
  await expect(personCard(page, person.name)).toBeVisible();
  return page;
}

// Attaches Chromium's virtual platform authenticator to page: it holds discoverable passkeys and
// confirms every prompt with user verification, the way a fingerprint would.
export async function withVirtualAuthenticator(page: Page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
    },
  });
  return { cdp, authenticatorId };
}
