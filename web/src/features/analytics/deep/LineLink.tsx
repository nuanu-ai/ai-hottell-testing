// A transcript line of the report («L1219»): a link to the session feed at the event built from
// that line, or plain text without onOpen. L<n> is a line of the transcript, not a feed event, so
// the screens open the feed by ?src=, never ?line= (HT-410).
export function LineLink({
  line,
  label,
  onOpen,
}: {
  line: number;
  label?: string;
  onOpen?: (line: number) => void;
}) {
  const text = label ?? `L${String(line)}`;
  if (!onOpen) return <span className="mono">{text}</span>;
  return (
    <button
      type="button"
      className="link mono"
      onClick={() => {
        onOpen(line);
      }}
    >
      {text}
    </button>
  );
}

// The first lines of a list; the rest are left to the feed.
export function LineLinks({
  lines,
  limit = 6,
  onOpen,
}: {
  lines: readonly number[];
  limit?: number;
  onOpen?: (line: number) => void;
}) {
  if (lines.length === 0) return null;
  return (
    <span className="lines">
      {lines.slice(0, limit).map((line) => (
        <LineLink key={line} line={line} onOpen={onOpen} />
      ))}
    </span>
  );
}
