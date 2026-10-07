import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';

import { ApiError } from '../../../shared/api';
import { fmtN, plural } from '../../../shared/lib/format';
import { PulseBars } from '../../../shared/ui';
import { pulseQuery } from '../api/queries';
import { usePersonState } from '../api/useDataset';
import { filtersToSearch, sessionKeys, useAnalyticsFilters } from '../model/filters';
import { noProject } from '../model/sample';

// The head pulse (README v5.1 «Пульс»): hook events of the last 10 minutes for the chosen person,
// and on a click the sessions working now. No pulse endpoint (404) — no pulse at all.
export function Pulse() {
  const { user, known } = usePersonState();
  const { data, error } = useQuery({ ...pulseQuery(user), enabled: known });
  const { filters } = useAnalyticsFilters();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    const onPointer = (event: MouseEvent) => {
      if (!box.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onPointer);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onPointer);
    };
  }, [open]);

  if (!data || (error instanceof ApiError && error.status === 404)) {
    return null;
  }

  return (
    <div className="pulse" ref={box}>
      <PulseBars
        bars={data.bars}
        now={Date.parse(data.at)}
        active={data.active.length}
        error={data.error}
        expanded={open}
        onClick={() => {
          setOpen((was) => !was);
        }}
      />
      {open && (
        <div className="pdrop">
          <div className="h">Идут сейчас · {data.active.length}</div>
          {data.active.length === 0 && <div className="none">Сейчас никто не работает.</div>}
          {data.active.map((session) => (
            <div className="a" key={`${session.agent}:${session.sid}`}>
              <div className="r1">
                <span className="p">{session.project || noProject}</span>
                <span className="n">
                  {fmtN(session.per_min)} {plural(session.per_min, 'событие', 'события', 'событий')}{' '}
                  за минуту
                </span>
              </div>
              <span className="f" title={session.first}>
                {session.first ?? 'первая реплика ещё не известна'}
              </span>
              <Link
                className="link"
                to="/sessions/$id"
                params={{ id: session.sid }}
                search={{ ...filtersToSearch(filters), ...sessionKeys(session) }}
                onClick={() => {
                  setOpen(false);
                }}
              >
                лента →
              </Link>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
