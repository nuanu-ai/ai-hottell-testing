import { authors } from './authors';

export function StoryPage() {
  return (
    <section className="panel">
      <h1>Hottell</h1>
      <p>Телеметрия и разбор работы с Claude Code и Codex на собственном сервере команды.</p>
      <p>Авторы: {authors.join(', ')}.</p>
      <p>
        Исходники, самостоятельная установка и лицензия MIT — в{' '}
        <a href="https://github.com/nuanu-ai/ai-hottell-testing" target="_blank" rel="noreferrer">
          публичном репозитории
        </a>.
      </p>
    </section>
  );
}
