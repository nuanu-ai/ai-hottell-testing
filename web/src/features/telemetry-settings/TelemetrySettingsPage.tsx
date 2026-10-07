import { useQuery } from '@tanstack/react-query';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';

import type { components } from '../../shared/api';
import {
  Button,
  Hint,
  LoadError,
  Loading,
  Notice,
  PageTitle,
  Panel,
  TabPanel,
  Tabs,
  View,
  type TabItem,
} from '../../shared/ui';
import { AgentTab } from './AgentTab';
import { ApplyStatus } from './ApplyStatus';
import {
  isVersionConflict,
  telemetrySettingsQuery,
  useSaveTelemetrySettings,
} from './api/settings';
import {
  agents,
  applyChanges,
  changes,
  conflicts,
  denialCount,
  isAgentOn,
  type Change,
  type TelemetrySettings,
} from './model';
import { LeaveGuard } from './LeaveGuard';
import { SaveBar } from './SaveBar';
import { SharedTab } from './SharedTab';
import { Summary } from './Summary';
import type { SettingsTab } from './tabs';

type VersionedSettings = components['schemas']['VersionedTelemetrySettings'];

export function TelemetrySettingsPage() {
  return (
    <>
      <PageTitle>Что отправлять</PageTitle>
      <View>
        <Hint>
          По умолчанию собирается всё. Снимите отметку с того, чем не хотите делиться. Запреты
          действуют на всех ваших машинах
        </Hint>
        <Settings />
      </View>
    </>
  );
}

function Settings() {
  const settings = useQuery(telemetrySettingsQuery);

  if (settings.isPending) {
    return (
      <Panel>
        <Loading>Загружаем настройки…</Loading>
      </Panel>
    );
  }

  // A failed re-read keeps the data read before, and with it the form and its draft.
  if (settings.data === undefined) {
    return (
      <Panel>
        <LoadError
          error={settings.error}
          retrying={settings.isFetching}
          onRetry={() => {
            void settings.refetch();
          }}
        />
      </Panel>
    );
  }

  return (
    <SettingsForm
      loaded={settings.data}
      reload={async () => {
        const fresh = await settings.refetch();
        return fresh.isError ? undefined : fresh.data;
      }}
    />
  );
}

type SettingsFormProps = {
  loaded: VersionedSettings;
  reload: () => Promise<VersionedSettings | undefined>;
};

const tabsId = 'telemetry-settings';

// An agent's tab counts its denies, or says it is off; the shared tab counts denied folders.
// A tab with changes not saved yet shows a dot in place of the count.
function settingsTabs(settings: TelemetrySettings, pending: Change[]): TabItem<SettingsTab>[] {
  const count = (scope: SettingsTab) =>
    pending.some((change) => change.scope === scope)
      ? ('dot' as const)
      : denialCount(settings, scope) || undefined;
  return [
    ...agents.map(({ id, title }) =>
      isAgentOn(settings, id)
        ? { id, label: title, badge: count(id) }
        : {
            id,
            label: (
              <>
                {title} <span className="muted">выключен</span>
              </>
            ),
            badge: pending.some((change) => change.scope === id) ? ('dot' as const) : undefined,
          },
    ),
    { id: 'shared', label: 'Папки и история', badge: count('shared') },
  ];
}

// One draft for every tab: switching tabs keeps the changes.
// The draft keeps the version it was read at: a background refetch never moves it, so a
// save made over someone else's changes gets its 409.
// The base is the settings last read or saved: the save bar shows the draft's changes to it
// and «Отменить изменения» returns to it.
// After a 409 the local changes are laid over the fresh settings; this is what the page says.
type Merge = { version: number; overlaps: Change[] };

