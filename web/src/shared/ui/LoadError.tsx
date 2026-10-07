import { Button } from './Button';
import { Notice } from './Notice';

type LoadErrorProps = {
  error: { message: string };
  retrying: boolean;
  onRetry: () => void;
};

// A failed load: the message on the left and «Повторить» on the right, wrapping on a narrow screen.
export function LoadError({ error, retrying, onRetry }: LoadErrorProps) {
  return (
    <Notice tone="err">
      <div className="load-err">
        <span>{error.message}</span>
        <Button size="sm" disabled={retrying} onClick={onRetry}>
          Повторить
        </Button>
      </div>
    </Notice>
  );
}
