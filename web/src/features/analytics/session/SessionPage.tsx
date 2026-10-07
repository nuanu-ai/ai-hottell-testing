import type { ReactNode } from 'react';

import type { components } from '../../../shared/api';

import type { Whose } from '../model/filters';
import { SessionHead } from './SessionHead';

type Session = components['schemas']['AnalyticsSession'];

export type SessionPageProps = {
  // The session from the page's dataset; undefined when the dataset has no such session.
  session: Session | undefined;
  onBack: () => void;
  discussSlot?: ReactNode;
  // Whose the session is, by its owner (HT-444).
  whose?: Whose;
  children?: ReactNode;
};

// A session's feed (README v5.1 «Лента сессии»): «← Сессии», the head, then the feed panels.
export function SessionPage({ session, onBack, discussSlot, whose, children }: SessionPageProps) {
  const back = (
    <div>
      <button type="button" className="link" onClick={onBack}>
        ← Сессии
      </button>
    </div>
  );
  if (!session) {
    return (
      <div className="sess">
        {back}
        <div className="state">
          Сессии нет в датасете страницы — её сводки нет, ниже лента из транскрипта.
        </div>
        {children}
      </div>
    );
  }
  return (
    <div className="sess">
      {back}
      <SessionHead session={session} discussSlot={discussSlot} whose={whose} />
      {children}
    </div>
  );
}
