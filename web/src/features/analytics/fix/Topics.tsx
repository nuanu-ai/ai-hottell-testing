import { useNavigate, useSearch } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';

import type { components } from '../../../shared/api';
import { discussActions } from '../../../shared/lib/discuss';
import { Button, DiscussActions, SectionHead, TopicCard } from '../../../shared/ui';
import { useHideTopic, useRestoreTopics } from '../api/hiddenTopics';
import { kindLabels, labelOf } from '../model/labels';
import type { Sample, Topic } from '../model/sample';
import { TopicEvidence, TopicState } from './TopicDetails';
import { topicPrice, topicWhy } from './topicPrice';

type Session = components['schemas']['AnalyticsSession'];

// The topics shown before «Ещё N».
export const FIRST_TOPICS = 3;
// How long a changed card keeps its accent bar.
const FLASH_MS = 1200;

type TopicsProps = {
  topics: readonly Topic[];
  X: Sample;
  sessions: readonly Session[];
  /** How many findings the person hid. */
  hidden: number;
  more: boolean;
  onMore: (more: boolean) => void;
  /** The period of the coach's reply; null for the whole period. */
  days: number | null;
  /** «Обсудить» only on one's own data. */
  canDiscuss: boolean;
};

/** The ids of the topics whose finding changed since the last data. */
function useFlashes(topics: readonly Topic[]): ReadonlySet<string> {
  const seen = useRef<Map<string, string> | null>(null);
  const [flash, setFlash] = useState<ReadonlySet<string>>(new Set());
  useEffect(() => {
    const now = new Map(topics.map((t) => [t.f.id, JSON.stringify(t.f)]));
    const before = seen.current;
    seen.current = now;
    if (!before) return;
    const changed = [...now].filter(([id, sig]) => before.has(id) && before.get(id) !== sig);
    if (changed.length === 0) return;
    setFlash(new Set(changed.map(([id]) => id)));
    const timer = setTimeout(() => {
      setFlash(new Set());
    }, FLASH_MS);
    return () => {
      clearTimeout(timer);
    };
  }, [topics]);
  return flash;
}

// «Темы» of «Что исправить» (README v5.1 «4. Темы»): bad → warn → info, then by episodes; up to 3,
// the rest behind «Ещё N». ?open=<id> opens a topic and brings it into view.
export function Topics({
  topics,
  X,
  sessions,
  hidden,
  more,
  onMore,
  days,
  canDiscuss,
}: TopicsProps) {
  const search: { open?: string } = useSearch({ strict: false });
  const navigate = useNavigate();
  const hide = useHideTopic();
  const restore = useRestoreTopics();
  const flash = useFlashes(topics);
  const openId = search.open;
  const openIndex = topics.findIndex((t) => t.f.id === openId);
  const showAll = more || openIndex >= FIRST_TOPICS;
  const shown = showAll ? topics : topics.slice(0, FIRST_TOPICS);

  useEffect(() => {
    if (openId) {
      document.getElementById(`topic-${openId}`)?.scrollIntoView({ block: 'start' });
    }
    // Only when a link brings a topic in, not on every data refresh.
  }, [openId]);

  const setOpen = (id: string | undefined) => {
    void navigate({
      to: '.',
      search: (prev: Record<string, unknown>) => ({ ...prev, open: id }),
      replace: true,
    });
  };

  return (
    <>
      <SectionHead
        title="Темы"
        sub={topics.length > 0 ? 'сначала важные, затем по числу эпизодов' : undefined}
        action={
          hidden > 0 ? (
            <Button
              variant="link"
              onClick={() => {
                restore.mutate(undefined);
              }}
            >
              скрыто {hidden} — вернуть
            </Button>
          ) : undefined
        }
      />
      <div className="topics">
        {shown.length === 0 && <div className="p muted">По выбранным сессиям тем нет.</div>}
        {shown.map((t) => (
          <TopicCard
            key={t.f.id}
            id={t.f.id}
            sev={t.f.sev}
            kind={labelOf(kindLabels, t.f.kind)}
            title={t.f.title}
            price={topicPrice(t)}
            why={topicWhy(t, X, sessions)}
            flash={flash.has(t.f.id)}
            left={<TopicEvidence f={t.f} ids={X.ids} />}
            right={<TopicState f={t.f} />}
            open={openId === t.f.id}
            onToggle={() => {
              setOpen(openId === t.f.id ? undefined : t.f.id);
            }}
            actions={
              <>
                {canDiscuss && (
                  <DiscussActions
                    size="sm"
                    actions={discussActions({ mode: 'coach', subject: t.f.id, days })}
                  />
                )}
                <Button
                  size="sm"
                  className="quiet"
                  onClick={() => {
                    hide.mutate(t.f.id);
                  }}
                >
                  Не проблема
                </Button>
              </>
            }
          />
        ))}
        {topics.length > FIRST_TOPICS && (
          <Button
            variant="link"
            onClick={() => {
              onMore(!showAll);
              if (showAll && openIndex >= FIRST_TOPICS) setOpen(undefined);
            }}
          >
            {showAll
              ? `Свернуть до ${String(FIRST_TOPICS)}`
              : `Ещё ${String(topics.length - FIRST_TOPICS)}`}
          </Button>
        )}
      </div>
    </>
  );
}
