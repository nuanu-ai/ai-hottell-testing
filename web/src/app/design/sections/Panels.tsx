import { useState } from 'react';

import {
  Axes,
  Button,
  DataLimits,
  EvidenceRows,
  Grid,
  Label,
  PageHead,
  Panel,
  SectionHead,
  Snippet,
  TopicCard,
  type EvidenceItem,
} from '../../../shared/ui';

const noop = () => undefined;

// Synthetic evidence: 8 rows, so the card shows the last 6 and «ещё 2».
const EVIDENCE: EvidenceItem[] = Array.from({ length: 8 }, (_, index) => ({
  sid: index % 2 ? '01a0f058-1c2d-4e5f-8a9b-0c1d2e3f5b9f' : '00000000-0000-4000-8000-00000000000a',
  line: 5 + index * 12,
  at: new Date(Date.now() - (8 - index) * 3_600_000).toISOString(),
  text: `MCP demo: нет ответа за 60 с, вызов ${String(index + 1)} повторён агентом`,
}));

function Filler() {
  return <p className="muted">Содержимое панели</p>;
}

function Topics() {
  // Each press remounts the first card with flash, so the 1.2 s bar can be seen again.
  const [flashes, setFlashes] = useState(0);
  // The screen holds the open topic; here the first card starts open to show its parts.
  const [open, setOpen] = useState(true);
  return (
    <div className="topics">
      <TopicCard
        key={flashes}
        sev="bad"
        kind="Инструменты"
        title="Сбой MCP-сервера повторяется в длинных сессиях и съедает ходы агента"
        price={{ lead: '79 эпизодов', rest: ' · 8 сессий · ≈ $7.86' }}
        why="Больше всего в 01a0f57b…4b74 · claude-demo: 41 из 79 эпизодов"
        actions={
          <Button
            size="sm"
            onClick={() => {
              setFlashes((n) => n + 1);
            }}
          >
            Показать вспышку
          </Button>
        }
        flash={flashes > 0}
        open={open}
        onToggle={() => {
          setOpen((value) => !value);
        }}
        left={
          <>
            <p>Сервер не отвечает после долгого простоя, агент повторяет вызов.</p>
            <div className="blk">
              <Label>Где видно · {EVIDENCE.length}</Label>
              <EvidenceRows items={EVIDENCE} limit={6} onOpen={noop} />
            </div>
          </>
        }
        right={
          <>
            <Axes
              items={[
                ['Готовность', 'гипотеза'],
                ['Решение', '—'],
                ['Применение', '—'],
                ['Эффект', '—'],
              ]}
            />
            <div className="blk">
              <Label>Кандидат правки</Label>
              <Snippet
                where="~/.claude/settings.json → mcpServers.demo.timeout"
                text={'"timeout": 60000'}
              />
            </div>
            <div className="blk">
              <Label>Без кандидата</Label>
              <Snippet />
            </div>
          </>
        }
      />
      <TopicCard
        sev="warn"
        title="Холодный кэш в начале сессии"
        price={{ lead: '3 сессии', rest: ' · в выбранных' }}
        why="Первый ход каждой сессии читает контекст заново."
        actions={
          <Button size="sm" variant="link">
            Не проблема
          </Button>
        }
      />
    </div>
  );
}

export function PanelsSection() {
  return (
    <Grid>
      <Panel span={8} title="Расходы по дням" sub="оценка">
        <Filler />
      </Panel>
      <Panel span={4} title="Куда уходят деньги" sub="по проектам · оценка">
        <Filler />
      </Panel>
      <Panel span={7} title="Трение" action={<Button variant="link">все сигналы →</Button>}>
        <Filler />
      </Panel>
      <Panel span={5} title="Самые дорогие сессии" action={<Button variant="link">все →</Button>}>
        <Filler />
      </Panel>
      <Panel span={6} title="Кто работал: агент и вы" sub="часы в день">
        <Filler />
      </Panel>
      <Panel span={6} title="Результат в git" sub="по сессиям с коммитами">
        <Filler />
      </Panel>
      <Panel span={12} title="Откуда данные" sub="что записано, что восстановлено, чего нет">
        <Filler />
      </Panel>
      <Panel span={12} title="Темы" sub="TopicCard · раскрытие · вспышка">
        <Topics />
      </Panel>
      <Panel span={12} title="Шапка страницы" sub="PageHead · с sub и action, только заголовок">
        <PageHead
          title="Пользователи"
          sub="3 участника · 1 приглашение"
          action={<Button variant="primary">Пригласить</Button>}
        />
        <PageHead title="Подключение" />
      </Panel>
      <Panel span={12} title="Заголовок секции и ограничения данных" sub="SectionHead · DataLimits">
        <SectionHead
          title="Темы"
          sub="сначала важные, затем по числу эпизодов"
          action={<Button variant="link">скрыто 2 — вернуть</Button>}
        />
        <DataLimits
          items={[
            'Стоимость Codex — оценка по условной цене API',
            'Ответы MCP-серверов не записываются',
            'Ещё 3 замечания по отдельным сессиям — в ленте каждой сессии.',
          ]}
          count={3}
        />
      </Panel>
    </Grid>
  );
}
