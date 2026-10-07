import { Panel } from '../../shared/ui';
import { FoldersCard } from './FoldersCard';
import { setBackfill, type TelemetrySettings } from './model';
import { Toggle } from './Toggle';

type SharedTabProps = {
  settings: TelemetrySettings;
  // The settings last read or saved: a folder pattern not in them is marked as not saved.
  saved: TelemetrySettings;
  onChange: (next: TelemetrySettings) => void;
};

// The folders and the history are shared by both agents, so they get a tab of their own.
export function SharedTab({ settings, saved, onChange }: SharedTabProps) {
  return (
    <>
      <FoldersCard settings={settings} saved={saved} onChange={onChange} />
      <Panel title="История">
        <Toggle
          label="Догрузить историю"
          checked={settings.backfill_history}
          // The flag stays on, yet the binary sends the history only once (settings.md kind 7).
          caption="однократно, при включении"
          note={{
            text: 'Один раз отправить транскрипты, которые были до установки бинаря. К истории применяются запреты агента, транскриптов и папок: при выключенных транскриптах она не отправляется',
          }}
          onChange={(on) => {
            onChange(setBackfill(settings, on));
          }}
        />
      </Panel>
    </>
  );
}
