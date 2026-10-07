import type { ButtonHTMLAttributes } from 'react';

import { classNames } from './classNames';

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'link';
  size?: 'sm' | 'lg';
};

// A link-styled button is the v3 .link: no frame, no padding.
export function Button({ variant, size, type = 'button', className, ...rest }: ButtonProps) {
  const base = variant === 'link' ? 'link' : 'btn';
  return (
    <button
      type={type}
      className={classNames(
        base,
        variant === 'primary' && 'pri',
        base === 'btn' && size && size,
        className,
      )}
      {...rest}
    />
  );
}
