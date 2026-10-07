import { useQuery } from '@tanstack/react-query';

import { discussActions } from '../../../shared/lib/discuss';
import { shortId } from '../../../shared/lib/format';
import {
  CopyField,
  DiscussActions,
  Empty,
  Label,
  Loading,
  Notice,
  Panel,
  Tag,
} from '../../../shared/ui';
import { skillOpportunitiesQuery } from './api';
import { LineLinks } from './LineLink';
import { type Opportunity, kindLabel, opportunitiesOf, readinessLabel } from './skills';

// The report is made by the user's agent: «Обновить анализ» copies $session-retro skills, it
// starts nothing on the server.
function RefreshActions() {
  return (
    <DiscussActions
      size="sm"
      actions={discussActions({ mode: 'skills' }, [
        'Обновить анализ в Codex',
        'Обновить анализ в Claude Code',
      ])}
    />
  );
}

function OpportunityCard({
  item,
  onOpenLine,
}: {
  item: Opportunity;
  onOpenLine?: (sid: string, line: number) => void;
}) {
  const [kind, tone] = kindLabel(item.kind);
  return (
    <article className="opp" data-kind={item.kind}>
      <div className="opp-h">
        <Tag tone={tone}>{kind}</Tag>
        <b>{item.title}</b>
        <span className="muted">{readinessLabel(item.readiness)}</span>
      </div>
      <p>{item.recommendation}</p>
      {item.installedSkill && (
        <p>
          Skill: <code>{item.installedSkill}</code>
        </p>
      )}
      {item.external && (
        <p>
          <a href={item.external.url} target="_blank" rel="noopener noreferrer">
            {item.external.name || item.external.url}
          </a>{' '}
          {item.external.reviewRequired && <Tag tone="warn">нужна проверка источника</Tag>}
        </p>
      )}
      <dl className="opp-d">
        <dt>Почему skill</dt>
        <dd>{item.whySkill || '—'}</dd>
        <dt>Альтернатива</dt>
        <dd>{item.alternative || '—'}</dd>
        <dt>Неопределённость</dt>
        <dd>{item.uncertainty || '—'}</dd>
        <dt>Как проверить</dt>
        <dd>{item.verification || '—'}</dd>
      </dl>
      {item.sources.length > 0 && (
        <>
          <Label>Где видно</Label>
          <ul className="opp-src">
            {item.sources.map((s, index) => (
              <li key={index}>
                <span className="mono">
                  {shortId(s.sessionId)} · {s.taskId}
                </span>{' '}
                <LineLinks
                  lines={s.lines}
                  onOpen={
                    onOpenLine &&
                    ((line) => {
                      onOpenLine(s.sessionId, line);
                    })
                  }
                />
              </li>
            ))}
          </ul>
        </>
      )}
      {item.kind === 'create' && item.copyPrompt && (
        <>
          <Label>Промпт для skill-creator — ничего не устанавливает</Label>
          <CopyField value={item.copyPrompt} title={item.copyPrompt} />
        </>
      )}
    </article>
  );
}

// Skill opportunities on the «Skills» screen: use an installed skill, create one, or review an
// external one before installing.
export function SkillOpportunities({
  user = null,
  onOpenLine,
}: {
  user?: string | null;
  onOpenLine?: (sid: string, line: number) => void;
}) {
  const answer = useQuery(skillOpportunitiesQuery(user));
  return (
    <Panel
      title="Возможности skills"
      sub="по опубликованным глубоким разборам"
      action={<RefreshActions />}
    >
      {answer.isPending ? (
        <Loading />
      ) : answer.isError ? (
        <Notice tone="err">Не удалось загрузить анализ skills: {answer.error.message}</Notice>
      ) : !answer.data.report ? (
        <Empty title="Анализа skills ещё нет">
          <p>Его делает ваш агент со skill session-retro по опубликованным разборам.</p>
        </Empty>
      ) : (
        <>
          {answer.data.stale && (
            <Notice tone="warn">Набор разборов изменился после анализа — обновите</Notice>
          )}
          {opportunitiesOf(answer.data.report).map((item) => (
            <OpportunityCard key={item.id} item={item} onOpenLine={onOpenLine} />
          ))}
          {opportunitiesOf(answer.data.report).length === 0 && (
            <Empty title="Возможностей не найдено" />
          )}
        </>
      )}
    </Panel>
  );
}
