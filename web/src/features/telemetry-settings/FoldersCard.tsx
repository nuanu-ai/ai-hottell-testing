import { Link } from '@tanstack/react-router';
import { useId, useState } from 'react';

import { Actions, Button, Field, Hint, Panel, Stack, Table, Tag, Td, Th } from '../../shared/ui';
import {
  addFolder,
  allowChangesNothing,
  folderPatternError,
  folderPatterns,
  removeFolder,
  type FolderList,
  type TelemetrySettings,
} from './model';

type FoldersCardProps = {
  settings: TelemetrySettings;
  saved: TelemetrySettings;
  onChange: (next: TelemetrySettings) => void;
};

const lists: { id: FolderList; title: string; label: string; hint: string }[] = [
  {
    id: 'denied',
    title: 'Запрещённые папки',
    label: 'Запретить папку',
    hint: 'Хуки и транскрипты сессий, запущенных в этих папках, не отправляются',
  },
  {
    id: 'allowed',
    title: 'Исключения из запрета',
    label: 'Разрешить внутри запрета',
    hint: 'Снимает запрет с папок внутри запрещённых; вне запрета ничего не меняет',
  },
];

// The folder rules are shared by both agents, so the card stands apart from them.
export function FoldersCard({ settings, saved, onChange }: FoldersCardProps) {
  return (
    <Panel title="Папки проектов">
      <Stack gap={16}>
        <Hint>
          Шаблон сравнивается с папкой сессии целиком и начинается с / или с ~/ — домашнего каталога
          на каждой машине. ** — любое число сегментов пути, например ~/work/**. Запрет общий для
          обоих агентов
        </Hint>
        <Hint>
          Пример: запрещена ~/work/**, разрешена ~/work/oss/**. Тогда ~/work/client-a закрыта, а
          ~/work/oss/tool и ~/projects/x открыты
        </Hint>
        <Hint>
          На нативный OTel запрет папки не действует — подробнее на вкладках{' '}
          <Link to="/telemetry" search={{ tab: 'claude' }}>
            Claude Code
          </Link>{' '}
          и{' '}
          <Link to="/telemetry" search={{ tab: 'codex' }}>
            Codex
          </Link>
        </Hint>
        {lists.map((list) => (
          <FolderList
            key={list.id}
            list={list}
            patterns={folderPatterns(settings, list.id)}
            saved={folderPatterns(saved, list.id)}
            denied={folderPatterns(settings, 'denied')}
            onAdd={(pattern) => {
              onChange(addFolder(settings, list.id, pattern));
            }}
            onRemove={(pattern) => {
              onChange(removeFolder(settings, list.id, pattern));
            }}
          />
        ))}
      </Stack>
    </Panel>
  );
}

type FolderListProps = {
  list: (typeof lists)[number];
  patterns: string[];
  saved: string[];
  // The denied patterns of the draft: an allowed pattern under none of them changes nothing.
  denied: string[];
  onAdd: (pattern: string) => void;
  onRemove: (pattern: string) => void;
};

function FolderList({ list, patterns, saved, denied, onAdd, onRemove }: FolderListProps) {
  const inputId = useId();
  const [value, setValue] = useState('');
  const [error, setError] = useState<string>();

  // The input sits inside the settings form: Enter adds the pattern instead of saving.
  const add = () => {
    const pattern = value.trim();
    const problem = folderPatternError(pattern, patterns);
    setError(problem);
    if (!problem) {
      onAdd(pattern);
      setValue('');
    }
  };

  return (
    <section aria-label={list.title}>
      <Stack gap={8}>
        <h3>{list.title}</h3>
        <Hint>{list.hint}</Hint>
        {patterns.length > 0 && (
          <Table>
            <thead>
              <tr>
                <Th>шаблон</Th>
                <Th></Th>
                <Th></Th>
              </tr>
            </thead>
            <tbody>
              {patterns.map((pattern) => (
                <tr key={pattern}>
                  <Td wrap className="mono">
                    {pattern}
                  </Td>
                  <Td>
                    <Actions>
                      {!saved.includes(pattern) && <Tag tone="warn">не сохранено</Tag>}
                      {list.id === 'allowed' && allowChangesNothing(pattern, denied) && (
                        <Tag tone="warn">ничего не меняет</Tag>
                      )}
                    </Actions>
                  </Td>
                  <Td numeric>
                    <Button
                      size="sm"
                      aria-label={`Удалить ${pattern}`}
                      onClick={() => {
                        onRemove(pattern);
                      }}
                    >
                      Удалить
                    </Button>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
        <Field label={list.label} htmlFor={inputId} error={error} errorId={`${inputId}-error`}>
          <Actions>
            <input
              id={inputId}
              type="text"
              className="mono"
              placeholder="~/work/**"
              value={value}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? `${inputId}-error` : undefined}
              onChange={(event) => {
                setValue(event.target.value);
                setError(undefined);
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  add();
                }
              }}
            />
            <Button size="sm" onClick={add}>
              Добавить
            </Button>
          </Actions>
        </Field>
      </Stack>
    </section>
  );
}
