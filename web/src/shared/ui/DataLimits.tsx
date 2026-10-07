// «Ограничения данных · N»: a folded block at the end of every analytics page (README v5.1 «7»;
// the reference gapsBlock, index.html:L575–L580). The details stay uncontrolled, so a refresh of the
// data keeps it open or closed as the person left it.
export function DataLimits({ items, count }: { items: readonly string[]; count: number }) {
  return (
    <details className="box limits">
      <summary>Ограничения данных · {count}</summary>
      <ul>
        {items.map((item, index) => (
          <li key={index}>{item}</li>
        ))}
      </ul>
    </details>
  );
}
