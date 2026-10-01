import { useId } from 'react';
import type { InputHTMLAttributes, ReactNode } from 'react';

import { classNames } from './classNames';

type FieldProps = {
  label: string;
  htmlFor: string;
  error?: string;
  errorId?: string;
  children: ReactNode;
};

export function Field({ label, htmlFor, error, errorId, children }: FieldProps) {
  return (
    <div className="field">
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {error && (
        <div className="hint hint-err" id={errorId}>
          {error}
        </div>
      )}
    </div>
  );
}

type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & {
  label: string;
  error?: string;
};

function LabelledInput({
  label,
  error,
  id,
  type,
  'aria-describedby': describedBy,
  ...rest
}: InputProps & { type: string }) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const errorId = `${inputId}-error`;

  return (
    <Field label={label} htmlFor={inputId} error={error} errorId={errorId}>
      <input
        id={inputId}
        type={type}
        {...rest}
        aria-invalid={error ? true : undefined}
        aria-describedby={classNames(describedBy, error && errorId) || undefined}
      />
    </Field>
  );
}

export function TextInput(props: InputProps) {
  return <LabelledInput type="text" {...props} />;
}

export function PasswordInput(props: InputProps) {
  return <LabelledInput type="password" {...props} />;
}

export function EmailInput(props: InputProps) {
  return <LabelledInput type="email" {...props} />;
}
