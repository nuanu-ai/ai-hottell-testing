import type { ButtonHTMLAttributes } from 'react';

import { classNames } from './classNames';

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'link';
  size?: 'sm';
};

export function Button({ variant, size, type = 'button', className, ...rest }: ButtonProps) {
  return (
    <button
      type={type}
      className={classNames('btn', variant && `btn-${variant}`, size && `btn-${size}`, className)}
      {...rest}
    />
  );
}
