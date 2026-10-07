import { shortId } from '../lib/format';
import { formatWhen } from '../lib/time';

// Parts of an open topic (reference topicCard, index.html:L640–L651): the «Где видно» rows,
// the four axes and the change candidate. The captions above them are the screen's.

export type EvidenceItem = {
  sid: string;
  // The event's line in the session feed; the live dataset numbers them as «#N».
  line?: number;
  // A line of the session's transcript (a Deep L<n>), shown as «LN»; the feed finds its event.
  src?: number;
  at?: string;
  text?: string;
};

type EvidenceRowsProps = {
  items: readonly EvidenceItem[];
  // How many of the latest rows to show; the rest are counted, not drawn.
  limit?: number;
  // Opens the session feed at the event, by its feed line or its transcript line.
  onOpen: (sid: string, line: number | undefined, src?: number) => void;
};

export function EvidenceRows({ items, limit = 6, onOpen }: EvidenceRowsProps) {
  if (items.length === 0) {
    return <div className="muted evr-none">В выбранных сессиях доказательств нет.</div>;
  }
  const shown = items.slice(-limit);
  const more = items.length - shown.length;
  return (
    <div>
      {shown.map((item, index) => {
        const when = formatWhen(item.at);
        return (
          <div className="evr" key={index}>
            <button
              type="button"
              className="link mono"
              onClick={() => {
                onOpen(item.sid, item.line, item.src);
              }}
            >
              {shortId(item.sid)}
              {item.line != null && ` · #${String(item.line)}`}
              {item.src != null && ` · L${String(item.src)}`}
            </button>
            {item.at ? (
              <span className="tm" title={when.title}>
                {when.text}
              </span>
            ) : (
              <span />
            )}
            <span className="x" title={item.text}>
              {item.text}
            </span>
          </div>
        );
      })}
      {more > 0 && <span className="muted evr-more">ещё {more} — в ленте сессий</span>}
    </div>
  );
}

// Label and value pairs in two columns: «Готовность», «Решение», «Применение», «Эффект».
export function Axes({ items }: { items: readonly (readonly [string, string])[] }) {
  return (
    <div className="axes">
      {items.flatMap(([label, value]) => [
        <span key={`${label}-l`}>{label}</span>,
        <span key={`${label}-v`}>{value}</span>,
      ])}
    </div>
  );
}

// «Кандидат правки»: where the change goes, and the text to put there.
export function Snippet({ where, text }: { where?: string; text?: string }) {
  return (
    <>
      {where && <code className="w">{where}</code>}
      <pre className="snip">{text || '—'}</pre>
    </>
  );
}
