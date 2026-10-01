import { act, fireEvent, render, screen } from '@testing-library/react';

import { CopyField } from './CopyField';

function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
}

describe('CopyField', () => {
  afterEach(() => {
    vi.useRealTimers();
    window.getSelection()?.removeAllRanges();
  });

  it('copies the value and says so for 1.5 s', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(() => Promise.resolve());
    stubClipboard(writeText);
    render(<CopyField value="token-123" />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Скопировать' }));
      await Promise.resolve();
    });

    expect(writeText).toHaveBeenCalledExactlyOnceWith('token-123');
    expect(screen.getByRole('button')).toHaveTextContent('Скопировано');

    act(() => {
      vi.advanceTimersByTime(1499);
    });
    expect(screen.getByRole('button')).toHaveTextContent('Скопировано');

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(screen.getByRole('button')).toHaveTextContent('Скопировать');
  });

  it('restarts the 1.5 s on a second copy', async () => {
    vi.useFakeTimers();
    stubClipboard(() => Promise.resolve());
    render(<CopyField value="token-123" />);
    const copy = async () => {
      await act(async () => {
        fireEvent.click(screen.getByRole('button'));
        await Promise.resolve();
      });
    };

    await copy();
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    await copy();
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(screen.getByRole('button')).toHaveTextContent('Скопировано');
  });

  it('selects the text when the clipboard refuses', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')));
    render(<CopyField value="token-123" />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Скопировать' }));
      await Promise.resolve();
    });

    expect(window.getSelection()?.toString()).toBe('token-123');
    expect(screen.getByRole('button')).toHaveTextContent('Скопировать');
  });

  it('keeps the line breaks of a multiline value and copies them as they are', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    stubClipboard(writeText);
    const block = 'first line \\\n  second line';
    const { container } = render(<CopyField multiline value={block} />);

    expect(container.firstElementChild).toHaveClass('код', 'код-строки');
    expect(container.querySelector('code')?.textContent).toBe(block);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Скопировать' }));
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledExactlyOnceWith(block);
  });

  it('is one line unless asked otherwise', () => {
    const { container } = render(<CopyField value="token-123" />);

    expect(container.firstElementChild).toHaveClass('код');
    expect(container.firstElementChild).not.toHaveClass('код-строки');
  });
});
