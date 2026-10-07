import { useEffect } from 'react';

import { Segmented } from '../../../shared/ui';
import { usePageFacets } from '../api/useDataset';
import {
  type AgentFilter,
  type KindFilter,
  type Period,
  useAnalyticsFilters,
} from '../model/filters';
import { noProject } from '../model/sample';
import { PersonSelect } from './PersonSelect';

const periodOptions: { value: `${Period}`; label: string }[] = [
  { value: '7', label: '7 дн' },
  { value: '14', label: '14 дн' },
  { value: '30', label: '30 дн' },
  { value: 'all', label: 'Всё' },
];

const agentOptions: { value: AgentFilter; label: string }[] = [
  { value: 'all', label: 'Все' },
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
];

const kindOptions: { value: KindFilter; label: string }[] = [
  { value: 'work', label: 'Без служебных' },
  { value: 'all', label: 'Все' },
];

const toPeriod = (value: `${Period}`): Period =>
  value === 'all' ? 'all' : (Number(value) as Period);

// The projects of the data in alphabetical order, «без проекта» last.
function sortedProjects(projects: readonly string[]): string[] {
  const names = new Set(projects);
  const named = [...names].filter((name) => name !== noProject).sort((a, b) => a.localeCompare(b));
  return names.has(noProject) ? [...named, noProject] : named;
}

// The left of the head's second row (README v5.1 «Шапка, строка 2»): period, agent, project, person,
// service sessions. hidePerson drops «Человек» on a page about everyone («Команда»): its projects and
// its service sessions then come from everyone's data.
export function FiltersBar({ hidePerson = false }: { hidePerson?: boolean }) {
  const { filters, setFilter } = useAnalyticsFilters();
  const facets = usePageFacets();

  const projects = facets.projects ? sortedProjects(facets.projects) : [];
  const { hasSystem, isPlaceholderData } = facets;
  // A project the data no longer has would leave the page empty: fall back to every project. Only
  // the answer to the current request decides: the previous period's data kept on screen may lack
  // a project the new one has.
  const staleProject =
    facets.projects !== undefined &&
    !isPlaceholderData &&
    filters.project !== 'all' &&
    !projects.includes(filters.project);
  useEffect(() => {
    if (staleProject) {
      setFilter('project', 'all');
    }
  }, [staleProject, setFilter]);

  return (
    <div className="filters">
      <Segmented
        label="Период"
        options={periodOptions}
        value={String(filters.days) as `${Period}`}
        onChange={(value) => {
          setFilter('days', toPeriod(value));
        }}
      />
      <Segmented
        label="Агент"
        options={agentOptions}
        value={filters.agent}
        onChange={(value) => {
          setFilter('agent', value);
        }}
      />
      <select
        className="seg-select"
        aria-label="Проект"
        value={staleProject ? 'all' : filters.project}
        onChange={(event) => {
          setFilter('project', event.target.value);
        }}
      >
        <option value="all">Все проекты</option>
        {projects.map((project) => (
          <option key={project} value={project}>
            {project}
          </option>
        ))}
      </select>
      {!hidePerson && <PersonSelect />}
      {hasSystem && (
        <Segmented
          label="Служебные сессии"
          options={kindOptions}
          value={filters.kind}
          onChange={(value) => {
            setFilter('kind', value);
          }}
        />
      )}
    </div>
  );
}
