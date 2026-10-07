import { useQuery } from '@tanstack/react-query';
import { useParams, useSearch } from '@tanstack/react-router';
import { useState } from 'react';

import { usersQuery } from '../../../shared/api';
import { discussActions } from '../../../shared/lib/discuss';
import { Coverage, DiscussActions, Grid, Loading, Notice } from '../../../shared/ui';
import { pulseQuery, sessionQuery } from '../api/queries';
import { usePersonState } from '../api/useDataset';
import { SessionRetroActions } from '../deep/SessionRetroActions';
import { SkillOpportunities } from '../deep/SkillOpportunities';
import { coverageLine } from '../fix/coverage';
import { FrictionPage } from '../friction/FrictionPage';
import {
  allPeople,
  findSession,
  keepFilters,
  personOf,
  sessionKeys,
  sessionOwner,
  type SessionSearch,
  whoseOf,
  whoseSession,
} from '../model/filters';
import { sample } from '../model/sample';
import { FrictionAndCost } from '../overview/FrictionAndCost';
import { OverviewPage } from '../overview/OverviewPage';
import { Spend } from '../overview/Spend';
import { OverviewGaps, WorkAndGit } from '../overview/WorkAndGit';
import { DeepPanels } from '../session/DeepPanels';
import { activeInPulse, LIVE_POLL_MS, liveRefetchInterval } from '../session/live';
import { SessionAside, SessionGaps } from '../session/SessionAside';
import { SessionPage } from '../session/SessionPage';
import { Timeline } from '../session/Timeline';
import type { TimelineMode } from '../session/timelineWindow';
import { SessionsPage } from '../sessions/SessionsPage';
import { SkillsPage } from '../skills/SkillsPage';
import { ToolsPage } from '../tools/ToolsPage';
import { daysOf, useScreen, withFilters } from './useScreen';

// The analytics tabs as routes see them: each reads the dataset and the filters, builds the page's
// sample and hands the screen its props. The screens themselves know no URL and no request.

function Waiting({ error }: { error: Error | null }) {
  return error ? (
    <Notice tone="err">Не удалось загрузить данные: {error.message}</Notice>
  ) : (
    <Loading />
  );
}

export function OverviewScreen() {
  const { filters, data, error, X, hidden, go, me } = useScreen();
  if (!data || !X) return <Waiting error={error} />;
  const previous = filters.days === 'all' ? null : sample(data, filters, 1);
  const coverage = coverageLine(X.S);
  const days = daysOf(filters);
  return (
    <OverviewPage
      sample={{ S: X.S, system: X.sys }}
      previous={previous && { S: previous.S, system: previous.sys }}
      days={days}
      kind={filters.kind}
      whose={whoseOf(filters, me)}
      coverage={coverage && <Coverage>{coverage}</Coverage>}
    >
      <Grid>
        <Spend sessions={X.S} days={days} end={data.window.to} />
        <FrictionAndCost
          sessions={X.S}
          friction={data.friction}
          findings={data.findings}
          hidden={hidden}
          onTopic={(id) => {
            go(withFilters('/fix', filters, { open: id }));
          }}
          onSession={(s) => {
            go(withFilters(`/sessions/${encodeURIComponent(s.id)}`, filters, sessionKeys(s)));
          }}
          onAllSignals={() => {
            go(withFilters('/friction', filters));
          }}
          onAllSessions={() => {
            go(withFilters('/sessions', filters));
          }}
        />
        <WorkAndGit sessions={X.S} days={days} end={data.window.to} whose={whoseOf(filters, me)} />
      </Grid>
      <OverviewGaps gaps={data.gaps} pricingNote={data.pricing.note} />
    </OverviewPage>
  );
}

export function SessionsScreen() {
  const { filters, setFilter, data, error, X, go } = useScreen();
  const search: { flag?: string } = useSearch({ strict: false });
  const everyone = filters.user === allPeople;
  const { data: users } = useQuery({ ...usersQuery, enabled: everyone });
  if (!data || !X) return <Waiting error={error} />;
  const userNames = new Map((users?.items ?? []).map((user) => [user.id, user.name]));
  return (
    <SessionsPage
      sessions={X.S}
      allSessions={X.all}
      kind={filters.kind}
      onShowSystem={() => {
        setFilter('kind', 'all');
      }}
      flag={search.flag}
      onClearFlag={() => {
        go(withFilters('/sessions', filters));
      }}
      showPerson={everyone}
      userNames={userNames}
      friction={data.friction}
      gaps={data.gaps}
      sessionHref={(s) =>
        withFilters(`/sessions/${encodeURIComponent(s.id)}`, filters, sessionKeys(s))
      }
      onNavigate={go}
    />
  );
}

