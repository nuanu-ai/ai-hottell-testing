import { render } from '@testing-library/react';
import type { ReactElement } from 'react';

import {
  Badge,
  Button,
  Card,
  Chip,
  CopyField,
  Dialog,
  Empty,
  Hint,
  Notice,
  PageTitle,
  PasswordInput,
  Row,
  Table,
  TableRow,
  Td,
  TextInput,
  Th,
} from './index';

// Each snapshot is the markup of deploy's templates (https://git.alva.dev/alva/deploy,
// commit 31e42ec): the components add no classes of their own beyond app.css.
function markup(element: ReactElement): string {
  return render(element).container.innerHTML;
}

describe('shared/ui markup', () => {
  it('Button', () => {
    expect(markup(<Button>Искать</Button>)).toMatchInlineSnapshot(
      `"<button type="button" class="btn">Искать</button>"`,
    );
    expect(
      markup(
        <Button variant="primary" size="sm" type="submit">
          Выпустить
        </Button>,
      ),
    ).toMatchInlineSnapshot(
      `"<button type="submit" class="btn btn-primary btn-sm">Выпустить</button>"`,
    );
    expect(
      markup(
        <Button variant="link" disabled>
          Сбросить
        </Button>,
      ),
    ).toMatchInlineSnapshot(
      `"<button type="button" class="btn btn-link" disabled="">Сбросить</button>"`,
    );
  });

  it('Card and PageTitle', () => {
    expect(
      markup(
        <Card>
          <PageTitle>Всё на месте</PageTitle>
        </Card>,
      ),
    ).toMatchInlineSnapshot(`"<div class="card"><span class="крупно">Всё на месте</span></div>"`);
  });

  it('Badge', () => {
    expect(markup(<Badge tone="ok">да</Badge>)).toMatchInlineSnapshot(
      `"<span class="badge badge-ok">да</span>"`,
    );
    expect(markup(<Badge tone="warn">можно отправить</Badge>)).toMatchInlineSnapshot(
      `"<span class="badge badge-warn">можно отправить</span>"`,
    );
    expect(
      markup(
        <Badge tone="err" title="причина">
          нет
        </Badge>,
      ),
    ).toMatchInlineSnapshot(`"<span class="badge badge-err" title="причина">нет</span>"`);
    expect(markup(<Badge tone="quiet">агент</Badge>)).toMatchInlineSnapshot(
      `"<span class="badge badge-quiet">агент</span>"`,
    );
  });

  it('Chip', () => {
    expect(
      markup(
        <Chip on count={12}>
          все
        </Chip>,
      ),
    ).toMatchInlineSnapshot(
      `"<button type="button" class="chip on">все <span class="count">12</span></button>"`,
    );
    expect(markup(<Chip>разошлись с git</Chip>)).toMatchInlineSnapshot(
      `"<button type="button" class="chip">разошлись с git</button>"`,
    );
  });

  it('Table', () => {
    expect(
      markup(
        <Table>
          <thead>
            <TableRow>
              <Th>стек</Th>
              <Th nowrap>частей</Th>
            </TableRow>
          </thead>
          <tbody>
            <TableRow href="/stack/a">
              <Td cellTitle>
                <a href="/stack/a">a</a>
              </Td>
              <Td nowrap>3</Td>
            </TableRow>
          </tbody>
        </Table>,
      ),
    ).toMatchInlineSnapshot(
      `"<div class="table-wrap"><table class="table"><thead><tr><th>стек</th><th class="nowrap">частей</th></tr></thead><tbody><tr class="clickable" data-href="/stack/a"><td class="cell-title"><a href="/stack/a">a</a></td><td class="nowrap">3</td></tr></tbody></table></div>"`,
    );
  });

  it('Notice', () => {
    expect(markup(<Notice tone="err">Сбой</Notice>)).toMatchInlineSnapshot(
      `"<div class="notice notice-err" role="alert">Сбой</div>"`,
    );
    expect(markup(<Notice tone="ok">Готово</Notice>)).toMatchInlineSnapshot(
      `"<div class="notice notice-ok">Готово</div>"`,
    );
    expect(markup(<Notice tone="info">Сведения</Notice>)).toMatchInlineSnapshot(
      `"<div class="notice notice-info">Сведения</div>"`,
    );
    expect(markup(<Notice tone="warn">Внимание</Notice>)).toMatchInlineSnapshot(
      `"<div class="notice notice-warn">Внимание</div>"`,
    );
  });

  it('Empty', () => {
    expect(
      markup(<Empty title="Ничего не найдено">Попробуйте снять отбор.</Empty>),
    ).toMatchInlineSnapshot(
      `"<div class="empty"><h3>Ничего не найдено</h3>Попробуйте снять отбор.</div>"`,
    );
  });

  it('TextInput and PasswordInput', () => {
    expect(markup(<TextInput id="who" label="Кто вы" name="who" />)).toMatchInlineSnapshot(
      `"<div class="field"><label for="who">Кто вы</label><input id="who" type="text" name="who"></div>"`,
    );
    expect(
      markup(<PasswordInput id="key" label="Ключ" error="Неверный ключ" />),
    ).toMatchInlineSnapshot(
      `"<div class="field"><label for="key">Ключ</label><input id="key" aria-invalid="true" aria-describedby="key-error" type="password"><div class="hint hint-err" id="key-error">Неверный ключ</div></div>"`,
    );
  });

  it('Hint and Row', () => {
    expect(markup(<Hint>Подсказка</Hint>)).toMatchInlineSnapshot(`"<p class="hint">Подсказка</p>"`);
    expect(
      markup(
        <Row tight>
          <Button>Да</Button>
        </Row>,
      ),
    ).toMatchInlineSnapshot(
      `"<div class="row tight"><button type="button" class="btn">Да</button></div>"`,
    );
  });

  it('Dialog', () => {
    expect(markup(<Dialog ref={null}>Точно?</Dialog>)).toMatchInlineSnapshot(
      `"<dialog class="окно">Точно?</dialog>"`,
    );
  });

  it('CopyField', () => {
    expect(markup(<CopyField value="value-to-copy" />)).toMatchInlineSnapshot(
      `"<div class="код"><code class="mono">value-to-copy</code><button type="button" class="btn btn-sm">Скопировать</button></div>"`,
    );
  });
});
