import type { components } from '../../../shared/api';
import { CostList, CostRow, FrictionRow, Panel } from '../../../shared/ui';

import { signalInSample, topicForSignal } from './signals';

type Session = components['schemas']['AnalyticsSession'];
type Friction = components['schemas']['AnalyticsFriction'];
type Finding = components['schemas']['AnalyticsFinding'];

const TOP = 6;

type FrictionAndCostProps = {
  sessions: readonly Session[];
  friction: readonly Friction[];
  findings: readonly Finding[];
  // Topics hidden as «Не проблема».
  hidden?: ReadonlySet<string>;
  // «тема →»: the topic opened in «Что исправить» (/fix?open=<id>, filters kept).
  onTopic: (id: string) => void;
  onSession: (session: Session) => void;
  onAllSignals: () => void;
  onAllSessions: () => void;
};

// «Обзор, 3»: «Трение» (span 7) and «Самые дорогие сессии» (span 5).
export function FrictionAndCost({
  sessions,
  friction,
  findings,
  hidden,
  onTopic,
  onSession,
  onAllSignals,
  onAllSessions,
}: FrictionAndCostProps) {
  const ids = new Set(sessions.map((s) => s.id));
  const signals = friction
    .map((f) => ({ f, ...signalInSample(f, ids) }))
    .filter((row) => row.sessions > 0);
  const top = sessions
    .filter((s) => s.cost_usd != null)
    .sort((a, b) => (b.cost_usd ?? 0) - (a.cost_usd ?? 0))
    .slice(0, TOP);

  return (
    <>
      <Panel
        span={7}
        className="ov-friction"
        title="Трение"
        action={
          <button type="button" className="link" onClick={onAllSignals}>
            все сигналы →
          </button>
        }
      >
        {signals.length ? (
          signals.map(({ f, sessions: n, eps }) => (
            <FrictionRow
              key={f.key}
              sev={f.sev}
              name={f.name}
              eps={eps}
              sessions={n}
              topic={topicForSignal(f.key, findings, ids, hidden)}
              onTopic={onTopic}
            />
          ))
        ) : (
          <div className="empty">Сигналов нет</div>
        )}
      </Panel>
      <Panel
        span={5}
        className="ov-top"
        title="Самые дорогие сессии"
        action={
          <button type="button" className="link" onClick={onAllSessions}>
            все →
          </button>
        }
      >
        {top.length ? (
          <CostList>
            {top.map((s) => (
              <CostRow
                key={`${s.user_id}:${s.agent}:${s.id}`}
                agent={s.agent}
                text={s.first || s.title || s.short}
                cost={s.cost_usd}
                basis={s.cost_basis}
                onOpen={() => {
                  onSession(s);
                }}
              />
            ))}
          </CostList>
        ) : (
          <div className="empty">Стоимость не записана</div>
        )}
      </Panel>
    </>
  );
}
