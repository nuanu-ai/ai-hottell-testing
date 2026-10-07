import type { components } from '../../../shared/api';

// The words of a finding's fields (the reference dictionaries, index.html:L472–L476).

type Finding = components['schemas']['AnalyticsFinding'];

export const kindLabels: Record<Finding['kind'], string> = {
  personalization: 'персонализация',
  project_rule: 'правило проекта',
  skill: 'skill',
  hook: 'проверка · hook',
  script: 'скрипт',
  automation: 'автоматизация',
  diagnostic: 'диагностика',
  habit: 'привычка',
};

export const readinessLabels: Record<Finding['readiness'], string> = {
  hypothesis: 'гипотеза',
  needs_spec: 'нужна спецификация',
  prepared: 'готово к применению',
};

export const decisionLabels: Record<Finding['decision'], string> = {
  not_requested: 'не запрошено',
  accepted: 'принято',
  rejected: 'отклонено',
  revision_requested: 'на доработке',
};

export const executionLabels: Record<Finding['execution'], string> = {
  not_applied: 'не применено',
  applied: 'применено',
};

export const effectLabels: Record<Finding['effect'], string> = {
  not_measured: 'не измерен',
  helped: 'помогло',
  no_effect: 'без эффекта',
  worse: 'стало хуже',
  insufficient_data: 'мало данных',
};

/** The word for a value, «—» for one the dictionary does not know. */
export function labelOf<K extends string>(labels: Record<K, string>, value: string | undefined) {
  return value !== undefined && value in labels ? labels[value as K] : '—';
}
