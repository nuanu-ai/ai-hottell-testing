import { useNavigate } from '@tanstack/react-router';

import type { components } from '../../../shared/api';
import { Axes, EvidenceRows, Label, Snippet } from '../../../shared/ui';
import { validateAnalyticsSearch } from '../model/filters';
import {
  decisionLabels,
  effectLabels,
  executionLabels,
  labelOf,
  readinessLabels,
} from '../model/labels';

type Finding = components['schemas']['AnalyticsFinding'];

// The open topic (README v5.1 «Раскрытие»; the reference topicCard, L640–L651). The axes come
// from the finding's fields; the coach journal (E4) will state them instead.

/** Left: the full what, «Где видно · N» over the chosen sessions, «Проверить до правки». */
export function TopicEvidence({ f, ids }: { f: Finding; ids: ReadonlySet<string> }) {
  const navigate = useNavigate();
  const evidence = f.ev.filter((e) => ids.has(e.sid));
  return (
    <>
      <p className="t-what">{f.what}</p>
      <Label>Где видно · {evidence.length}</Label>
      <EvidenceRows
        items={evidence.map((e) => ({
          sid: e.sid,
          line: e.line,
          at: e.at ?? undefined,
          text: e.text,
        }))}
        onOpen={(sid, line) => {
          void navigate({
            to: '/sessions/$id',
            params: { id: sid },
            // The filters go along; the timeline opens at the event.
            search: (prev: Record<string, unknown>) => ({
              ...validateAnalyticsSearch(prev),
              ...(line !== undefined ? { line } : {}),
            }),
          });
        }}
      />
      {f.preconditions.length > 0 && (
        <>
          <Label>Проверить до правки</Label>
          <ul className="t-pre">
            {f.preconditions.map((item, index) => (
              <li key={index}>{item}</li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

/** Right: the four axes, «Кандидат правки», «Как проверить пользу». */
export function TopicState({ f }: { f: Finding }) {
  return (
    <>
      <Axes
        items={[
          ['Готовность', labelOf(readinessLabels, f.readiness)],
          ['Решение', labelOf(decisionLabels, f.decision)],
          ['Применение', labelOf(executionLabels, f.execution)],
          ['Эффект', labelOf(effectLabels, f.effect)],
        ]}
      />
      {(f.where || f.snip) && (
        <>
          <Label>Кандидат правки</Label>
          <Snippet where={f.where} text={f.snip} />
        </>
      )}
      {f.verification && (
        <>
          <Label>Как проверить пользу</Label>
          <p className="t-check">{f.verification}</p>
        </>
      )}
    </>
  );
}
