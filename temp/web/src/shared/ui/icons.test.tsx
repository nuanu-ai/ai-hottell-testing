import { render } from '@testing-library/react';

import { Collapse } from './icons';

describe('Collapse', () => {
  it.each([
    ['left', 'M15 6l-6 6 6 6'],
    ['right', 'M9 6l6 6-6 6'],
  ] as const)('points %s', (direction, d) => {
    const { container } = render(<Collapse direction={direction} />);

    expect(container.querySelector('path')).toHaveAttribute('d', d);
  });
});
