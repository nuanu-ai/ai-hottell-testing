import { useState } from 'react';

import { fmtMoney, fmtN, pct } from '../../../shared/lib/format';
import {
  Button,
  CountUp,
  Cycle,
  type CycleStep,
  Hero,
  Kpi,
  Kpis,
  Panel,
  Stat,
  Stats,
} from '../../../shared/ui';

const ROUNDS = [
  { cost: 0.06, sessions: 3, hit: 0.417 },
  { cost: 1.84, sessions: 27, hit: 0.632 },
];

// CountUp counts from the old value to the new one; the button switches between two rounds.
function CountUpDemo() {
  const [round, setRound] = useState(0);
  const values = ROUNDS[round % ROUNDS.length] ?? { cost: null, sessions: null, hit: null };
  return (
    <Panel
      title="Досчёт чисел"
      sub="CountUp · 600 мс"
      action={
        <Button
          size="sm"
          onClick={() => {
            setRound((r) => r + 1);
          }}
        >
          Новые значения
        </Button>
      }
    >
      <Kpis>
        <Kpi label="Расходы" value={<CountUp value={values.cost} format={fmtMoney} />} />
        <Kpi label="Сессии" value={<CountUp value={values.sessions} format={fmtN} />} />
        <Kpi label="Попадание в кэш" value={<CountUp value={values.hit} format={pct} />} />
        <Kpi label="Нет данных" value={<CountUp value={null} format={fmtN} />} />
      </Kpis>
    </Panel>
  );
}

const CYCLE: CycleStep[] = [
  {
    key: 'seen',
    label: 'Замечено',
    value: 12,
    sub: 'сигналов трения',
    title: 'Сигналы трения в выборке',
  },
  { key: 'topics', label: 'Темы', value: 4, sub: 'стоит обсудить', title: 'Темы для обсуждения' },
  {
    key: 'evidence',
    label: 'С доказательствами',
    value: 3,
    sub: 'есть событие',
    title: 'Темы с событием в ленте',
  },
  {
    key: 'discussed',
    label: 'Обсуждено',
    value: null,
    sub: 'журнал пуст',
    title: 'Записи журнала коуча с любым решением',
  },
  {
    key: 'applied',
    label: 'Внедрено',
    value: null,
    sub: '—',
    title: 'Внесённые изменения: decision applied',
  },
  {
    key: 'checked',
    label: 'Проверено',
    value: null,
    sub: '—',
    title: 'Внедрённые изменения с итогом проверки',
  },
];

// Cycle with the empty coach journal of E3: steps 4–6 wait for E4, the hint sits under them.
// «Тема выросла» is a silent update: step 2 grows and the dot runs to it from step 1.
function CycleDemo() {
  const [selected, setSelected] = useState<string | null>(null);
  const [extra, setExtra] = useState(0);
  const steps = CYCLE.map((step) =>
    step.key === 'topics' && step.value !== null ? { ...step, value: step.value + extra } : step,
  );
  return (
    <Panel
      title="Цикл улучшений"
      sub="Cycle · точка роста 800 мс"
      action={
        <Button
          size="sm"
          onClick={() => {
            setExtra((n) => n + 1);
          }}
        >
          Тема выросла
        </Button>
      }
    >
      <Cycle
        animateGrowth
        steps={steps}
        selected={selected}
        onSelect={(key) => {
          setSelected((current) => (current === key ? null : key));
        }}
        hint="Обсудите первую тему в Codex или Claude Code"
      />
    </Panel>
  );
}

export function MetricsSection() {
  return (
    <>
      <Kpis>
        <Kpi
          label="Расходы"
          value="≈ $0.06"
          trend={{ change: 12, goodWhen: 'down', text: '12% к прошлым 7 дн' }}
        />
        <Kpi
          label="Сессии"
          value="3"
          trend={{ change: 25, goodWhen: 'up', text: '25% к прошлым 7 дн' }}
        />
        <Kpi
          label="Агент работал"
          value="21 м"
          trend={{ change: -30, goodWhen: 'up', text: '30% к прошлым 7 дн' }}
        />
        <Kpi
          label="Ваше время"
          value="21 м"
          trend={{ change: -15, goodWhen: 'down', text: '15% к прошлым 7 дн' }}
        />
        <Kpi
          label="Попадание в кэш"
          value="41,7%"
          trend={{ change: 1, goodWhen: 'up', text: '1% к прошлым 7 дн' }}
        />
        <Kpi label="Ошибки инструментов" value="50,0%" note="за всё время" />
      </Kpis>
      <Stats>
        <Stat label="Стоимость · по данным OTel" value="$0.06" />
        <Stat label="От начала до конца" value="10 м" />
        <Stat label="Работа агента" value="10 м" />
        <Stat label="Ваших реплик" value="2" />
        <Stat label="Запросов к модели" value="3" />
        <Stat label="Вызовов" value="5" />
        <Stat label="Ошибок" value="1" />
        <Stat label="Ваше время" value="8 м" />
      </Stats>
      <Hero
        title="Разбор этих сессий не дал предложений."
        items={[
          { value: 0, label: 'к исправлению' },
          { value: 0, label: 'с подтверждённой проблемой' },
          { value: 0, label: 'готово к применению' },
          { value: 0, label: 'сессий с доказательствами' },
        ]}
      />
      <CountUpDemo />
      <CycleDemo />
    </>
  );
}
