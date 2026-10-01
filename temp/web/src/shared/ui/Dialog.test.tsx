import { createRef } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';

import { Dialog } from './Dialog';

// jsdom has no showModal/close; these stand in for the browser's own behaviour.
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    this.open = false;
  };
});

describe('Dialog', () => {
  it('opens and closes through its ref', () => {
    const ref = createRef<HTMLDialogElement>();
    render(<Dialog ref={ref}>Точно остановить?</Dialog>);

    ref.current?.showModal();
    expect(ref.current).toHaveAttribute('open');

    ref.current?.close();
    expect(ref.current).not.toHaveAttribute('open');
  });

  it('closes on Escape', () => {
    const ref = createRef<HTMLDialogElement>();
    render(<Dialog ref={ref}>Точно остановить?</Dialog>);
    ref.current?.showModal();

    fireEvent.keyDown(screen.getByText('Точно остановить?'), { key: 'Escape' });

    expect(ref.current).not.toHaveAttribute('open');
  });
});
