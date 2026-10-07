import { useState } from 'react';

import {
  Actions,
  Button,
  Checkbox,
  EmailInput,
  Panel,
  PasswordInput,
  Segmented,
  Tag,
  TextInput,
  Stack,
} from '../../../shared/ui';

const PERIODS = [
  { value: '7', label: '7 дн' },
  { value: '14', label: '14 дн' },
  { value: '30', label: '30 дн' },
  { value: 'all', label: 'Всё' },
] as const;

const AGENTS = [
  { value: 'all', label: 'Все' },
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
] as const;

export function ControlsSection() {
  const [period, setPeriod] = useState<(typeof PERIODS)[number]['value']>('all');
  const [agent, setAgent] = useState<(typeof AGENTS)[number]['value']>('all');
  const [checked, setChecked] = useState(true);

  return (
    <Panel title="Кнопки, метки, поля" sub="Button · Tag · Segmented · поля · Checkbox">
      <Actions>
        <Button>Обычная</Button>
        <Button variant="primary">Основная</Button>
        <Button size="sm">Пересобрать</Button>
        <Button disabled>Недоступна</Button>
        <Button variant="link">ссылка →</Button>
      </Actions>
      <Actions>
        <Tag tone="claude">Claude Code</Tag>
        <Tag tone="codex">CX</Tag>
        <Tag tone="ok">записано</Tag>
        <Tag tone="warn">Холодный кэш</Tag>
        <Tag tone="bad">ошибка</Tag>
        <Tag tone="acc">гипотеза</Tag>
        <Tag tone="plain">нет</Tag>
      </Actions>
      <Actions>
        <Segmented label="Период" options={PERIODS} value={period} onChange={setPeriod} />
        <Segmented label="Агент" options={AGENTS} value={agent} onChange={setAgent} />
        <select aria-label="Проект" defaultValue="all">
          <option value="all">Все проекты</option>
          <option value="demo">claude-demo</option>
        </select>
      </Actions>
      <Stack className="narrow">
        <TextInput label="Название" placeholder="MacBook" />
        <EmailInput label="Email" error="Нужен адрес вида name@example.com" />
        <PasswordInput label="Пароль" disabled />
        <div className="field">
          <label htmlFor="design-note">Заметка</label>
          <textarea id="design-note" rows={3} defaultValue="Многострочное поле" />
        </div>
        <Checkbox
          label="Отправлять транскрипты"
          checked={checked}
          onChange={(event) => {
            setChecked(event.target.checked);
          }}
        />
        <p className="check-note">Пояснение под флажком</p>
      </Stack>
    </Panel>
  );
}
