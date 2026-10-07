import { useState } from 'react';

import { shortId } from '../../../shared/lib/format';
import { utcTitle } from '../../../shared/lib/time';
import { type JournalRow, localDayMonth } from './journal';

// «Исправлено»: date · layer · target · change · result tag; a click opens «Основания · Изменение ·
// Проверка · Откат». A p2:… proposal leads to its registry card (TopicCard anchor topic-<id>).
export function JournalList({
  rows,
  emptyJournal,
  onOpenSession,
}: {
  rows: readonly JournalRow[];
  // The journal has no record at all, not just none in the pressed step.
  emptyJournal: boolean;
  onOpenSession: (sid: string, seq: number | null, owner: string) => void;
}) {
  const [open, setOpen] = useState<string | null>(null);
  if (rows.length === 0) {
    return (
      <div className="jlist">
        <div className="jempty">
          <div>
            <b>{emptyJournal ? 'Пока ни одна тема не обсуждалась' : 'В этой группе записей нет'}</b>
            <span className="muted">
              Решение по теме записывается сюда: что изменено, где, как откатить и когда проверить.
            </span>
          </div>
          <span className="mono muted">эффект не заявляем без проверки</span>
        </div>
      </div>
    );
  }
  return (
    <div className="jlist">
      {rows.map((r) => {
        const isOpen = open === r.id;
        const [tag, tone] = r.tag;
        return (
          <div className="jrow" key={r.id} data-kind={r.kind}>
            <button
              type="button"
              className="jbtn"
              aria-expanded={isOpen}
              onClick={() => {
                setOpen(isOpen ? null : r.id);
              }}
            >
              <span className="mono muted" title={utcTitle(new Date(r.at))}>
                {localDayMonth(r.at)}
              </span>
              <span className="jl">{r.layer}</span>
              <span className="jt el mono">{r.target || '—'}</span>
              <span className="el">{r.change || r.topic || '—'}</span>
              <span className={tone === 'plain' ? 'tag' : `tag ${tone}`}>{tag}</span>
            </button>
            {isOpen && (
              <div className="jdet">
                <span>Основания</span>
                <span>
                  {r.topic || '—'}
                  {r.proposals.map((id) => (
                    <a key={id} className="link mono jp" href={`#topic-${id}`}>
                      {id}
                    </a>
                  ))}
                  {r.evidence.map((e, index) => (
                    <span className="ev" key={index}>
                      {e.session && (
                        <button
                          type="button"
                          className="link mono"
                          onClick={() => {
                            onOpenSession(e.session, e.seq, r.user);
                          }}
                        >
                          {shortId(e.session)}
                          {e.seq != null && ` · #${String(e.seq)}`}
                        </button>
                      )}
                      {e.quote && ` · «${e.quote}»`}
                    </span>
                  ))}
                </span>
                <span>Изменение</span>
                <span>
                  {r.change || '—'}
                  {r.target && (
                    <>
                      {' · '}
                      <span className="mono">{r.target}</span>
                    </>
                  )}
                </span>
                <span>Проверка</span>
                <span>
                  {r.check || '—'}
                  {r.result && ` · ${r.result}`}
                </span>
                <span>Откат</span>
                <span>{r.rollback || '—'}</span>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
