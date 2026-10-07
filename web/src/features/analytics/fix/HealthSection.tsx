import type { components } from '../../../shared/api';
import { SevDot } from '../../../shared/ui';
import { impactFor } from '../model/sample';

type Finding = components['schemas']['AnalyticsFinding'];

// «Здоровье сбора данных» (README v5.1 «6»): the cards about collection, folded. They are about
// the collector, which records service sessions too, so «Без служебных» does not change them:
// ids are the sessions of the filter of every kind.
export function HealthSection({
  cards,
  ids,
}: {
  cards: readonly Finding[];
  ids: ReadonlySet<string>;
}) {
  if (cards.length === 0) {
    return null;
  }
  return (
    <details className="box health">
      <summary>
        Здоровье сбора данных · {cards.length} — что hottell пока записывает не полностью; к вашей
        работе не относится
      </summary>
      <div className="hcards">
        {cards.map((f) => {
          const impact = impactFor(f, ids);
          return (
            <div className="hc" key={f.id}>
              <span className="hh">
                <SevDot tone={f.sev} />
                <span>{f.title}</span>
              </span>
              <span className="num hc-impact">
                <b>{impact?.value ?? '—'}</b> <span className="muted">{impact?.label ?? ''}</span>
              </span>
              {f.what && <span className="full hc-what">{f.what}</span>}
              {f.where && <code className="full hc-where">{f.where}</code>}
              {f.snip && <pre className="snip full">{f.snip}</pre>}
            </div>
          );
        })}
      </div>
    </details>
  );
}
