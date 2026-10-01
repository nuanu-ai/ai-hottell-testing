import { startAuthentication, startRegistration } from '@simplewebauthn/browser';

import {
  createPasskey,
  getPasskey,
  PasskeyAlreadyRegistered,
  PasskeyCancelled,
  PasskeyFailed,
} from './webauthn';

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}));

const domError = (name: string) => new DOMException('from the browser', name);

beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('getPasskey', () => {
  it('passes the options through and returns the answer', async () => {
    vi.mocked(startAuthentication).mockResolvedValue({ id: 'cred' } as never);

    await expect(getPasskey({ challenge: 'abc' })).resolves.toEqual({ id: 'cred' });
    expect(startAuthentication).toHaveBeenCalledWith({ optionsJSON: { challenge: 'abc' } });
  });

  it.each(['NotAllowedError', 'AbortError'])('turns %s into PasskeyCancelled', async (name) => {
    vi.mocked(startAuthentication).mockRejectedValue(domError(name));

    await expect(getPasskey({})).rejects.toBeInstanceOf(PasskeyCancelled);
    expect(console.error).not.toHaveBeenCalled();
  });

  it('turns InvalidStateError into PasskeyFailed: only a registration can repeat', async () => {
    vi.mocked(startAuthentication).mockRejectedValue(domError('InvalidStateError'));

    await expect(getPasskey({})).rejects.toBeInstanceOf(PasskeyFailed);
  });

  it('turns anything else into PasskeyFailed and logs the original', async () => {
    const original = domError('SecurityError');
    vi.mocked(startAuthentication).mockRejectedValue(original);

    await expect(getPasskey({})).rejects.toBeInstanceOf(PasskeyFailed);
    expect(console.error).toHaveBeenCalledWith(original);
  });
});

describe('createPasskey', () => {
  it('passes the options through and returns the answer', async () => {
    vi.mocked(startRegistration).mockResolvedValue({ id: 'new' } as never);

    await expect(createPasskey({ challenge: 'abc' })).resolves.toEqual({ id: 'new' });
    expect(startRegistration).toHaveBeenCalledWith({ optionsJSON: { challenge: 'abc' } });
  });

  it.each(['NotAllowedError', 'AbortError'])('turns %s into PasskeyCancelled', async (name) => {
    vi.mocked(startRegistration).mockRejectedValue(domError(name));

    await expect(createPasskey({})).rejects.toBeInstanceOf(PasskeyCancelled);
  });

  it('turns InvalidStateError into PasskeyAlreadyRegistered', async () => {
    vi.mocked(startRegistration).mockRejectedValue(domError('InvalidStateError'));

    await expect(createPasskey({})).rejects.toBeInstanceOf(PasskeyAlreadyRegistered);
  });

  it('turns anything else into PasskeyFailed and logs the original', async () => {
    const original = new TypeError('bad options');
    vi.mocked(startRegistration).mockRejectedValue(original);

    await expect(createPasskey({})).rejects.toBeInstanceOf(PasskeyFailed);
    expect(console.error).toHaveBeenCalledWith(original);
  });
});
