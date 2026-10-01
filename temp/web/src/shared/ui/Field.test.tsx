import { render, screen } from '@testing-library/react';

import { EmailInput, PasswordInput, TextInput } from './Field';

describe('Field', () => {
  it('ties the label to a text input', () => {
    render(<TextInput label="Кто вы" />);

    expect(screen.getByLabelText('Кто вы')).toHaveAttribute('type', 'text');
  });

  it('ties the label to a password input', () => {
    render(<PasswordInput label="Ключ" />);

    expect(screen.getByLabelText('Ключ')).toHaveAttribute('type', 'password');
  });

  it('ties the label to an email input', () => {
    render(<EmailInput label="Почта" />);

    expect(screen.getByLabelText('Почта')).toHaveAttribute('type', 'email');
  });

  it('gives each field its own id', () => {
    render(
      <>
        <TextInput label="Первое" />
        <TextInput label="Второе" />
      </>,
    );

    expect(screen.getByLabelText('Первое').id).not.toBe(screen.getByLabelText('Второе').id);
  });

  it('describes the input with its error', () => {
    render(<PasswordInput label="Ключ" error="Неверный ключ" />);

    const input = screen.getByLabelText('Ключ');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Неверный ключ');
  });

  it('keeps a caller description next to the error', () => {
    render(
      <>
        <TextInput label="Имя" aria-describedby="name-hint" error="Нужно имя" />
        <p id="name-hint">Как в паспорте</p>
      </>,
    );

    expect(screen.getByLabelText('Имя')).toHaveAccessibleDescription('Как в паспорте Нужно имя');
  });
});
