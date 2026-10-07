import { Fragment, useId } from 'react';

import {
  Actions,
  Button,
  Checkbox,
  Grid,
  Hint,
  Notice,
  Panel,
  Table,
  Tag,
  Td,
  Th,
  View,
  type TagTone,
} from '../../shared/ui';
import {
  agentOffEffect,
  applyTiming,
  contentCategories,
  contentSignalsOff,
  contentToneTitles,
  denyGaps,
  eventCount,
  eventNote,
  eventsByGroup,
  hookFields,
  isAgentOn,
  isContentLocked,
  isContentOn,
  isEventOn,
  isFieldOn,
  otherDeniedFields,
  requiredFields,
  isSourceOn,
  setAgent,
  setContent,
  setEvent,
  setEvents,
  setField,
  setSource,
  signalList,
  sourceGroups,
  sources,
  unhookedEvents,
  type AgentId,
  type HookEvent,
  type ContentItem,
  type ContentTone,
  type HookFieldItem,
  type SourceGroup,
  type SourceItem,
  type TelemetrySettings,
} from './model';

type AgentTabProps = {
  agent: { id: AgentId; title: string };
  settings: TelemetrySettings;
  onChange: (next: TelemetrySettings) => void;
};

// An agent that is off sends nothing, so its sections give way to one notice; they keep
// their changes and come back with the agent.
export function AgentTab({ agent, settings, onChange }: AgentTabProps) {
  const on = isAgentOn(settings, agent.id);

  return (
    <View>
      <Panel
        title={agent.title}
        sub={<Tag tone={on ? 'ok' : 'bad'}>{on ? 'отправляется' : 'выключен'}</Tag>}
        action={
          <Checkbox
            label={`Отправлять данные ${agent.title}`}
            checked={on}
            onChange={(event) => {
              onChange(setAgent(settings, agent.id, event.target.checked));
            }}
          />
        }
      >
        {on ? (
          <Hint>Выключенный агент выключен целиком: {agentOffEffect}</Hint>
        ) : (
          <Notice tone="warn">
            {agent.title} выключен: {agentOffEffect}. Настройки источников сохранятся и вернутся,
            когда вы его включите
          </Notice>
        )}
      </Panel>
      {on && (
        <>
          <Grid>
            {sourceGroups.map((group) => (
              <SourcePanel
                key={group.id}
                group={group}
                agent={agent}
                settings={settings}
                onChange={onChange}
              />
            ))}
          </Grid>
          <HookSections agent={agent} settings={settings} onChange={onChange} />
          <ContentPanel agent={agent} settings={settings} onChange={onChange} />
        </>
      )}
      {/* The timing stays while the agent is off: its native OTel goes on until the agent
          rereads its config. */}
      <Grid>
        {on && (
          <Panel title="Чего запреты не закрывают" span={6}>
            <ul className="notes-list stack g8">
              {denyGaps[agent.id].map((gap) => (
                <li key={gap}>{gap}</li>
              ))}
            </ul>
          </Panel>
        )}
        <Panel title="Когда вступит в силу" span={6}>
          <Table>
            <tbody>
              {applyTiming[agent.id].map((row) => (
                <tr key={row.what}>
                  <Td wrap>{row.what}</Td>
                  <Td wrap>{row.when}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Panel>
      </Grid>
    </View>
  );
}

const toneTags: Record<ContentTone, TagTone> = { ok: 'ok', warn: 'warn', off: 'bad', na: 'plain' };

// Each category with the agent's signals that carry it and how its deny works there.
function ContentPanel({ agent, settings, onChange }: AgentTabProps) {
  return (
    <Panel title="Содержимое нативного OTel">
      <Hint>
        Запрет убирает содержимое, но не сам сигнал: счётчики, длины и размеры продолжают уходить
      </Hint>
      <Table>
        <thead>
          <tr>
            <Th>Содержимое</Th>
            <Th>Где</Th>
            <Th numeric>Отправлять</Th>
          </tr>
        </thead>
        <tbody>
          {contentCategories.map((category) => (
            <ContentRow
              key={category.id}
              category={category}
              agent={agent}
              settings={settings}
              onChange={onChange}
            />
          ))}
        </tbody>
      </Table>
    </Panel>
  );
}

type ContentRowProps = AgentTabProps & { category: ContentItem };

// A deny the agent cannot act on keeps its row, with the saved value under a locked switch.
// A row whose signals are all off is muted: its deny is not needed while they are.
function ContentRow({ category, agent, settings, onChange }: ContentRowProps) {
  const noteId = useId();
  const offId = useId();
  const use = category.use[agent.id];
  const off = isContentLocked(use) ? undefined : contentSignalsOff(settings, agent.id, use);
  const described = [off && offId, use.note !== undefined && noteId].filter(Boolean).join(' ');

  return (
    <tr className={off ? 'muted' : undefined}>
      <Td wrap>
        {category.title} <Tag tone={toneTags[use.tone]}>{contentToneTitles[use.tone]}</Tag>
        {off && (
          <p id={offId} className="hint">
            Нативные {signalList(off)} выключены — не нужно
          </p>
        )}
        {use.note !== undefined && (
          <p id={noteId} className="hint">
            {use.note}
          </p>
        )}
      </Td>
      <Td wrap>{use.signals.length > 0 ? signalList(use.signals) : '—'}</Td>
      <Td numeric>
        <Checkbox
          label={<span className="sr-only">{category.title}</span>}
          checked={isContentOn(settings, agent.id, category.id)}
          disabled={isContentLocked(use)}
          aria-describedby={described || undefined}
          onChange={(event) => {
            onChange(setContent(settings, agent.id, category.id, event.target.checked));
          }}
        />
      </Td>
    </tr>
  );
}

// The fields the page lists, then any other denied name, so it can be seen and allowed.
function FieldsPanel({ agent, settings, onChange }: AgentTabProps) {
  const others = otherDeniedFields(settings, agent.id);

  return (
    <Panel title="Поля событий">
      <Hint>
        Снятая отметка вырезает поле верхнего уровня из всех событий, где оно есть; остальное
        событие уходит. Вложенные поля так не вырезаются. {requiredFields.join(', ')} запретить
        нельзя: без них запись не связать с сессией
      </Hint>
      <Table>
        <thead>
          <tr>
            <Th>Поле</Th>
            <Th>В событиях</Th>
            <Th numeric>Отправлять</Th>
          </tr>
        </thead>
        <tbody>
          {hookFields[agent.id].map((field) => (
            <FieldRow
              key={field.id}
              field={field}
              agent={agent}
              settings={settings}
              onChange={onChange}
            />
          ))}
          {others.length > 0 && (
            <tr>
              <Td wrap>
                Другие запрещённые поля
                <p className="hint">Их нет в списке выше; бинарь вырезает и их</p>
              </Td>
              <Td colSpan={2}>
                <ul role="list" aria-label="Другие запрещённые поля" className="bare-list stack g4">
                  {others.map((id) => (
                    <li key={id}>
                      <Actions>
                        <span className="mono">{id}</span>
                        <Button
                          size="sm"
                          aria-label={`Разрешить ${id}`}
                          onClick={() => {
                            onChange(setField(settings, agent.id, id, true));
                          }}
                        >
                          Разрешить
                        </Button>
                      </Actions>
                    </li>
                  ))}
                </ul>
              </Td>
            </tr>
          )}
        </tbody>
      </Table>
    </Panel>
  );
}

// «last_assistant_message» and «PostToolUseFailure» may break after a «_» and between the
// words of a name, so a narrow table wraps them at their joints rather than anywhere.
function breakable(text: string) {
  return text.split(/(?<=_)|(?<=[a-z])(?=[A-Z])/).map((part, index) => (
    <Fragment key={index}>
      {index > 0 && <wbr />}
      {part}
    </Fragment>
  ));
}

type EventSwitchProps = AgentTabProps & { event: HookEvent };

// An event's switch with the line under it saying when the agent sends it.
function EventSwitch({ agent, event, settings, onChange }: EventSwitchProps) {
  const noteId = useId();

  return (
    <div>
      <Checkbox
        className="mono"
        label={event}
        checked={isEventOn(settings, agent.id, event)}
        aria-describedby={noteId}
        onChange={(e) => {
          onChange(setEvent(settings, agent.id, event, e.target.checked));
        }}
      />
      <p id={noteId} className="check-note">
        {eventNote(agent.id, event)}
      </p>
    </div>
  );
}

type FieldRowProps = AgentTabProps & { field: HookFieldItem };

function FieldRow({ field, agent, settings, onChange }: FieldRowProps) {
  const eventsId = useId();

  return (
    <tr>
      <Td className="mono">{breakable(field.id)}</Td>
      <Td wrap id={eventsId}>
        {breakable(field.events)}
      </Td>
      <Td numeric>
        <Checkbox
          label={<span className="sr-only">{field.id}</span>}
          checked={isFieldOn(settings, agent.id, field.id)}
          aria-describedby={eventsId}
          onChange={(event) => {
            onChange(setField(settings, agent.id, field.id, event.target.checked));
          }}
        />
      </Td>
    </tr>
  );
}

type SourcePanelProps = AgentTabProps & { group: { id: SourceGroup; title: string } };

// Half the width on a wide screen; the grid stacks the two panels on a narrow one.
function SourcePanel({ group, agent, settings, onChange }: SourcePanelProps) {
  return (
    <Panel title={group.title} span={6}>
      <Table>
        <thead>
          <tr>
            <Th>Источник</Th>
            <Th>Где</Th>
            <Th numeric>Отправлять</Th>
          </tr>
        </thead>
        <tbody>
          {sources
            .filter((source) => source.group === group.id)
            .map((source) => (
              <SourceRow
                key={source.id}
                source={source}
                agent={agent}
                settings={settings}
                onChange={onChange}
              />
            ))}
        </tbody>
      </Table>
    </Panel>
  );
}

type SourceRowProps = AgentTabProps & { source: SourceItem };

function SourceRow({ source, agent, settings, onChange }: SourceRowProps) {
  const noteId = useId();
  const note = source.notes[agent.id];

  return (
    <tr>
      <Td wrap>
        {source.title}
        {note?.tag !== undefined && (
          <>
            {' '}
            <Tag tone={note.tone === 'warn' ? 'warn' : 'plain'}>{note.tag}</Tag>
          </>
        )}
        {note && (
          <p id={noteId} className="hint">
            {note.text}
          </p>
        )}
      </Td>
      <Td className="mono break">{source.keys[agent.id]}</Td>
      <Td numeric>
        <Checkbox
          label={<span className="sr-only">{source.title}</span>}
          checked={isSourceOn(settings, agent.id, source.id)}
          aria-describedby={note ? noteId : undefined}
          onChange={(event) => {
            onChange(setSource(settings, agent.id, source.id, event.target.checked));
          }}
        />
      </Td>
    </tr>
  );
}

// Hook events and fields are cut from the hooks source only: while it is off, their panels
// give way to one line, and they keep their changes.
function HookSections({ agent, settings, onChange }: AgentTabProps) {
  if (!isSourceOn(settings, agent.id, 'hooks')) {
    return (
      <Panel title="События хуков">
        <Hint>Источник «События хуков» выключен: не отправляются ни события, ни их поля</Hint>
      </Panel>
    );
  }

  const { sent, total } = eventCount(settings, agent.id);
  const unhooked = unhookedEvents[agent.id];

  return (
    <>
      <Panel
        title="События хуков"
        sub={`уходит ${String(sent)} из ${String(total)}`}
        action={
          <Actions>
            <Button
              size="sm"
              disabled={sent === total}
              onClick={() => {
                onChange(setEvents(settings, agent.id, true));
              }}
            >
              Все
            </Button>
            <Button
              size="sm"
              disabled={sent === 0}
              onClick={() => {
                onChange(setEvents(settings, agent.id, false));
              }}
            >
              Ничего
            </Button>
          </Actions>
        }
      >
        <Hint>Снимите отметку с события, чтобы бинарь его не отправлял</Hint>
        <div role="group" aria-label={`События хуков ${agent.title}`} className="stack">
          {eventsByGroup(agent.id).map(({ group, events }) => (
            <div key={group.id} role="group" aria-labelledby={`${agent.id}-events-${group.id}`}>
              <h3 id={`${agent.id}-events-${group.id}`}>{group.title}</h3>
              <div className="check-grid">
                {events.map((event) => (
                  <EventSwitch
                    key={event}
                    agent={agent}
                    event={event}
                    settings={settings}
                    onChange={onChange}
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
        {unhooked.length > 0 && (
          <Hint>
            {unhooked.join(' и ')} не отправляются никогда: хук на них заменил бы создание и
            удаление worktree
          </Hint>
        )}
      </Panel>
      <FieldsPanel agent={agent} settings={settings} onChange={onChange} />
    </>
  );
}
