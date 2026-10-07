import { fullMoment } from '../lib/time';
import { classNames } from './classNames';

type MomentProps = {
  at: string;
  format: (moment: Date) => string;
  // Time in a table is mono 12.5px as in v5.1; a moment inside a sentence keeps the text font.
  mono?: boolean;
};

// A moment as <time>: the short form on screen, the full one in the title.
export function Moment({ at, format, mono = true }: MomentProps) {
  const moment = new Date(at);
  return (
    <time
      dateTime={at}
      title={fullMoment(moment)}
      className={classNames(mono && 'moment') || undefined}
    >
      {format(moment)}
    </time>
  );
}
