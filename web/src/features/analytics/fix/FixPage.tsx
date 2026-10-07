import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import { meQueryOptions } from '../../../shared/api';
import { discussActions } from '../../../shared/lib/discuss';
import {
  Coverage,
  Cycle,
  DataLimits,
  DiscussActions,
  Hero,
  Loading,
  Notice,
  SectionHead,
} from '../../../shared/ui';
import { journalQuery } from '../coach/api';
import { journalCycleSteps, journalRows, rowsOfStep } from '../coach/journal';
import { JournalList } from '../coach/JournalList';
import { ProposalRegistry } from '../coach/ProposalRegistry';
import { usePerson } from '../api/useDataset';
import { hiddenTopicsQuery } from '../api/hiddenTopics';
import { useDataset } from '../api/useDataset';
import { filtersToSearch, useAnalyticsFilters, whoseOf } from '../model/filters';
import { useNavigate } from '@tanstack/react-router';
import { pageGaps } from '../model/gaps';
import { sample, scopeOf, topicsOf } from '../model/sample';
import { coverageLine } from './coverage';
import { fixLimits, heroSub, topicsHeadline } from './headline';
import { HealthSection } from './HealthSection';
import { Topics } from './Topics';
import { periodText, steps, type StepKey } from './cycleSteps';
import './fix.css';

const noneHidden: ReadonlySet<string> = new Set();

const severityRank = { bad: 0, warn: 1, info: 2 } as const;

// «Что исправить» (README v5.1), the start page: the improvement cycle over the page's sample,
// then the topics. Steps 4–6 and «Исправлено» come from the coach journal: loading or failed until
// it answers, empty only on an empty answer.
export function FixPage() {
  const { filters } = useAnalyticsFilters();
  const { data, error } = useDataset();
  const { data: hidden = noneHidden } = useQuery(hiddenTopicsQuery);
  const [step, setStep] = useState<StepKey | null>(null);
  const [moreTopics, setMoreTopics] = useState(false);
  const person = usePerson();
  const { data: me } = useQuery(meQueryOptions);
  // «Обсудить» only on one's own data: the coach reads this Mac's transcripts (HT-444).
  const own = whoseOf(filters, me?.id) === 'own';
  const days = filters.days === 'all' ? null : filters.days;
  const navigate = useNavigate();
  // The coach journal of the page's period and person: steps 4–6 and «Исправлено».
  const to = data?.window.to;
  const from =
    to && filters.days !== 'all'
      ? new Date(Date.parse(to) - filters.days * 86_400_000).toISOString()
      : undefined;
  const journal = useQuery({ ...journalQuery({ user: person ?? null, from, to }), enabled: !!to });
  const openLine = (sid: string, line: number | null | undefined, owner?: string) => {
    void navigate({
      to: '/sessions/$id',
      params: { id: sid },
      search: {
        ...filtersToSearch(filters),
        ...(line != null ? { line } : {}),
        ...(owner ? { owner } : {}),
      },
    });
  };
  // A transcript line (Deep L<n>) opens the feed at the event built from it (HT-410).
  const openSrc = (sid: string, src: number | undefined, owner?: string) => {
    void navigate({
      to: '/sessions/$id',
      params: { id: sid },
      search: {
        ...filtersToSearch(filters),
        ...(src != null ? { src } : {}),
        ...(owner ? { owner } : {}),
      },
    });
  };

  if (!data) {
    return error ? (
      <Notice tone="err">Не удалось загрузить данные: {error.message}</Notice>
    ) : (
      <Loading />
    );
  }

  const X = sample(data, filters);
  const topics = topicsOf(data, X, hidden);
  const hiddenCount = data.findings.filter((f) => hidden.has(f.id)).length;
  const sampleSteps = steps({
    X,
    kind: filters.kind,
    days: filters.days,
    friction: data.friction,
    topics: topics.length,
    journal: null,
  });
  // Steps 1–3 count the sample, 4–6 the journal; until it answers they read «—».
  const cycle = journal.data
    ? [...sampleSteps.slice(0, 3), ...journalCycleSteps(journal.data.cycle)]
    : sampleSteps;
  const journalEmpty = !journal.data || journal.data.cycle.empty;
  const rows = journal.data ? journalRows(journal.data.entries) : [];
  const coverage = coverageLine(X.S);
  // Collection health is about the collector: every kind of session of the filter counts.
  const everyKind = sample(data, { ...filters, kind: 'all' }).ids;
  const health = data.findings
    .filter((f) => scopeOf(f) === 'collection' && f.sessions.some((id) => everyKind.has(id)))
    .sort((a, b) => severityRank[a.sev] - severityRank[b.sev]);
  const limits = pageGaps(data.gaps, fixLimits);

  return (
    <div className="fix">
      <Cycle
        steps={cycle}
        selected={step}
        onSelect={(key) => {
          const chosen = key as StepKey;
          // A pressed step 4–6 pressed again shows every record.
          setStep((current) => (current === chosen ? null : chosen));
          // Steps 1–3 open every topic; 4–6 filter «Исправлено».
          if (chosen === 'sessions' || chosen === 'signals' || chosen === 'topics') {
            setMoreTopics(true);
          }
        }}
        hint={journalEmpty ? 'Обсудите первую тему в Codex или Claude Code' : undefined}
        animateGrowth
      />
      <Hero
        title={topicsHeadline(topics.length)}
        sub={heroSub(X.U.length, X.A.length, periodText(filters.days))}
        actions={
          own ? (
            <DiscussActions actions={discussActions({ mode: 'coach', subject: 'обзор', days })} />
          ) : undefined
        }
      />
      {coverage && <Coverage>{coverage}</Coverage>}
      <Topics
        topics={topics}
        X={X}
        sessions={data.sessions}
        hidden={hiddenCount}
        more={moreTopics}
        onMore={setMoreTopics}
        days={days}
        canDiscuss={own}
      />
      <ProposalRegistry days={days} user={person ?? null} onOpenLine={openSrc} />
      {journal.data ? (
        <section className="journal">
          <SectionHead
            title="Исправлено"
            sub={rows.length ? 'журнал коуча · новые сверху' : 'журнал коуча'}
          />
          <JournalList
            rows={rowsOfStep(rows, step)}
            emptyJournal={rows.length === 0}
            onOpenSession={openLine}
          />
        </section>
      ) : (
        <section className="journal">
          <SectionHead title="Исправлено" sub="журнал коуча" />
          {journal.isError ? (
            <Notice tone="err">Не удалось загрузить журнал: {journal.error.message}</Notice>
          ) : (
            <Loading />
          )}
        </section>
      )}
      <HealthSection cards={health} ids={everyKind} />
      <DataLimits items={limits.items} count={limits.count} />
    </div>
  );
}
