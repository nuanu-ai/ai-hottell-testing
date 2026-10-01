import {
  browserSupportsWebAuthn,
  startAuthentication,
  startRegistration,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
} from '@simplewebauthn/browser';

// The API passes the options and the answer through as plain JSON objects.
type PasskeyJSON = Record<string, unknown>;

/** The person closed the browser prompt or it timed out. */
export class PasskeyCancelled extends Error {
  constructor() {
    super('passkey ceremony cancelled');
    this.name = 'PasskeyCancelled';
  }
}

/** The authenticator already holds a passkey of this user. */
export class PasskeyAlreadyRegistered extends Error {
  constructor() {
    super('passkey already registered');
    this.name = 'PasskeyAlreadyRegistered';
  }
}

/** Any other failure of the browser; its own text goes to the console only. */
export class PasskeyFailed extends Error {
  constructor() {
    super('passkey ceremony failed');
    this.name = 'PasskeyFailed';
  }
}

export function isPasskeySupported(): boolean {
  return browserSupportsWebAuthn();
}

// @simplewebauthn/browser keeps the DOMException's name on the errors it wraps.
// A DOMException is not an Error in every runtime, so only its name is read.
function errorName(error: unknown): string {
  if (typeof error === 'object' && error !== null && 'name' in error) {
    return typeof error.name === 'string' ? error.name : '';
  }
  return '';
}

function toPasskeyError(error: unknown, registering: boolean): Error {
  const name = errorName(error);
  if (name === 'NotAllowedError' || name === 'AbortError') {
    return new PasskeyCancelled();
  }
  if (registering && name === 'InvalidStateError') {
    return new PasskeyAlreadyRegistered();
  }
  console.error(error);
  return new PasskeyFailed();
}

/** Asks the browser for a passkey to sign in with; the answer goes to …/login/finish. */
export async function getPasskey(options: PasskeyJSON): Promise<PasskeyJSON> {
  try {
    const credential = await startAuthentication({
      optionsJSON: options as unknown as PublicKeyCredentialRequestOptionsJSON,
    });
    return { ...credential };
  } catch (error) {
    throw toPasskeyError(error, false);
  }
}

/** Asks the browser to create a passkey; the answer goes to …/register/finish. */
export async function createPasskey(options: PasskeyJSON): Promise<PasskeyJSON> {
  try {
    const credential = await startRegistration({
      optionsJSON: options as unknown as PublicKeyCredentialCreationOptionsJSON,
    });
    return { ...credential };
  } catch (error) {
    throw toPasskeyError(error, true);
  }
}
