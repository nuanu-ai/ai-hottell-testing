import { useQuery } from '@tanstack/react-query';
import { useId, useState } from 'react';

import { meQueryOptions } from '../../../shared/api';
import { Loading, Notice, Panel } from '../../../shared/ui';
import { type Proposal, proposalsQuery, usersQuery } from './api';
import { ProposalEvidence, ProposalState } from './ProposalDetails';
import { ProposalList } from './ProposalList';

// «Человек»: every signed-in user reads every registry, the filter narrows it to one person.
function PersonFilter({
  value,
  onChange,
}: {
  value: string | null;
  onChange: (user: string | null) => void;
}) {
  const id = useId();
  const users = useQuery(usersQuery);
  return (
    <label className="person-filter" htmlFor={id}>
      Человек{' '}
      <select
        id={id}
        value={value ?? ''}
        onChange={(event) => {
          onChange(event.target.value === '' ? null : event.target.value);
        }}
      >
        <option value="">Все</option>
        {users.data?.items
          .filter((u) => u.status === 'active')
          .map((u) => (
            <option key={u.id} value={u.id}>
              {u.name || u.email}
            </option>
          ))}
      </select>
    </label>
  );
}

// The proposal registry with the «Человек» filter (the place of «Темы» in the analysed mode).
export function ProposalRegistry({
  days,
  user: initialUser = null,
  onOpenLine,
}: {
  days: number | null;
  user?: string | null;
  // Opens the session feed at a transcript line of the evidence (L<n>); the screen owns the route.
  onOpenLine: (sid: string, src: number | undefined, owner?: string) => void;
}) {
  const [user, setUser] = useState<string | null>(initialUser);
  const proposals = useQuery(proposalsQuery(user));
  const me = useQuery(meQueryOptions);
  // Only the registry's owner decides; RBAC comes later (HT-159 decision 13).
  const details = (p: Proposal) => ({
    left: <ProposalEvidence proposal={p} onOpenLine={onOpenLine} />,
    right: <ProposalState proposal={p} owner={me.data?.id === p.user_id} />,
  });
  return (
    <Panel
      title="Предложения"
      sub="сначала важные, затем по числу сессий"
      action={<PersonFilter value={user} onChange={setUser} />}
    >
      {proposals.isPending ? (
        <Loading />
      ) : proposals.isError ? (
        <Notice tone="err">
          Не удалось загрузить реестр предложений: {proposals.error.message}
        </Notice>
      ) : (
        <ProposalList
          proposals={proposals.data.proposals}
          days={days}
          details={details}
          canDiscuss={(p) => me.data?.id === p.user_id}
        />
      )}
    </Panel>
  );
}
