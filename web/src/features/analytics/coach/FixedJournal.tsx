import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import { Loading, Notice, Stack } from '../../../shared/ui';
import { type JournalKey, journalQuery } from './api';
import { CycleSteps } from './CycleSteps';
import { JournalList } from './JournalList';
import { journalRows, rowsOfStep } from './journal';

// «Исправлено» with steps 4–6 above it: pressing «Внедрено» or «Проверено» narrows the list,
// pressing it again shows every record.
export function FixedJournal({
  onOpenSession,
  ...key
}: JournalKey & { onOpenSession: (sid: string, seq: number | null, owner: string) => void }) {
  const journal = useQuery(journalQuery(key));
  const [step, setStep] = useState<string | null>(null);
  if (journal.isPending) return <Loading />;
  if (journal.isError) {
    return <Notice tone="err">Не удалось загрузить журнал: {journal.error.message}</Notice>;
  }
  const rows = journalRows(journal.data.entries);
  return (
    <Stack gap={12}>
      <CycleSteps
        cycle={journal.data.cycle}
        selected={step}
        onSelect={(key) => {
          setStep((current) => (current === key ? null : key));
        }}
      />
      <div className="sec-h">
        <h2>Исправлено</h2>
        <span className="sub">{rows.length ? 'журнал коуча · новые сверху' : 'журнал коуча'}</span>
      </div>
      <JournalList
        rows={rowsOfStep(rows, step)}
        emptyJournal={rows.length === 0}
        onOpenSession={onOpenSession}
      />
    </Stack>
  );
}
