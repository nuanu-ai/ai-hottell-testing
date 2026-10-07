import { OutcomeBar, OutcomeLegend, Panel, Table, Td, Th } from '../../../shared/ui';

const TOOLS = [
  { name: 'Read', calls: 120, errors: 0, unknown: 4 },
  { name: 'Bash', calls: 100, errors: 1, unknown: 0 },
  { name: 'Edit', calls: 40, errors: 6, unknown: 10 },
  { name: 'mcp__demo__search', calls: 12, errors: 3, unknown: null },
];

export function OutcomeSection() {
  return (
    <Panel title="Исход вызовов" sub="OutcomeBar · OutcomeLegend">
      <Table>
        <thead>
          <tr>
            <Th>Инструмент</Th>
            <Th numeric>Вызовов</Th>
            <Th>Исход</Th>
            <Th numeric>Ошибок</Th>
            <Th numeric>Нет результата</Th>
          </tr>
        </thead>
        <tbody>
          {TOOLS.map((t) => (
            <tr key={t.name}>
              <Td>
                <span className="mono">{t.name}</span>
              </Td>
              <Td numeric>{t.calls}</Td>
              <Td>
                <OutcomeBar calls={t.calls} errors={t.errors} unknown={t.unknown} />
              </Td>
              <Td numeric>{t.errors}</Td>
              <Td numeric>{t.unknown ?? '—'}</Td>
            </tr>
          ))}
        </tbody>
      </Table>
      <OutcomeLegend notSplit />
    </Panel>
  );
}