export function SessionScreen() {
  const { id = '' } = useParams({ strict: false });
  const search: SessionSearch = useSearch({ strict: false });
  const { filters, data, error, go, me } = useScreen();
  const session = data && findSession(data.sessions, id, search);
  // By the session's owner, not the filter: «Ваше…» and «Обсудить» only on one's own (HT-444).
  const whose = whoseSession(sessionOwner(session, search), me);
  const [mode, setMode] = useState<TimelineMode>(search.line || search.src ? 'all' : 'main');
  // A session that went quiet and then resumed shows up in the pulse: that restarts the polling.
  const person = usePersonState();
  const { data: pulse } = useQuery({ ...pulseQuery(person.user), enabled: person.known });
  const activeNow = activeInPulse(pulse, session);
  // The feed does not depend on the dataset: a proposal's evidence may be older than the
  // period or another person's, and its owner comes with the link (HT-405). A session outside
  // the dataset is asked for only when the link points at it — an owner, a line or the session
  // keys of a live one; a bare unknown id would only answer 404 (HT-497).
  const pointed =
    search.owner !== undefined ||
    search.line !== undefined ||
    search.src !== undefined ||
    search.session_agent !== undefined ||
    search.session_user !== undefined;
  const timeline = useQuery({
    ...sessionQuery(id, { agent: session?.agent, user: session?.user_id ?? search.owner }),
    enabled: id !== '' && data !== undefined && (session !== undefined || pointed),
    refetchInterval: (query) => (activeNow ? LIVE_POLL_MS : liveRefetchInterval(query.state.data)),
    refetchIntervalInBackground: false,
  });
  if (!data) return <Waiting error={error} />;
  const feed = (
    <Timeline
      timeline={timeline.data}
      error={timeline.error}
      mode={mode}
      onModeChange={setMode}
      line={search.line}
      src={search.src}
      live={activeNow || liveRefetchInterval(timeline.data) !== false}
    />
  );
  return (
    <SessionPage
      session={session}
      onBack={() => {
        go(withFilters('/sessions', filters));
      }}
      whose={whose}
      // «Разобрать» runs the retro of this session; «Обсудить» asks the coach about it over the
      // period of the filters. Not daysOf: «Всё» is the whole period here, not 0 days.
      discussSlot={
        session &&
        whose === 'own' && (
          <>
            <SessionRetroActions sessionId={session.id} size="sm" />
            <DiscussActions
              size="sm"
              actions={discussActions({
                mode: 'coach',
                subject: `сессия ${session.id}`,
                days: filters.days === 'all' ? null : filters.days,
              })}
            />
          </>
        )
      }
    >
      {!session && id !== '' && (
        <>
          {pointed && feed}
          <DeepPanels
            discuss={whose === 'own'}
            sessionId={id}
            onOpenLine={(src) => {
              go(
                withFilters(`/sessions/${encodeURIComponent(id)}`, filters, {
                  session_agent: search.session_agent,
                  session_user: search.session_user,
                  owner: search.owner,
                  src,
                }),
              );
            }}
          />
        </>
      )}
      {session && (
        <>
          <SessionAside session={session} timeline={timeline.data} feed={feed} />
          {/* Deep L<n> is a transcript line, not a feed event: ?src= finds the event built
              from it (HT-410). */}
          <DeepPanels
            discuss={whose === 'own'}
            sessionId={session.id}
            user={session.user_id}
            agent={session.agent}
            onOpenLine={(src) => {
              go(
                withFilters(`/sessions/${encodeURIComponent(session.id)}`, filters, {
                  ...sessionKeys(session),
                  src,
                }),
              );
            }}
          />
          <SessionGaps gaps={data.gaps} id={session.id} />
        </>
      )}
    </SessionPage>
  );
}

export function FrictionScreen() {
  const { filters, data, error, X, hidden, go } = useScreen();
  if (!data || !X) return <Waiting error={error} />;
  return (
    <FrictionPage dataset={data} selection={X} hidden={hidden} filters={filters} onNavigate={go} />
  );
}

export function ToolsScreen() {
  const { data, error, X } = useScreen();
  if (!data || !X) return <Waiting error={error} />;
  return <ToolsPage dataset={data} selection={X} />;
}

export function SkillsScreen() {
  const { filters, data, error, X, go, me } = useScreen();
  if (!data || !X) return <Waiting error={error} />;
  // The opportunities go inside the page: «Ограничения данных» stay its last block.
  return (
    <SkillsPage
      dataset={data}
      selection={X}
      days={daysOf(filters)}
      onNavigate={(href) => {
        go(keepFilters(href, filters));
      }}
    >
      <SkillOpportunities
        user={personOf(filters, me) ?? null}
        onOpenLine={(sid, src) => {
          go(withFilters(`/sessions/${encodeURIComponent(sid)}`, filters, { src }));
        }}
      />
    </SkillsPage>
  );
}
