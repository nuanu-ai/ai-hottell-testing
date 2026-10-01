import { useQuery } from '@tanstack/react-query';
import { useId, useState } from 'react';

import type { components } from '../../shared/api';
import { Button, Card, Notice, PageTitle } from '../../shared/ui';
import {
  isVersionConflict,
  telemetrySettingsQuery,
  useSaveTelemetrySettings,
} from './api/settings';
import { FoldersCard } from './FoldersCard';
import {
  agents,
  contentCategories,
  hookEvents,
  hookFields,
  isAgentOn,
  isContentOn,
  isEventOn,
  isFieldOn,
  isSourceOn,
  setAgent,
  setBackfill,
  setContent,
  setEvent,
  setField,
  setSource,
  sources,
  unhookedEvents,
  type AgentId,
  type Note,
  type TelemetrySettings,
} from './model';

type VersionedSettings = components['schemas']['VersionedTelemetrySettings'];

export function TelemetrySettingsPage() {
  return (
    <>
      <div style={{ marginBottom: 16 }}>
        <PageTitle>Что отправлять</PageTitle>
        <p className="hint" style={{ margin: '4px 0 0' }}>
          По умолчанию собирается всё. Снимите отметку с того, чем не хотите делиться. Запреты
          действуют на всех ваших машинах
        </p>
      </div>
      <Settings />
    </>
  );
}

function Settings() {
  const settings = useQuery(telemetrySettingsQuery);

  if (settings.isPending) {
    return (
      <Card>
        <div className="loading">
          <span className="spinner" />
          Загружаем настройки…
        </div>
      </Card>
    );
  }

  if (settings.isError) {
    return (
      <Card>
        <Notice tone="err">
          {settings.error.message}{' '}
          <Button
            size="sm"
            disabled={settings.isFetching}
            onClick={() => {
              void settings.refetch();
            }}
          >
            Повторить
          </Button>
        </Notice>
      </Card>
    );
  }

  return (
    <SettingsForm loaded={settings.data} reload={async () => (await settings.refetch()).data} />
  );
}

type SettingsFormProps = {
  loaded: VersionedSettings;
  reload: () => Promise<VersionedSettings | undefined>;
};

// The draft keeps the version it was read at: a background refetch never moves it, so a
// save made over someone else's changes gets its 409.
function SettingsForm({ loaded, reload }: SettingsFormProps) {
  const save = useSaveTelemetrySettings();
  const [draft, setDraft] = useState(loaded);
  const [reloading, setReloading] = useState(false);
  const settings = draft.settings;

  const change = (next: TelemetrySettings) => {
    setDraft({ ...draft, settings: next });
    if (!save.isIdle) {
      save.reset();
    }
  };

  const reloadSettings = async () => {
    setReloading(true);
    try {
      const fresh = await reload();
      if (fresh) {
        setDraft(fresh);
        save.reset();
      }
    } finally {
      setReloading(false);
    }
  };

  const conflict = isVersionConflict(save.error);

  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(
          { settings, expectedVersion: draft.version },
          {
            onSuccess: (saved) => {
              setDraft(saved);
            },
          },
        );
      }}
    >
      {agents.map((agent) => (
        <AgentCard key={agent.id} agent={agent} settings={settings} onChange={change} />
      ))}
      <FoldersCard settings={settings} onChange={change} />
      <Card>
        <h2>История</h2>
        <Toggle
          label="Догрузить историю"
          checked={settings.backfill_history}
          note={{
            text: 'Один раз отправить транскрипты, которые были до установки бинаря. К истории применяются запреты агента, транскриптов и папок: при выключенных транскриптах она не отправляется',
          }}
          onChange={(on) => {
            change(setBackfill(settings, on));
          }}
        />
      </Card>
      <Card>
        <div style={{ display: 'grid', gap: 12 }}>
          {conflict && (
            <Notice tone="warn">
              Настройки изменились в другом окне. Перезагрузите их и повторите изменения{' '}
              <Button
                size="sm"
                disabled={reloading}
                onClick={() => {
                  void reloadSettings();
                }}
              >
                Перезагрузить
              </Button>
            </Notice>
          )}
          {save.error && !conflict && <Notice tone="err">{save.error.message}</Notice>}
          {save.isSuccess && (
            <Notice tone="ok">
              Сохранено. Изменения нативного OTel вступят в силу в новых сессиях агентов: у Claude
              Code — в новой сессии, у Codex — после перезапуска
            </Notice>
          )}
          <div className="row" style={{ gap: 12 }}>
            <Button variant="primary" type="submit" disabled={save.isPending || reloading}>
              Сохранить
            </Button>
            <span className="hint">Версия настроек: {draft.version}</span>
          </div>
        </div>
      </Card>
    </form>
  );
}

type AgentCardProps = {
  agent: { id: AgentId; title: string };
  settings: TelemetrySettings;
  onChange: (next: TelemetrySettings) => void;
};

