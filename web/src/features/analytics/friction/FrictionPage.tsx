import type { components } from '../../../shared/api';
import { estMoney, fmtN, pct } from '../../../shared/lib/format';
import {
  DataLimits,
  Meter,
  Panel,
  SevDot,
  Table,
  TableRow,
  Td,
  Th,
  View,
} from '../../../shared/ui';
import { withFilters, type AnalyticsFilters } from '../model/filters';
import { pageGaps } from '../model/gaps';
import type { Selection } from '../tools/count';
import { frictionIn, topicForSignal } from './frictionIn';
import './friction.css';

type Dataset = components['schemas']['AnalyticsDataset'];

const NONE: ReadonlySet<string> = new Set();

// Without filters a link carries only its own key.
function link(path: string, filters: AnalyticsFilters | undefined, extra: Record<string, string>) {
  return filters
    ? withFilters(path, filters, extra)
    : `${path}?${new URLSearchParams(extra).toString()}`;
}

function goTo(href: string) {
  window.location.assign(href);
}

type FrictionPageProps = {
  dataset: Pick<Dataset, 'friction' | 'findings' | 'gaps'>;
  selection: Selection;
  // Topics hidden as «Не проблема».
  hidden?: ReadonlySet<string>;
  // The page's filters, carried by every link so the next screen keeps the same sample.
  filters?: AnalyticsFilters;
  onNavigate?: (href: string) => void;
};

// «Трение» of v5.1 (renderFriction in the reference): every friction signal over the chosen sessions.
// A row leads to the sessions with the signal, «тема →» to its topic in «Что исправить».
// «Ограничения данных» close the page.
export function FrictionPage({
  dataset,
  selection,
  hidden = NONE,
  filters,
  onNavigate = goTo,
}: FrictionPageProps) {
  const chosen = selection.ids.size;
  const limits = pageGaps(dataset.gaps);

  return (
    <View>
      <Panel
        title="Сигналы трения"
        sub="считаются по событиям, без модели · клик — сессии с сигналом"
        style={{ gap: 10 }}
      >
        <Table>
          <thead>
            <tr>
              <Th style={{ width: 16 }} aria-label="Важность" />
              <Th>Сигнал</Th>
              <Th>Как считается</Th>
              <Th numeric>Эпизодов</Th>
              <Th numeric>Сессий</Th>
              <Th numeric>Стоимость эпизодов</Th>
              <Th style={{ width: 140 }}>Доля сессий</Th>
              <Th aria-label="Тема" />
            </tr>
          </thead>
          <tbody>
            {dataset.friction.map((signal) => {
              const count = frictionIn(signal, selection);
              const seen = count.sessions.length;
              const share = chosen ? seen / chosen : 0;
              const topic = topicForSignal(signal.key, dataset.findings, selection, hidden);
              const muted = seen ? undefined : 'friction-muted';
              const cost =
                count.full && seen && signal.cost_usd != null
                  ? estMoney(signal.cost_usd, null)
                  : '—';
              return (
                <TableRow
                  key={signal.key}
                  href={link('/sessions', filters, { flag: signal.key })}
                  onNavigate={onNavigate}
                >
                  <Td className={muted}>
                    <SevDot tone={signal.sev} />
                  </Td>
                  <Td className={muted ?? 'friction-name'}>{signal.name}</Td>
                  <Td wrap className={muted ?? 'muted friction-how'}>
                    {signal.how}
                  </Td>
                  <Td numeric className={muted}>
                    {signal.count == null ? '—' : fmtN(count.episodes)}
                  </Td>
                  <Td numeric className={muted}>
                    {seen}
                  </Td>
                  <Td numeric className={muted}>
                    {cost}
                  </Td>
                  <Td className={muted}>
                    <Meter
                      value={share}
                      tone={signal.sev === 'info' ? undefined : signal.sev}
                      label={`${signal.name}: доля сессий`}
                      valueText={pct(share)}
                    />
                  </Td>
                  <Td>
                    {topic && (
                      <button
                        type="button"
                        className="link friction-topic"
                        onClick={() => {
                          onNavigate(link('/fix', filters, { open: topic.id }));
                        }}
                      >
                        тема →
                      </button>
                    )}
                  </Td>
                </TableRow>
              );
            })}
            {!dataset.friction.length && (
              <tr>
                <td colSpan={8} className="empty">
                  Нет сигналов
                </td>
              </tr>
            )}
          </tbody>
        </Table>
      </Panel>
      <DataLimits items={limits.items} count={limits.count} />
    </View>
  );
}
