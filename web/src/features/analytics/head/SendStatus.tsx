import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import type { components } from '../../../shared/api';
import { fmtN } from '../../../shared/lib/format';
import { formatAgoShort } from '../../../shared/lib/time';
import { sendStatusQuery } from '../api/delivery';

type Delivery = components['schemas']['AnalyticsDelivery'];

// The freshest moment the server heard from the binary: any received source, else the collector token.
function lastSent(delivery: Delivery): string | undefined {
  const moments = delivery.last_received.map((r) => r.at);
  if (delivery.ingest_key_last_used_at) {
    moments.push(delivery.ingest_key_last_used_at);
  }
  return moments.sort().at(-1);
}

// The send line of the head (README v5.1 «Шапка, строка 2, п. 2»), always about the signed-in person:
// working, an error, stale (hottell has not reported its status for a while), or not set up. Any
// other state, or a failed read, shows nothing.
export function SendStatus() {
  const { data } = useQuery(sendStatusQuery);
  if (!data) {
    return null;
  }
  // Every state of the schema has a line; one this build does not know yet shows nothing.
  const state: string = data.state;

  if (state === 'ok') {
    const last = lastSent(data);
    const queued = data.queue?.records;
    return (
      <span className="send">
        <b className="ok">Отправка: работает</b>
        {last && ` · последняя ${formatAgoShort(last)}`}
        {queued != null && ` · в очереди ${fmtN(queued)}`}
        {data.settings_version != null && ` · настройки v${String(data.settings_version)}`}
        {' · '}
        <Link to="/telemetry">Что отправлять →</Link>
      </span>
    );
  }
  if (state === 'problem') {
    const problem = data.problems?.[0];
    return (
      <span className="send">
        <b className="warn">Отправка: ошибка</b>
        {problem && ` · ${problem}`}
        {' · '}
        <Link to="/connect">Подключение →</Link>
      </span>
    );
  }
  if (state === 'stale') {
    return (
      <span className="send">
        <b className="warn">Отправка: {data.reason ?? 'hottell давно не сообщал статус'}</b>
        {' · '}
        <Link to="/connect">Подключение →</Link>
      </span>
    );
  }
  if (state === 'not_configured') {
    return (
      <span className="send muted" title={data.reason}>
        Отправка не настроена · <Link to="/connect">Подключение →</Link>
      </span>
    );
  }
  return null;
}