function SettingsForm({ loaded, reload }: SettingsFormProps) {
  const save = useSaveTelemetrySettings();
  const [base, setBase] = useState(loaded);
  const [draft, setDraft] = useState(loaded);
  const [reloading, setReloading] = useState(false);
  const [merge, setMerge] = useState<Merge>();
  // Kept apart from the save's error: an edit resets that, and the failure must stay told.
  const [rereadFailed, setRereadFailed] = useState(false);
  const draftRef = useRef(draft);
  useEffect(() => {
    draftRef.current = draft;
  }, [draft]);
  const settings = draft.settings;
  const pending = changes(base.settings, settings);

  const reset = (next: VersionedSettings) => {
    setBase(next);
    setDraft(next);
  };

  const change = (next: TelemetrySettings) => {
    setDraft({ ...draft, settings: next });
    if (!save.isIdle) {
      save.reset();
    }
  };

  // A 409: the settings were saved elsewhere. The fresh ones become the base and the local
  // changes are laid over them, the local change winning where both changed a point; the
  // user saves again with the button.
  const mergeFresh = async () => {
    setReloading(true);
    try {
      const fresh = await reload();
      setRereadFailed(!fresh);
      if (fresh) {
        // The draft as it is now: edits made while the settings were re-read count too.
        const ours = changes(base.settings, draftRef.current.settings);
        setBase(fresh);
        setDraft({ ...fresh, settings: applyChanges(fresh.settings, ours) });
        setMerge({
          version: fresh.version,
          overlaps: conflicts(base.settings, fresh.settings, ours),
        });
        save.reset();
      }
    } finally {
      setReloading(false);
    }
  };

  const conflict = isVersionConflict(save.error);
  // Claude Code opens when the URL names no tab.
  const { tab = 'claude' } = useSearch({ from: '/shell/telemetry' });
  const agent = agents.find((item) => item.id === tab);
  const navigate = useNavigate();

  return (
    <form
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(
          { settings, expectedVersion: draft.version },
          {
            onSuccess: (saved) => {
              reset(saved);
              setMerge(undefined);
            },
            onError: (error) => {
              if (isVersionConflict(error)) void mergeFresh();
            },
          },
        );
      }}
    >
      <View>
        <Hint>Версия настроек: {draft.version}</Hint>
        {save.isSuccess && (
          <Notice tone="ok">
            Сохранено. Изменения нативного OTel вступят в силу в новых сессиях агентов: у Claude
            Code — в новой сессии, у Codex — после перезапуска
          </Notice>
        )}
        {/* With nothing left to save the bar is gone, so the merge is told here. */}
        {merge && pending.length === 0 && <MergeNotice merge={merge} />}
        <Summary settings={base.settings}>
          {base.version > 0 && <ApplyStatus saved={base.version} />}
        </Summary>
        <Tabs
          id={tabsId}
          label="Разделы настроек"
          tabs={settingsTabs(settings, pending)}
          value={tab}
          onChange={(next) => {
            void navigate({ to: '/telemetry', search: { tab: next }, replace: true });
          }}
        />
        <TabPanel tabsId={tabsId} tab={tab}>
          {agent ? (
            <AgentTab agent={agent} settings={settings} onChange={change} />
          ) : (
            <SharedTab settings={settings} saved={base.settings} onChange={change} />
          )}
        </TabPanel>
        <LeaveGuard dirty={pending.length > 0} />
        <SaveBar
          changes={pending}
          busy={save.isPending || reloading}
          onCancel={() => {
            setDraft(base);
            setMerge(undefined);
            save.reset();
          }}
        >
          {merge && <MergeNotice merge={merge} />}
          {rereadFailed && !reloading && (
            <Notice tone="warn">
              Настройки изменились в другом окне, а перечитать их не удалось{' '}
              <Button
                size="sm"
                onClick={() => {
                  void mergeFresh();
                }}
              >
                Повторить
              </Button>
            </Notice>
          )}
          {save.error && !conflict && <Notice tone="err">{save.error.message}</Notice>}
        </SaveBar>
      </View>
    </form>
  );
}

function MergeNotice({ merge }: { merge: Merge }) {
  return (
    <Notice tone="warn">
      Настройки изменились в другом окне (v{merge.version}). Ваши правки наложены поверх
      {merge.overlaps.length > 0 &&
        `. Пересекаются: ${merge.overlaps.map((change) => change.text).join('; ')}`}
    </Notice>
  );
}
