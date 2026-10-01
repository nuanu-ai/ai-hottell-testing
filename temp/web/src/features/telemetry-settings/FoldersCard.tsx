import { useId, useState } from 'react';

import { Button, Card, Field } from '../../shared/ui';
import {
  addFolder,
  folderPatternError,
  folderPatterns,
  removeFolder,
  type FolderList,
  type TelemetrySettings,
} from './model';

type FoldersCardProps = {
  settings: TelemetrySettings;
  onChange: (next: TelemetrySettings) => void;
};

const lists: { id: FolderList; title: string; hint: string }[] = [
  {
    id: 'denied',
    title: 'Не отправлять',
    hint: 'Хуки и транскрипты сессий, запущенных в этих папках, не отправляются',
  },
  {
    id: 'allowed',
    title: 'Разрешить внутри запрещённого',
    hint: 'Снимает запрет с папок внутри запрещённых; вне запрета ничего не меняет',
  },
];

// The folder rules are shared by both agents, so the card stands apart from them.
export function FoldersCard({ settings, onChange }: FoldersCardProps) {
  return (
    <Card>
      <h2>Папки проектов</h2>
      <p className="hint" style={{ margin: '0 0 8px' }}>
        Шаблон сравнивается с папкой сессии целиком и начинается с / или с ~/ — домашнего каталога
        на каждой машине. ** — любое число сегментов пути, например ~/work/**. Запрет общий для
        обоих агентов
      </p>
      <p className="hint" style={{ margin: '0 0 8px' }}>
        ✗ На нативный OTel запрет папки не действует ни у Claude Code, ни у Codex: их сессии в
        запрещённой папке отправляют нативный OTel по общим настройкам. Чтобы ничего не уходило,
        выключите источник или агента
      </p>
      {lists.map((list) => (
        <FolderList
          key={list.id}
          list={list}
          patterns={folderPatterns(settings, list.id)}
          onAdd={(pattern) => {
            onChange(addFolder(settings, list.id, pattern));
          }}
          onRemove={(pattern) => {
            onChange(removeFolder(settings, list.id, pattern));
          }}
        />
      ))}
    </Card>
  );
}

type FolderListProps = {
  list: (typeof lists)[number];
  patterns: string[];
  onAdd: (pattern: string) => void;
  onRemove: (pattern: string) => void;
};

function FolderList({ list, patterns, onAdd, onRemove }: FolderListProps) {
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
    <section aria-label={list.title} style={{ marginTop: 16 }}>
      <h3 style={{ margin: '0 0 4px' }}>{list.title}</h3>
      <p className="hint" style={{ margin: '0 0 8px' }}>
        {list.hint}
      </p>
      {patterns.length > 0 && (
        <ul style={{ listStyle: 'none', margin: '0 0 8px', padding: 0, display: 'grid', gap: 6 }}>
          {patterns.map((pattern) => (
            <li key={pattern} className="row" style={{ gap: 12 }}>
              <span className="mono">{pattern}</span>
              <Button
                size="sm"
                aria-label={`Удалить ${pattern}`}
                onClick={() => {
                  onRemove(pattern);
                }}
              >
                Удалить
              </Button>
            </li>
          ))}
        </ul>
      )}
      <Field label="Шаблон пути" htmlFor={inputId} error={error} errorId={`${inputId}-error`}>
        <div className="row" style={{ gap: 12 }}>
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
        </div>
      </Field>
    </section>
  );
}
