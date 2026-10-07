import { useQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';

import { meQueryOptions, usersQuery } from '../../../shared/api';
import { DataLimits, Empty, Loading, Notice, PageTitle, Panel } from '../../../shared/ui';
import { useTeam } from '../api/useDataset';
import { limitsOf } from '../model/gaps';
import { useAnalyticsFilters } from '../model/filters';
import { teamRows } from './rows';
import { TeamTable } from './TeamTable';
import './team.css';

const teamLimits = [
  'Числа человека считаются так же, как «Обзор» по его сессиям: те же фильтры периода, агента, проекта и служебных сессий.',
  'В списке все вошедшие пользователи; приглашённые без входа — в «Пользователях».',
];

// «Команда»: every person beside their numbers for the period (HT-435), counted by the server over
// everyone's sessions whatever ?user= says (HT-531).
export function TeamPage() {
  const { filters } = useAnalyticsFilters();
  const { data, error } = useTeam();
  const { data: users, error: usersError } = useQuery(usersQuery);
  const { data: me } = useQuery(meQueryOptions);
  const navigate = useNavigate();

  // Without the user list the people without sessions would silently miss: wait for it, and say
  // so when it fails, showing the people the data names.
  if (!data || (!users && !usersError)) {
    return (
      <div className="team">
        <PageTitle>Команда</PageTitle>
        {error ? (
          <Notice tone="err">Не удалось загрузить данные: {error.message}</Notice>
        ) : (
          <Loading />
        )}
      </div>
    );
  }

  const rows = teamRows(data.people, users?.items ?? [], me?.id);
  const limits = limitsOf(data.gaps, data.session_gaps, teamLimits);
  return (
    <div className="team">
      <PageTitle>Команда</PageTitle>
      {usersError && (
        <Notice tone="warn">
          Не удалось загрузить список пользователей: {usersError.message}. Показаны только люди с
          сессиями за период.
        </Notice>
      )}
      {rows.some((row) => row.lastActive !== null) ? (
        <Panel>
          <TeamTable
            rows={rows}
            filters={filters}
            onNavigate={(href) => {
              void navigate({ href });
            }}
          />
        </Panel>
      ) : (
        <Empty title="За период нет сессий ни у кого" />
      )}
      <DataLimits items={limits.items} count={limits.count} />
    </div>
  );
}
