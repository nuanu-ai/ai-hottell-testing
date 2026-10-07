// The skill opportunities report (pkg/deepv2 ValidateSkillReport, v2_analysis.md) read for the
// «Skills» screen. The report arrives as a free-form object, so it is read defensively.

import type { TagTone } from '../../../shared/ui';
import { linesOf } from './model';

export type OpportunityKind = 'use_existing' | 'create' | 'install_candidate';

export const KIND_LABELS: Record<OpportunityKind, readonly [string, TagTone]> = {
  use_existing: ['использовать установленный', 'ok'],
  create: ['создать свой', 'acc'],
  install_candidate: ['установить внешний', 'warn'],
};

const READINESS: Record<string, string> = {
  hypothesis: 'гипотеза',
  candidate: 'кандидат',
  prepared: 'подготовлено',
};

export type OpportunitySource = { sessionId: string; taskId: string; lines: number[] };

export type Opportunity = {
  id: string;
  kind: string;
  title: string;
  recommendation: string;
  whySkill: string;
  alternative: string;
  uncertainty: string;
  verification: string;
  readiness: string;
  sources: OpportunitySource[];
  installedSkill: string;
  copyPrompt: string;
  external: { name: string; url: string; reviewRequired: boolean } | null;
};

type Obj = Record<string, unknown>;

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function objects(v: unknown): Obj[] {
  return Array.isArray(v)
    ? v.filter((x): x is Obj => typeof x === 'object' && x !== null && !Array.isArray(x))
    : [];
}

export function readinessLabel(readiness: string): string {
  return READINESS[readiness] ?? readiness;
}

export function kindLabel(kind: string): readonly [string, TagTone] {
  return (
    (KIND_LABELS as Record<string, readonly [string, TagTone] | undefined>)[kind] ?? [kind, 'plain']
  );
}

export function opportunitiesOf(report: Obj | undefined): Opportunity[] {
  if (!report) return [];
  return objects(report.opportunities).map((o) => {
    const ext =
      typeof o.external_skill === 'object' && o.external_skill !== null
        ? (o.external_skill as Obj)
        : null;
    return {
      id: str(o.id),
      kind: str(o.kind),
      title: str(o.title),
      recommendation: str(o.recommendation),
      whySkill: str(o.why_skill),
      alternative: str(o.alternative),
      uncertainty: str(o.uncertainty),
      verification: str(o.verification),
      readiness: str(o.readiness),
      sources: objects(o.sources).map((s) => ({
        sessionId: str(s.session_id),
        taskId: str(s.task_id),
        lines: linesOf(s.evidence),
      })),
      installedSkill: str(o.installed_skill),
      copyPrompt: str(o.copy_prompt),
      external: ext
        ? {
            name: str(ext.name),
            url: str(ext.url),
            reviewRequired: ext.review_status === 'source_review_required',
          }
        : null,
    };
  });
}
