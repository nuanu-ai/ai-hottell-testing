import { Panel } from '../../../shared/ui';

export function TypographySection() {
  return (
    <Panel title="Типографика" sub="IBM Plex Sans и IBM Plex Mono">
      <div className="hero" style={{ padding: 0, border: 0 }}>
        <h1>Заголовок Hero, 22/600</h1>
      </div>
      <h1 className="page-title" style={{ margin: 0 }}>
        Заголовок страницы, 20/600
      </h1>
      <div className="ph">
        <h2>Заголовок панели, 14/600</h2>
        <span className="sub">подпись панели</span>
      </div>
      <h3>Подпись раздела в панели, h3 как .lbl</h3>
      <p>Обычный текст 14px: агент сделал 1072 вызова, из них 669 — основной агент.</p>
      <p className="mono">Моноширинный 12.5px: rollout-2026-01-10T10-00-00.jsonl</p>
      <p className="muted">Приглушённый текст</p>
      <p>
        Код в строке: <code>task check</code>, тона: <span className="t-bad">ошибка</span>,{' '}
        <span className="t-ok">успех</span>, <span className="t-warn">внимание</span>
      </p>
    </Panel>
  );
}