function AgentCard({ agent, settings, onChange }: AgentCardProps) {
  const on = isAgentOn(settings, agent.id);

  return (
    <Card>
      <h2>{agent.title}</h2>
      <Toggle
        label={`Отправлять данные ${agent.title}`}
        checked={on}
        note={{
          text: 'Выключенный агент выключен целиком: хуки не ставятся, транскрипты не читаются, нативный OTel выключен',
        }}
        onChange={(next) => {
          onChange(setAgent(settings, agent.id, next));
        }}
      />
      <fieldset disabled={!on} style={fieldsetReset}>
        <h3 style={{ margin: '16px 0 8px' }}>Источники</h3>
        <div style={{ display: 'grid', gap: 10 }}>
          {sources.map((source) => (
            <Toggle
              key={source.id}
              label={source.title}
              checked={isSourceOn(settings, agent.id, source.id)}
              note={source.notes[agent.id]}
              onChange={(next) => {
                onChange(setSource(settings, agent.id, source.id, next));
              }}
            />
          ))}
        </div>
        <HookSections agent={agent} settings={settings} onChange={onChange} />
        <h3 style={{ margin: '16px 0 4px' }}>Содержимое нативного OTel</h3>
        <p className="hint" style={{ margin: '0 0 8px' }}>
          Запрет убирает содержимое, но не сам сигнал: счётчики, длины и размеры продолжают уходить.
          Запрет папки на нативный OTel не действует — чтобы ничего не уходило, выключите источник
          или агента
        </p>
        <div style={{ display: 'grid', gap: 10 }}>
          {contentCategories.map((category) => (
            <Toggle
              key={category.id}
              label={category.title}
              checked={isContentOn(settings, agent.id, category.id)}
              note={category.notes[agent.id]}
              onChange={(next) => {
                onChange(setContent(settings, agent.id, category.id, next));
              }}
            />
          ))}
        </div>
      </fieldset>
    </Card>
  );
}

const fieldsetReset = { border: 0, margin: 0, padding: 0, minWidth: 0 } as const;

// Hook events and fields are cut from the hooks source only, so they wait while it is off.
function HookSections({ agent, settings, onChange }: AgentCardProps) {
  const hooksOn = isSourceOn(settings, agent.id, 'hooks');

  return (
    <fieldset disabled={!hooksOn} style={fieldsetReset}>
      <h3 style={{ margin: '16px 0 4px' }}>События хуков</h3>
      <p className="hint" style={{ margin: '0 0 8px' }}>
        {hooksOn
          ? 'Снимите отметку с события, чтобы бинарь его не отправлял'
          : 'Источник «События хуков» выключен: не отправляется ни одно событие'}
      </p>
      <div
        role="group"
        aria-label={`События хуков ${agent.title}`}
        style={{
          display: 'grid',
          gap: 8,
          gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
        }}
      >
        {hookEvents[agent.id].map((event) => {
          const unhooked = unhookedEvents[agent.id].includes(event);
          return (
            <label
              key={event}
              className="mono"
              style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}
            >
              <input
                type="checkbox"
                checked={!unhooked && isEventOn(settings, agent.id, event)}
                disabled={unhooked}
                onChange={(e) => {
                  onChange(setEvent(settings, agent.id, event, e.target.checked));
                }}
              />
              {event}
            </label>
          );
        })}
      </div>
      {unhookedEvents[agent.id].length > 0 && (
        <p className="hint" style={{ margin: '8px 0 0' }}>
          ✗ {unhookedEvents[agent.id].join(' и ')} не отправляются никогда: хук на них заменил бы
          создание и удаление worktree
        </p>
      )}
      <h3 style={{ margin: '16px 0 4px' }}>Поля событий</h3>
      <p className="hint" style={{ margin: '0 0 8px' }}>
        Снятая отметка вырезает поле верхнего уровня из всех событий, где оно есть; остальное
        событие уходит. Поля внутри поля так не вырезаются
      </p>
      <div style={{ display: 'grid', gap: 10 }}>
        {hookFields[agent.id].map((field) => (
          <Toggle
            key={field.id}
            label={field.id}
            checked={isFieldOn(settings, agent.id, field.id)}
            note={{ text: `В событиях: ${field.events}` }}
            onChange={(next) => {
              onChange(setField(settings, agent.id, field.id, next));
            }}
          />
        ))}
      </div>
    </fieldset>
  );
}

type ToggleProps = {
  label: string;
  checked: boolean;
  note: Note;
  onChange: (on: boolean) => void;
};

const noteMarks = { warn: '⚠ ', off: '✗ ' } as const;

function Toggle({ label, checked, note, onChange }: ToggleProps) {
  const noteId = useId();

  return (
    <div>
      <label style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
        <input
          type="checkbox"
          checked={checked}
          aria-describedby={noteId}
          onChange={(event) => {
            onChange(event.target.checked);
          }}
        />
        {label}
      </label>
      <p id={noteId} className="hint" style={{ margin: '2px 0 0 24px' }}>
        {note.tone && noteMarks[note.tone]}
        {note.text}
      </p>
    </div>
  );
}
