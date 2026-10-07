import type { InputHTMLAttributes, ReactNode } from 'react';

type CheckboxProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & { label: ReactNode };

export function Checkbox({ label, className, ...rest }: CheckboxProps) {
  return (
    <label className={className ? `check ${className}` : 'check'}>
      <input type="checkbox" {...rest} />
      <span>{label}</span>
    </label>
  );
}
