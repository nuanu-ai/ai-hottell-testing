import { fireEvent, render, screen } from '@testing-library/react';

import { Table, TableRow, Td } from './Table';

function renderRow(onNavigate: (href: string) => void) {
  render(
    <Table>
      <tbody>
        <TableRow href="/stack/a" onNavigate={onNavigate}>
          <Td cellTitle>
            <a
              href="/stack/a"
              onClick={(event) => {
                event.preventDefault();
              }}
            >
              стек a
            </a>
          </Td>
          <Td>прочее</Td>
          <Td>
            <button type="button">действие</button>
          </Td>
        </TableRow>
      </tbody>
    </Table>,
  );
}

describe('TableRow', () => {
  afterEach(() => {
    window.getSelection()?.removeAllRanges();
  });

  it('navigates to its href when a plain cell is clicked', () => {
    const onNavigate = vi.fn();
    renderRow(onNavigate);

    fireEvent.click(screen.getByText('прочее'));

    expect(onNavigate).toHaveBeenCalledExactlyOnceWith('/stack/a');
  });

  it.each(['стек a', 'действие'])('leaves a click on "%s" to that control', (text) => {
    const onNavigate = vi.fn();
    renderRow(onNavigate);

    fireEvent.click(screen.getByText(text));

    expect(onNavigate).not.toHaveBeenCalled();
  });

  it('does not navigate while text is selected', () => {
    const onNavigate = vi.fn();
    renderRow(onNavigate);
    const cell = screen.getByText('прочее');
    window.getSelection()?.selectAllChildren(cell);

    fireEvent.click(cell);

    expect(onNavigate).not.toHaveBeenCalled();
  });
});
