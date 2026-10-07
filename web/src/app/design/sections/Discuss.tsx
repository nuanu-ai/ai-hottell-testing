import { DiscussActions, Grid, Panel, type DiscussAction } from '../../../shared/ui';

// Demo texts only: the real reply and command are composed in E4.
function actions(subject: string, labels: [string, string]): DiscussAction[] {
  return [
    {
      agent: 'codex',
      label: labels[0],
      primary: true,
      reply: `$demo-coach ${subject}, за 7 дней`,
      command: `codex '$demo-coach ${subject}, за 7 дней'`,
    },
    {
      agent: 'claude',
      label: labels[1],
      reply: `/demo-coach ${subject}, за 7 дней`,
      command: `claude '/demo-coach ${subject}, за 7 дней'`,
    },
  ];
}

export function DiscussSection() {
  return (
    <Grid>
      <Panel span={7} title="Обсудить в агенте" sub="DiscussActions · size=lg">
        <DiscussActions
          size="lg"
          actions={actions('обзор', ['Обсудить в Codex', 'Обсудить в Claude Code'])}
        />
      </Panel>
      <Panel span={5} title="Обсудить тему" sub="DiscussActions · size=sm">
        <DiscussActions
          size="sm"
          actions={actions('topic-demo-with-a-long-name', ['Обсудить в Codex', 'в Claude Code'])}
        />
      </Panel>
    </Grid>
  );
}
