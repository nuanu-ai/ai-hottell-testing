import {
  CostList,
  CostRow,
  Coverage,
  EventFeed,
  FrictionRow,
  Grid,
  Label,
  Legend,
  Meter,
  MeterList,
  More,
  Panel,
  SessionId,
  SevDot,
  Table,
  TableRow,
  Tag,
  Td,
  Th,
} from '../../../shared/ui';
import type { FeedEvent, FeedTurn } from '../../../shared/ui';

const noop = () => undefined;

// A synthetic session feed: every kind of event once, over two turns.
const FEED_AT = (minute: number) => new Date(Date.UTC(2026, 8, 30, 5, 50 + minute)).toISOString();
const FEED: FeedEvent[] = [
  { line: 1, at: FEED_AT(0), turn: 0, k: 'prompt', x: 'Почини сборку фронта' },
  { line: 2, at: FEED_AT(0), turn: 0, k: 'api', x: 'запрос к модели', side: '$0.02' },
  { line: 3, at: FEED_AT(1), turn: 0, k: 'skill', x: 'team-skills:frontend' },
  { line: 4, at: FEED_AT(1), turn: 0, k: 'tool', x: 'pnpm test', side: '1,4 с' },
  {
    line: 5,
    at: FEED_AT(2),
    turn: 0,
    k: 'err',
    x: 'pnpm build',
    note: 'exit 1 · Type error in App.tsx',
    side: '3,2 с',
  },
  { line: 6, at: FEED_AT(3), turn: 0, k: 'agent', x: 'субагент: проверка типов' },
  { line: 7, at: FEED_AT(4), turn: 0, k: 'compact', x: 'контекст сжат' },
  {
    line: 8,
    at: FEED_AT(5),
    turn: 0,
    k: 'answer',
    x: 'Сборка исправлена, тесты зелёные.',
    note: '2 файла',
  },
  { line: 9, at: FEED_AT(6), turn: 1, k: 'prompt', x: 'Теперь обнови README' },
  { line: 10, at: FEED_AT(7), turn: 1, k: 'wait', x: 'агент ждёт ответа', side: 'вы' },
  { line: 11, at: FEED_AT(8), turn: 1, k: 'abort', x: 'ход прерван', note: 'прерван вами' },
];
const FEED_TURNS: FeedTurn[] = [
  { turn: 0, start: FEED_AT(0), dur_ms: 360_000, state: 'task_complete' },
  { turn: 1, start: FEED_AT(6), dur_ms: 120_000, state: 'turn_aborted' },
];

export function DataSection() {
  return (
    <Grid>
      <Panel span={7} title="Самые дорогие сессии" sub="Table · TableRow · Tag">
        <Table>
          <thead>
            <tr>
              <Th>Сессия</Th>
              <Th>Проект</Th>
              <Th numeric>$ · оценка</Th>
            </tr>
          </thead>
          <tbody>
            <TableRow href="#a" onNavigate={noop}>
              <Td>
                <Tag tone="claude">CC</Tag> <span className="mono">11111111</span>{' '}
                <span className="muted">05.03</span>
              </Td>
              <Td cellTitle>claude-demo</Td>
              <Td numeric>$0.06</Td>
            </TableRow>
            <TableRow href="#b" onNavigate={noop}>
              <Td>
                <Tag tone="codex">CX</Tag> <span className="mono">019cbd6f</span>{' '}
                <span className="muted">05.03</span>
              </Td>
              <Td cellTitle>live-demo</Td>
              <Td numeric>≈ $0.00</Td>
            </TableRow>
            <TableRow>
              <Td wrap>Длинное название, которое переносится, потому что ячейке разрешено</Td>
              <Td>old-work</Td>
              <Td numeric>—</Td>
            </TableRow>
          </tbody>
        </Table>
      </Panel>
      <Panel span={5} title="Куда уходят деньги" sub="MeterList · Meter · Legend">
        <MeterList
          items={[
            { label: 'claude-demo', value: 1, text: '$0.06' },
            { label: 'live-demo', value: 0.05, text: '≈ $0.00' },
            { label: 'old-work', value: 0, text: '—' },
          ]}
        />
        <div className="acts">
          <Meter value={0.6} />
          <Meter value={0.4} tone="warn" />
          <Meter value={0.8} tone="bad" />
        </div>
        <Legend
          items={[
            { label: 'm1', color: 'var(--m1)' },
            { label: 'm2', color: 'var(--m2)' },
            { label: 'm3', color: 'var(--m3)' },
            { label: 'm4', color: 'var(--m4)' },
          ]}
        />
      </Panel>
      <Panel span={7} title="Трение" sub="FrictionRow · эпизоды неизвестны — «—»">
        <div>
          <FrictionRow
            sev="bad"
            name="Ошибка инструмента"
            eps={1079}
            sessions={8}
            topic="t-1"
            onTopic={noop}
          />
          <FrictionRow
            sev="warn"
            name="Повтор той же команды"
            eps={79}
            sessions={3}
            topic="t-2"
            onTopic={noop}
          />
          <FrictionRow sev="info" name="Агент ждал вас" eps={null} sessions={12} />
        </div>
      </Panel>
      <Panel span={5} title="Самые дорогие сессии" sub="CostList · CostRow">
        <CostList>
          <CostRow
            agent="claude"
            text="Почини сборку фронта после обновления зависимостей и прогони тесты"
            cost={12.5}
            basis="otel_reported"
            onOpen={noop}
          />
          <CostRow
            agent="codex"
            text="Перепиши тесты хранилища"
            cost={130}
            basis="api_price_estimate"
            onOpen={noop}
          />
          <CostRow agent="codex" text="019cbd6f…a1b2" cost={null} basis={null} onOpen={noop} />
        </CostList>
      </Panel>
      <Panel
        span={12}
        title="Лента сессии"
        sub="EventFeed · EventRow · TurnHeader · подсвечена строка 5"
      >
        <EventFeed events={FEED} turns={FEED_TURNS} highlightLine={5} />
      </Panel>
      <Panel span={12} title="Мелкие элементы" sub="SevDot · Label · Coverage · More · SessionId">
        <div className="acts">
          ID сессии: <SessionId id="01a0f57b-0d3c-4e8f-9a1b-6c2d3e8f4b74" />
        </div>
        <div className="acts">
          <SevDot tone="bad" /> Сбой MCP <SevDot tone="warn" /> Холодный кэш <SevDot tone="info" />{' '}
          Агент ждал вас
        </div>
        <Label>Где видно</Label>
        <Coverage>
          Полнота данных: хуки с начала сессии — у 2 из 3 · исход вызова известен — 45,5% ·
          стоимость записана — у 2 из 3 сессий
        </Coverage>
        <More summary="Здоровье сбора данных · 2">
          <dl>
            <dt>Хуки</dt>
            <dd>записаны с начала сессии</dd>
            <dt>Нативный OTel</dt>
            <dd>не привязан к сессиям Codex</dd>
          </dl>
        </More>
      </Panel>
    </Grid>
  );
}
