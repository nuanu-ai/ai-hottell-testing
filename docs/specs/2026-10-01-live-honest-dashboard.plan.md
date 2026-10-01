# Живой дашборд: честные цифры и выводы без завышенной достоверности — план реализации

> **Для исполнителя (Claude):** обязательный навык — superpowers:executing-plans (или superpowers:subagent-driven-development в этой же сессии). Задачи выполняются по порядку: почти все трогают одни и те же файлы.

**Цель.** Живая версия дашборда (http://127.0.0.1:8801, «Новые сессии · Hooks + OTel») перестаёт показывать неверные цифры и вредный совет, отделяет здоровье сбора данных от выводов о работе, скрывает только сессии явного служебного происхождения, а детекторы перестают выдавать гипотезы за установленные причины.

**Архитектура.** Всё считается в сборщике `ui/builder/live.py` (только stdlib, без модели) по событиям Hooks/OTel из ClickHouse. Интерфейс `ui/static/index.html` только показывает и пересчитывает под фильтр. Новые поля датасета добавляются обратно совместимо: версия «Разобранные сессии» (:8800, `build.py`) их не пишет, и интерфейс без них ведёт себя как раньше.

**Принцип (план 2.0, `docs/plan/hottell-2.0.md`, «Цель»).** Карточка опирается на проверенную причину; её оформление не повышает достоверность гипотезы. Поэтому все карточки детекторов в живой версии остаются гипотезами: у каждой есть «что проверить до правки», правка — только кандидат.

**Стек.** Python 3 stdlib + unittest; одна HTML-страница с ванильным JS; Go — только пример хуков в `hottell/`.

---

## Изменения после ревью (2026-10-01)

| Замечание | Проверка | Что изменено |
|---|---|---|
| 1. «Ожидание» названо простоем, интервалы складываются | Подтверждено на данных: все 8 вопросов — `request_user_input_async`; за «258 мин ожидания» агент сделал 1072 вызова (669 — основной агент). Ожиданий, когда основной агент действительно стоял, — 0 | Карточка «не останавливать работу» удалена. Ожидание = только когда основной агент после вопроса ничего не делал до ответа; интервалы объединяются (T6) |
| 2. Советы слишком рано «готовы к применению» | Согласен; это требование плана 2.0 | Все карточки детекторов — `hypothesis` с «Что проверить до правки»; проверка пользы — на сопоставимых задачах, не числом сжатий; правило про вопросы удалено (T8) |
| 3. Фильтр прячет рабочие сессии | Согласен: правило «одна реплика, ≤ 2 вызовов» ловит обычные запросы, отсутствие реплик может быть неполным сбором | Скрываются только `system` — явные служебные задачи Codex. Правила `probe` и «без реплик» удалены. Расписание (`<heartbeat>`) видно с пометкой (T2) |
| 4. «То же содержимое» не проверяется | Подтверждено: 512 символов префикса и чтения без результата давали эпизод | Сравнивается хэш всего ответа (`cityHash64` в ClickHouse, проверен запросом); чтения без результата и ответы на несколько файлов не участвуют (T4) |
| 5. Потери PostToolUse исчезают навсегда | Подтверждено: льгота считалась от последнего вызова сессии | Льгота 2 мин — от момента сборки; вызовы в конце сессии считаются и называются отдельно (T7) |
| 6. Цифры карточек и стоимость под фильтром | Согласен; «≥ $» терял «≈» и не является доказанной нижней границей | Карточки несут вклад каждой сессии и пересчитываются под фильтр; стоимость — наблюдаемая оценка «≈» с долей сессий, где она есть (T11, T12) |
| 7. Тесты закрепляют правила, а не смысл | Согласен | В каждой задаче — контрпримеры из ревью; проверка в T14 — инварианты на обеих версиях, без заранее выбранных выводов |
| `async` и Codex 0.159 | Не доказано: здесь стоит codex-cli 0.135.0, документация Codex описывает `async` | T1 обоснован только расхождением примера с установщиком; причину из d192b63 не утверждаем |
| Цифры не воспроизводимы | ClickHouse стенда был остановлен; поднят и проверен | Добавлено приложение с запросами и фиксированным окном `--until 2026-10-01T02:00:00Z` |
| PR #11 слит | Подтверждено (`e63691c` в `camp`) | Ветка — от `origin/camp`, PR — в `camp` |
| Прогон плана после правок | T1–T9 применены в отдельной копии: 97 тестов OK, величины на окне до 02:00 UTC совпали с прототипом. Мутации показали 7 непокрытых мест | Добавлены тесты: объединение одновременных остановок, вызов субагента во время остановки, чтение двух файлов одной командой, OTel-сессия «из журнала + связанная», правило по реплике памяти, два сжатия за ≤ 10 мин, карточка перечитывания. Лента и «Ваше время» тоже считают только остановки. Исправлены место вставки SQL и ссылка на запрос |

Отклонено одно: «проверка общей страницы на обеих версиях» автотестом. У страницы нет тестового окружения, а jsdom/Playwright — новый стек (AGENTS.md: «не добавляй стек… заранее»). Вместо этого T14 — обязательная проверка обеих версий во встроенном браузере по списку инвариантов. Если нужен автотест страницы — отдельное решение.

## Отклонения при реализации

T1–T9 применены как написано (код совпал с прогоном в отдельной копии), затем — правки по ревью отдельными коммитами. Код задач ниже не переписан, кроме двух первых пунктов; где он расходится с итоговым кодом, верен итоговый код. Тестов после правок — 118 (план: 99 после T9).

- **`wait_min` при остановке неизвестной длины** (`df9f4c8`, отражено в T6). В T6 было `None if stopped else 0.0`: окно разрешения Claude (`tool_decision`, source=user — остановка без ответа) давало 0 мин. Стало `None`, если остановки есть, но длину ни одной измерить нельзя; `0.0` — только когда остановок не было. Тест `test_permission_wait_of_unknown_length_stays_unknown`.
- **«Что проверить до правки» у общих карточек «Разобрать эпизоды»** (`ec87137`, отражено в T8). В T8 у них были пустые `preconditions`, хотя план требует их у каждой карточки по работе. Добавлены два пункта: повторяется ли причина и не объясняется ли всё неполным сбором. Три проверки карточки по ожиданию добавляются в тест T6 на шаге T8: `scope` появляется только в T7.
- **A. Вопрос, на который ответили внутри инструмента** (`ba99c27`, правка T6). По T6 вопрос длился до вашей следующей реплики. Но обычный (не async) `request_user_input` / `AskUserQuestion` держит агента до своего PostToolUse, и ответ часто приходит результатом вызова, без новой реплики; такой вопрос с вызовом после него выглядел как «агент продолжал работу». Теперь у не-async вопроса с PostToolUse конец — `min(PostToolUse, ваша реплика)`, занятость — вызов основного агента строго между вопросом и концом; у async — как было. У каждого ожидания явное поле `end`, по нему объединяются интервалы. В тесте T6 сессии `stop` и `pair` (вопрос, потом агент стоит до реплики — шаблон async) переведены на `request_user_input_async`. Новый тест `test_question_answered_inside_the_tool`.
- **Нулевые и неудавшиеся вопросы — не остановки** (`8b07437`, уточнение пункта A). После A интервал нулевой длины считался остановкой: вопрос, записанный только своим PostToolUse (`no_pre`, время вопроса = время результата), и обычный вопрос, сразу вернувший ошибку (Codex отказывает в `request_user_input` вне Plan mode), давали `wait_min` 0.0, флаг «ожидание» и «агент стоял 0.0 мин до ответа». Теперь вопрос без PreToolUse не попадает в ожидания; обычный вопрос с ошибкой считается в `waits`, но остановкой не бывает, его результат не ответ, в ленте — «вопрос не прошёл»; остановка требует интервала положительной длины без вызовов основного агента внутри, а ожидание без конца — как раньше (после вопроса основной агент ничего не делал). Тесты: вопрос без PreToolUse, вопрос с ошибкой, результат в ту же секунду, конец по более ранней из реплики и результата, конец по реплике без результата.
- **Остановка — не короче 5 секунд; неудавшийся вопрос — не «агент спросил вас»** (`c636c64`, уточнение предыдущего пункта). На настоящих данных время с долями секунды, и правило «интервал длиннее нуля» почти ничего не отсекало: отказ текстом через 50 мс (исход «done», не «error») оставался остановкой с «ждал 0.0 мин». Теперь остановка с концом требует не меньше `STOP_MIN_S = 5` секунд. Значения поля `side` у событий-вопросов (`k == "wait"`) в ленте сессии: «ждал N мин» — остановка; «агент продолжал работу» — основной агент работал, пока вопрос был открыт; «ответ сразу» — ответ быстрее 5 с; «вопрос не прошёл» — обычный вопрос вернул ошибку; «вопрос без PreToolUse» — записан только результат (раньше было «без ответа»); «без ответа» — остановка, ответа нет. Строка разрешений «агент спросил вас» не считает неудавшиеся вопросы. Тесты: отказ текстом через 50 мс; проверки в тестах вопроса с ошибкой и вопроса без PreToolUse.
- **B. Имя skill** (`96f8a60`, `21937f5`, правка T9). Регулярное выражение из T9 искало `name:` во всех первых 2048 символах, и `name:` из примера YAML в теле SKILL.md побеждал. Теперь читается только блок между первой строкой `---` и следующей `---`; результат кэшируется на путь (`functools.lru_cache`); файл не в UTF-8 даёт имя папки, а не ошибку. Пути skills в `testdata/live/hooks.jsonl` перенесены из `/tmp/…` в `/nonexistent-hottell-fixture/…`: настоящий файл в `/tmp` менял бы имя и размер и делал тесты зависимыми от машины.
- **C. `--until` — время сборки** (`1afb387`, к T14). `main()` передаёт `now` = конец окна, если `--until` задан. Иначе льгота 2 мин и проверка «сессия ещё идёт» зависели от часов машины, и сборка на фиксированном окне в T14 не воспроизводилась.
- **D. Вызовы, которые ещё выполняются** (`e36f036`, правка T7). Карточка «нет PostToolUse» пропускает вызовы последнего хода, если он открыт (нет Stop/Interrupt) и последнее событие сессии моложе часа на момент сборки (`LIVE_SESSION_S = 3600`). Через час тишины они снова считаются, поэтому замечание 5 из ревью (потери не исчезают навсегда) остаётся выполненным. Тест T7 `test_grace_expires_for_finished_sessions` дополнен: через 30 мин карточки нет, через сутки есть.
- **E. Тексты и чистка** (`a8c8ddb`, `175b6c0`, `e77335b`, `6c87dc8`, правки T4, T6 и T8). `LIVE_HOW["wait"]` называет и `AskUserQuestion` и говорит, что обычный вопрос длится до своего результата. `LIVE_HOW["reread"]` и docstring `read_signature`: «хэш записанного ответа» (хэшируется то, что записал hottell). Карточка перечитывания: свой порог `REREAD_CARD_MIN = 3` вместо `T.REREAD_MIN`; файлы группируются по полному пути и показываются как `папка/имя`, чтобы README.md из разных папок не сливались (тест T8 `test_reread_card` читает `docs/README.md`, `api/README.md`, `docs/c.md`). Без изменения поведения: `edited_paths` берёт `EDIT_TOOLS`, `_plain` определён один раз и используется и в `aggregate_friction`, вложенная функция вместо `lambda`, аргументы карточки кода выхода Bash упорядочены. Новые тесты перечитывания: Claude `Read` с `Edit` между чтениями и чтения с пустым записанным ответом (`resp_len=0`).
- **Вызов, шедший в момент вопроса, — не простой** (`309a095` и коммит «Unknown-end calls block a stop only within the question's turn», уточнение пунктов A и «не короче 5 секунд»). Раньше простой отсчитывался от самого вопроса: вызов основного агента с 5 по 90 с и async-вопрос в 10 с при ответе в 120 с давали 1.8 мин. Теперь простой начинается, когда закончились вызовы основного агента (не субагентов и не другие вопросы), уже шедшие в момент вопроса: `idle_from` = позднейший их конец; длина и интервал для объединения — от `idle_from` до конца ожидания, «ваше время» по-прежнему отсчитывается от вопроса. Конец вызова — PostToolUse, а у Claude без него — начало плюс длительность из `tool_result`. Вызов без известного конца мешает остановке, только если он из того же хода, что и вопрос (Codex — `turn_id` основного потока, Claude — `prompt_id`, без ключа — ход, открытый в момент вызова): новый ход начинается с вашей реплики, и вызов прошлого хода без PostToolUse — потерянный результат, а не работа; внутри хода правило остаётся осторожным. Короткий простой из-за шедшего вызова в ленте — «агент продолжал работу», «ответ сразу» — только когда шедших вызовов не было. Следствие в фикстуре: вопрос сессии A после «sleep 100» без PostToolUse больше не остановка (`wait_min` 3.0 → 0.0, `user_min` 13.0 → 10.0). Без ограничения ходом на живых данных (37 сессий, 2026-10-01) остановок было 0 из 10 вопросов: в сессиях Codex до вопроса сотни вызовов без PostToolUse, у двух `AskUserQuestion` Claude — Bash без конца, начатые за 45–689 мин до вопроса. С ограничением (38 сессий, 2026-10-01) — 2 из 10: 2cc8fd26 (17.2 мин) и 6d2750fc (0.4 мин), обе `AskUserQuestion` Claude; вопросы Codex — не остановки, агент в это время работал. Тесты: вызов без PostToolUse в прошлом ходе не мешает остановке, в том же ходе — мешает.
- **Чтения с ошибкой — не перечитывание** (`87a8b43`, правка T4). Одинаковый текст ошибки давал одинаковый хэш: три `cat README.md` с «Permission denied» образовывали эпизод. Теперь не участвуют чтения с исходом error и чтения, у которых первая строка ответа (без служебных строк Codex) — ошибка `cat/sed/head/tail/nl/less/bat: …: No such file or directory | Permission denied | Is a directory | Operation not permitted` (без кода выхода исход «done»). Тесты: ошибка с кодом выхода (в том числе текст вне списка), ошибка текстом без кода.
- **Skills под фильтром** (`48143ba`, было и в `camp`). При фильтре по проекту таблица и итог показывали активации за весь датасет (nuanu-ai-lab: 51 вместо 33 на живых данных 2026-10-01), хотя сессии были отфильтрованы. Оба сборщика (`live.py`, `telemetry.py`) пишут `skills.rows[].by_session` — `{sid: активаций}`, сумма равна `activations`; интерфейс считает по нему таблицу и итог (`bySession`), без поля — как раньше. Описано в `ui/CONTRACT.md`.

---

## Что не так сейчас (проверено, окно до 2026-10-01 02:00 UTC, запросы — в приложении)

| # | Проблема | Доказательство | Задача |
|---|---|---|---|
| 1 | Пример `hottell/examples/codex-hooks.json` расходится с установщиком: в примере `async: true`, `install.go` его снимает; карточка «нет PostToolUse» советует сверяться с примером | `live.py:1302`, `install.go:33`, коммит d192b63 | T1, T7 |
| 2 | Карточка «нет PostToolUse» смешивает сессии до установки хуков с новыми и навсегда исключает конец сессии | `build_findings` (a); прототип: в сессиях под хуками без служебных — 36 из 937 вызовов (3,8 %), чаще MCP cua_repl (18), MCP codex_app (8), Bash (8); со служебными — 56 из 1047 | T7 |
| 3 | «Перечитывание» объединяет агентов: CONSTITUTION.md в 01a0f058 — 76 чтений от 55 субагентов и 11 от основного; содержимое не сравнивается | запрос 1 | T4 |
| 4 | Эпизоды перечитывания: 79 на «Что исправить», 60 на «Обзоре»/«Трении» | `aggregate_friction` обрезает доказательства до 60, `episodes()` считает доказательства | T3 |
| 5 | «Агент ждал вас» и `wait_min` считают async-вопросы, пока агент работает | прототип 2 | T6 |
| 6 | OTel-карточка: на плашке 19 сессий, в тексте 22 | `live.py:1338` | T7 |
| 7 | Skills: «24 из 15 доступных»; skill с именем `skill` | `aggregate_skills`, имя из папки | T9 |
| 8 | «Ошибки 0,2 %» при 64 % вызовов с неизвестным исходом; стоимость null складывается как 0; «$0.00 на коммит»; «$ в этих сессиях» — стоимость сессий целиком | `renderOverview`, `renderFriction` | T5, T11 |
| 9 | Служебные задачи Codex (10 из 31: подсказки Desktop, память) размывают все «из N» | прототип 2 | T2, T10 |
| 10 | Карточки для пользователя — «разберите эпизоды» без оснований и без указания, что проверить | `build_findings` (d) | T8 |

Базовая линия: `cd ui/builder && python3 -m unittest` → `Ran 87 tests … OK`; `cd ui && go test ./...` и `cd hottell && go test ./...` → ok.

## Не входит в план

- Карточка про вопросы агента: на данных нет ни одной подтверждённой остановки (замечание 1).
- Детектор опроса субагентов (`collaboration.wait_agent`): неясна правка без документации Codex.
- Исход сессии и коррекции в живой версии — это Deep, версия :8800.
- Автотест страницы (см. выше).

## Порядок и ветка

`claude/live-honest-fix` от `origin/camp`; PR — в `camp`. Порядок: T0 → … → T15, каждая задача — зелёные тесты и коммит.

---

### T0: Ветка

```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git fetch origin && git status --short && git switch -c claude/live-honest-fix origin/camp
```
Ожидается: в статусе только этот план (`?? docs/specs/…plan.md`), затем `Switched to a new branch`. Проверить базу: `cd ui/builder && python3 -m unittest` → 87 OK.

---

### T1: Пример хуков Codex совпадает с установщиком

Обоснование — только расхождение примера и того, что пишет `hottell install`. Причину снятия `async` (d192b63: «Codex 0.159 пропускает хуки с неизвестным полем») здесь не утверждаем: установлен codex-cli 0.135.0, документация Codex описывает `async`.

**Файлы:** изменить `hottell/examples/codex-hooks.json`; тест — `hottell/install_test.go`.

**Шаг 1. Падающий тест** — в конец `hottell/install_test.go`:

```go
// Пример в examples/ должен совпадать с тем, что пишет install (codexEvents): те же события, matcher только у
// событий инструментов, без полей, которые установщик снимает.
func TestCodexExampleMatchesInstall(t *testing.T) {
	raw, err := os.ReadFile("examples/codex-hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hooks) != len(codexEvents) {
		t.Fatalf("событий в примере %d, install пишет %d", len(doc.Hooks), len(codexEvents))
	}
	for _, ev := range codexEvents {
		groups := doc.Hooks[ev.name]
		if len(groups) != 1 {
			t.Fatalf("%s: групп %d", ev.name, len(groups))
		}
		if _, has := groups[0]["matcher"]; has != ev.matcher {
			t.Fatalf("%s: matcher есть=%v, у install %v", ev.name, has, ev.matcher)
		}
		h := groups[0]["hooks"].([]any)[0].(map[string]any)
		if _, has := h["async"]; has {
			t.Fatalf("%s: в примере async, install его снимает (dropAsync)", ev.name)
		}
		if h["command"] != "hottell -agent codex" {
			t.Fatalf("%s: command=%v", ev.name, h["command"])
		}
	}
}
```

**Шаг 2.** `cd hottell && go test -run TestCodexExampleMatchesInstall ./...` → `FAIL … PreToolUse: в примере async`.

**Шаг 3.** Заменить `hottell/examples/codex-hooks.json`:

```json
{
  "hooks": {
    "PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "PermissionRequest": [{"matcher": "*", "hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "PreCompact": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "PostCompact": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "SubagentStart": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "SubagentStop": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "SessionStart": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}],
    "Interrupt": [{"hooks": [{"type": "command", "command": "hottell -agent codex"}]}]
  }
}
```

**Шаг 4.** `cd hottell && go vet ./... && go test ./...` → ok.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add hottell/examples/codex-hooks.json hottell/install_test.go && git commit -m "Codex hooks example matches what install writes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T2: Тип сессии — скрываются только явные служебные

| kind | Правило | По умолчанию |
|---|---|---|
| `system` | сырая первая реплика начинается с `# Overview Generate N to M hyperpersonalized suggestions` или `## Memory Writing Agent`; или рабочая папка `~/.codex/memories` (папка самого Codex, не проект) | скрыта |
| `automation` | сырая первая реплика начинается с `<heartbeat>` | видна, с пометкой |
| `user` | всё остальное — в том числе короткие сессии и сессии без реплик (может быть неполный сбор) | видна |

Правила смотрят на сырой текст (`raw`): `T.prompt_text` вырезает парные теги.

**Файлы:** `ui/builder/live.py` (константы после `CODEX_NAMESPACES`; `build_session`; новая `classify_session`; `session_fields`); тест — `ui/builder/test_live.py`.

**Шаг 1. Помощники для синтетических сессий** — в `test_live.py` в импорты добавить `import datetime as dt` и `import hashlib`; после `_hook`:

```python
T0 = dt.datetime(2026, 3, 8, 10, 0, tzinfo=dt.timezone.utc)


def _sid(t0, c):
    """uuid7 со временем t0: сессия «создана» в момент первого события, поэтому не partial."""
    ms = f"{int(t0.timestamp() * 1000):012x}"
    return f"{ms[:8]}-{ms[8:12]}-7{c * 3}-8{c * 3}-{c * 12}"


def _ts(t0, s):
    return (t0 + dt.timedelta(seconds=s)).strftime("%Y-%m-%d %H:%M:%S.000000000")


def _call(t0, sid, s, tuid, tin, resp="ok", tool="Bash", post=True, resp_hash=None, **kw):
    """Пара PreToolUse/PostToolUse; post=False — только PreToolUse. resp_hash по умолчанию — хэш resp."""
    rows = [_hook(_ts(t0, s), "PreToolUse", sid, tool=tool, tuid=tuid, tin=tin, **kw)]
    if post:
        rows.append(_hook(_ts(t0, s + 1), "PostToolUse", sid, tool=tool, tuid=tuid, tin=tin, resp_head=resp,
                          resp_len=len(resp), resp_hash=resp_hash or hashlib.sha1(resp.encode()).hexdigest()[:16], **kw))
    return rows


def run(hooks, sse=(), now=None, codex_otel=()):
    fake = FakeClickHouse()
    fake.rows = {"hooks": hooks, "claude_logs": [], "claude_metrics": [], "codex_otel": list(codex_otel),
                 "codex_sse": list(sse)}
    return L.build_dataset(L.fetch(fake), now=now)
```

**Шаг 2. Падающие тесты.** В `class Sessions`:

```python
    def test_fixture_sessions_are_user_work(self):
        self.assertEqual({s["kind"] for s in self.ds["sessions"]}, {"user"})
```

Новый класс:

```python
class SessionKindTest(unittest.TestCase):
    def test_only_explicit_service_sessions_are_system(self):
        t = [T0 + dt.timedelta(hours=h) for h in range(6)]
        sug, mem, hb, short, silent, mwa = (_sid(t[i], c) for i, c in enumerate("abcdef"))
        bash = '{"command":"git status"}'
        memdir = "/Users/x/.codex/memories"
        hooks = [
            _hook(_ts(t[0], 0), "SessionStart", sug, source="startup"),
            _hook(_ts(t[0], 1), "UserPromptSubmit", sug, turn_id="T1",
                  prompt="# Overview\nGenerate 0 to 3 hyperpersonalized suggestions for what this user can do"),
            *_call(t[0], sug, 5, "s1", bash),
            _hook(_ts(t[1], 0), "UserPromptSubmit", mem, turn_id="T1", prompt="consolidate", cwd=memdir),
            *_call(t[1], mem, 5, "m1", bash, cwd=memdir),
            _hook(_ts(t[2], 0), "UserPromptSubmit", hb, turn_id="T1", prompt="<heartbeat>\n<instructions>Разбери транскрипты"),
            *_call(t[2], hb, 5, "h1", bash),
            # контрпримеры из ревью: обычный короткий запрос и сессия без реплик остаются видимыми
            _hook(_ts(t[3], 0), "SessionStart", short, source="startup"),
            _hook(_ts(t[3], 1), "UserPromptSubmit", short, turn_id="T1", prompt="Проверь статус сервиса"),
            *_call(t[3], short, 5, "c1", '{"command":"systemctl status api"}'), *_call(t[3], short, 20, "c2", bash),
            *_call(t[4], silent, 0, "q1", bash), *_call(t[4], silent, 120, "q2", bash),
            # правило по реплике памяти — отдельно от правила по папке
            _hook(_ts(t[5], 0), "UserPromptSubmit", mwa, turn_id="T1",
                  prompt="## Memory Writing Agent: Phase 2 (Consolidation)\nYou are a Memory Writing Agent."),
            *_call(t[5], mwa, 5, "w1", bash),
        ]
        ds, _ = run(hooks)
        kinds = {s["id"]: s["kind"] for s in ds["sessions"]}
        self.assertEqual(kinds, {sug: "system", mem: "system", hb: "automation", short: "user", silent: "user",
                                 mwa: "system"})
        reasons = {s["id"]: s["kind_reason"] for s in ds["sessions"]}
        self.assertEqual(reasons[sug], "подсказки Codex Desktop")
        self.assertIsNone(reasons[short])
```

**Шаг 3.** `cd ui/builder && python3 -m unittest test_live.SessionKindTest test_live.Sessions.test_fixture_sessions_are_user_work` → `KeyError: 'kind'`.

**Шаг 4. Реализация.** После `CODEX_NAMESPACES = (...)`:

```python
# Сессии явного служебного происхождения — интерфейс по умолчанию их скрывает. Сомнительные (короткие, без реплик)
# остаются видимыми: короткая сессия бывает обычной работой, отсутствие реплик — неполным сбором.
SYSTEM_PROMPTS = (
    (re.compile(r"^# Overview Generate \d+ to \d+ hyperpersonalized suggestions"), "подсказки Codex Desktop"),
    (re.compile(r"^## Memory Writing Agent"), "память Codex"),
)
SYSTEM_CWD = re.compile(r"/\.codex/memories(?:/|$)")     # рабочая папка самого Codex, не проект
AUTOMATION_PROMPT = re.compile(r"^<heartbeat>")
```

В `build_session` в сборе реплик заменить

```python
            prompts.append({"at": r["at"], "text": text, "kind": kind, "turn_id": r.get("turn_id") or "",
                            "prompt_id": r.get("prompt_id") or "", "title": r.get("title") or ""})
```
на
```python
            prompts.append({"at": r["at"], "text": text, "kind": kind, "turn_id": r.get("turn_id") or "",
                            "prompt_id": r.get("prompt_id") or "", "title": r.get("title") or "",
                            "raw": (r.get("prompt") or "")[:300]})
```

В конце `build_session` после `S["first_hook"] = first_hook`:

```python
    S["kind"], S["kind_reason"] = classify_session(S)
```

После `build_session`:

```python
def classify_session(S):
    """(kind, reason): system — явная служебная задача Codex; automation — запуск по расписанию; user — остальное."""
    first = next((p for p in S["prompts"] if p["kind"] == "prompt"), None)
    text = " ".join((first["raw"] if first else "").split())
    for rx, why in SYSTEM_PROMPTS:
        if rx.match(text):
            return "system", why
    if S["cwd"] and SYSTEM_CWD.search(S["cwd"]):
        return "system", "папка ~/.codex/memories"
    if AUTOMATION_PROMPT.match(text):
        return "automation", "реплика <heartbeat>"
    return "user", None
```

В `session_fields` после `"id": S["sid"], "short": S["sid"][:8], "agent": agent,`:

```python
        "kind": S["kind"], "kind_reason": S["kind_reason"],
```

**Шаг 5.** `python3 -m unittest` → OK.

**Шаг 6.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Live dataset: mark explicit Codex service sessions and scheduled runs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T3: Эпизоды трения по сессиям

**Файлы:** `ui/builder/live.py` (`aggregate_friction`), `ui/static/index.html` (`episodes`); тест — `Aggregates.test_friction`.

**Шаг 1. Падающий тест** — в конец `Aggregates.test_friction`:

```python
        self.assertEqual(fr["mcpfail"]["by_session"], {A: 1})
        self.assertEqual(fr["coldcache"]["by_session"], {C: 1})
        self.assertEqual(fr["retry"]["by_session"], {})
```

**Шаг 2.** `python3 -m unittest test_live.Aggregates.test_friction` → `KeyError: 'by_session'`.

**Шаг 3.** В `aggregate_friction` после `"sessions": sorted({sid for sid, _ in occ}),`:

```python
                    "by_session": dict(collections.Counter(sid for sid, _ in occ)),
```

В `index.html` заменить `episodes`:

```js
function episodes(f,ids){if(f.by_session)return Object.entries(f.by_session).reduce((n,[sid,k])=>n+(ids.has(sid)?k:0),0);const ev=f.evidence||[];if(ev.length)return ev.filter(e=>ids.has(e.sid)).length;return (f.sessions||[]).every(x=>ids.has(x))?(f.count??null):null}
```

**Шаг 4.** `python3 -m unittest` → OK.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py ui/static/index.html && git commit -m "Count friction episodes per session instead of capped evidence rows

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T4: Перечитывание — свой агент, проверенное одинаковое содержимое

Эпизод: одно место файла прочитано ≥ 3 раз подряд **одним агентом** (субагенты отдельно), у всех чтений **известен результат и совпадает хэш всего ответа**, между ними нет сжатия контекста и правки этого файла. Не участвуют: чтения без PostToolUse, команды, читающие несколько файлов (ответ не разделить), хвост (`tail`, «последние N»).

Хэш считает ClickHouse: `cityHash64` ответа без служебных строк Codex (`Chunk ID`, `Wall time`, `Process exited…`, `Original token count`, `Output:`). Выражение проверено запросом 2 в приложении.

**Файлы:** `ui/builder/live.py` (`SQL["hooks"]`; присоединение PostToolUse в `build_session`; помощники после `read_targets`; блок reread в `_friction`; `LIVE_HOW["reread"]`); тест — `test_live.py`.

**Шаг 1. Падающие тесты:**

```python
class RereadTest(unittest.TestCase):
    def test_only_same_agent_same_known_content_without_cuts(self):
        sid = _sid(T0, "1")
        cat = lambda f: json.dumps({"command": f"cat {f}"})
        patch = json.dumps({"command": "*** Begin Patch\n*** Update File: docs/b.md\n@@\n-x\n+y\n*** End Patch"})
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        step = iter(range(10, 100000, 10))

        def c(tin, **kw):
            kw.setdefault("resp", "same")
            hooks.extend(_call(T0, sid, next(step), f"t{len(hooks)}", tin, turn_id="T1", **kw))

        for _ in range(3):
            c(cat("docs/a.md"))                                         # единственный эпизод
        c(cat("docs/b.md")); c(cat("docs/b.md")); c(patch, tool="apply_patch", resp="Success"); c(cat("docs/b.md"))
        c(cat("docs/c.md")); c(cat("docs/c.md"))
        hooks.append(_hook(_ts(T0, next(step)), "PostCompact", sid, turn_id="T1", trig="auto"))
        c(cat("docs/c.md"))
        for k in range(3):
            c(cat("docs/d.md"), aid=f"sub-{k}", atype="default")        # каждый субагент — по разу
        c(cat("docs/d.md"))
        for _ in range(4):
            c(json.dumps({"command": "tail -n 27 ci.log"}))
        for k in range(3):
            c(cat("docs/e.md"), resp=f"версия {k}")                     # файл менялся
        for k in range(3):
            c(cat("docs/f.md"), resp_hash=f"h{k}")                      # контрпример: начало то же, дальше разное
        for _ in range(3):
            c(cat("docs/g.md"), post=False)                             # контрпример: результата нет
        for _ in range(3):
            c(cat("docs/h.md docs/i.md"))                               # ответ на два файла не разделить
        ds, _ = run(hooks)
        rr = next(f for f in ds["friction"] if f["key"] == "reread")
        self.assertEqual(rr["count"], 1)
        self.assertTrue(all("a.md" in e["text"] for e in rr["evidence"]))

    def test_reread_runs_cut_on_compaction_and_changed_content(self):
        t = [T0 + dt.timedelta(minutes=m) for m in range(6)]
        items = [({"at": t[0]}, "x"), ({"at": t[1]}, "x"), ({"at": t[2]}, "y"), ({"at": t[3]}, "y"), ({"at": t[5]}, "y")]
        self.assertEqual(L.reread_runs(items, []), [[items[2][0], items[3][0], items[4][0]]])
        self.assertEqual(L.reread_runs(items, [t[4]]), [])
```

**Шаг 2.** `python3 -m unittest test_live.RereadTest` → `AttributeError: … reread_runs` и `AssertionError` (сейчас эпизодов больше одного).

**Шаг 3. Реализация.**

3a. В `SQL["hooks"]` строку

```python
        length(LogAttributes['tool_response']) resp_len, LogAttributes['duration_ms'] dur,
```
заменить на
```python
        length(LogAttributes['tool_response']) resp_len,
        toString(cityHash64(replaceRegexpOne(LogAttributes['tool_response'],
            '^(?:(?:Chunk ID|Wall time|Process exited with code|Original token count)[^\\n]*\\n)*(?:Output:\\n)?', ''))) resp_hash,
        LogAttributes['duration_ms'] dur,
```

(`\\n` в обычной Python-строке доходит до ClickHouse как `\n` внутри литерала и становится переводом строки — RE2 сравнивает его буквально; проверено прогоном.)

3b. В `build_session` в присоединении PostToolUse после строки `c["hx"], c["hstatus"] = …` добавить:

```python
        c["resp_hash"] = r.get("resp_hash") or None
```

3c. После `read_targets`:

```python
def read_call_targets(c, cwd):
    """(файл, место), которые прочитал вызов: Claude Read (offset/limit) или cat/sed -n/head/tail в shell."""
    if c["tool"] == "Read" and isinstance(c["args"].get("file_path"), str):
        off, lim = c["args"].get("offset"), c["args"].get("limit")
        span = f"строки {off}–{(off or 0) + lim}" if isinstance(off, int) and isinstance(lim, int) else (
            f"первые {lim}" if isinstance(lim, int) else "весь файл")
        return [(c["args"]["file_path"], span)]
    return read_targets(c["cmd"], cwd) if c["cmd"] else []


def edited_paths(c, cwd):
    """Файлы, которые меняет вызов: Edit/Write (file_path) и apply_patch (*** Update File: …)."""
    if c["tool"] in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
        p = c["args"].get("file_path") or c["args"].get("notebook_path")
        return [p] if isinstance(p, str) else []
    if c["tool"] == "apply_patch":
        files = _PATCH_FILE.findall(str(c["args"].get("command") or c["args"].get("patch") or c["tin"]))
        return [f if os.path.isabs(f) or not cwd else os.path.normpath(os.path.join(cwd, f)) for f in files]
    return []


def read_signature(c):
    """Хэш всего ответа чтения. None — результата нет: содержимое неизвестно, равенство утверждать нельзя."""
    if c["post_at"] is None or not c.get("resp_len"):
        return None
    return c.get("resp_hash")


def reread_runs(items, cuts):
    """items — [(вызов, хэш)] одного агента и места файла по времени, только с известным содержимым;
    cuts — сжатия и правки файла. Серия — подряд одинаковое содержимое без сжатия и правки между чтениями."""
    runs, run, seg, last = [], [], None, None
    for c, sig in items:
        k = bisect.bisect_right(cuts, c["at"])
        if run and (k != seg or sig != last):
            runs.append(run)
            run = []
        run.append(c)
        seg, last = k, sig
    if run:
        runs.append(run)
    return [r for r in runs if len(r) >= T.REREAD_MIN]
```

3d. В `_friction` заменить весь блок от `reads = collections.defaultdict(list)` до конца цикла `for (path, span), items in sorted(...)`:

```python
    comp_at = [r["at"] for r in S["compacts"]]
    edits = collections.defaultdict(list)
    for c in calls:
        for p in edited_paths(c, S["cwd"]):
            edits[p].append(c["at"])
    reads = collections.defaultdict(list)
    for c in calls:
        targets = set(read_call_targets(c, S["cwd"]))
        sig = read_signature(c) if len(targets) == 1 else None      # ответ на несколько файлов не разделить
        if sig is None:
            continue
        (path, span), = targets
        if not span.startswith("последние"):                         # хвост лога дописывает другой процесс
            reads[(c["aid"], path, span)].append((c, sig))
    for (aid, path, span), items in sorted(reads.items(), key=lambda kv: -len(kv[1])):
        name = os.path.basename(path) + ("" if span == "весь файл" else f" {span}") + (" · субагент" if aid else "")
        for run in reread_runs(items, sorted(comp_at + edits.get(path, []))):
            extra = run[1:]
            out["reread"].append({"path": path, "extra": len(extra), "bytes": sum(x["resp_len"] for x in extra),
                                  "evidence": [ev(x, f"{name} · чтение {k + 1}/{len(run)}") for k, x in enumerate(run[:8])]})
```

3e. `LIVE_HOW["reread"]`:

```python
    "reread": "Одно место файла прочитано ≥ 3 раз подряд одним агентом (субагенты — отдельно) с одинаковым ответом "
              "(хэш всего ответа), без сжатия контекста и правки файла между чтениями. Чтения без результата, "
              "команды на несколько файлов и хвост логов (tail) не считаются.",
```

**Шаг 4.** `python3 -m unittest` → OK.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Reread detector: per agent, identical full response, cut by compaction and edits

Reads without a result or covering several files no longer count as identical.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T5: Сколько вызовов с известным исходом

**Файлы:** `ui/builder/live.py` (`session_fields`); тест — `TranscriptEnrichmentTest`.

**Шаг 1.** В `class TranscriptEnrichmentTest`:

```python
    def test_outcome_known(self):
        # exit 2 (ошибка) и completed (успех) известны; «x» без кода выхода — исход неизвестен
        self.assertEqual(self.s["outcome_known"], 2)
```

**Шаг 2.** → `KeyError: 'outcome_known'`.

**Шаг 3.** В `session_fields` после `"unknown_results": …,`:

```python
        "outcome_known": sum(c["state"] in ("success", "error") for c in calls),
```

**Шаг 4.** `python3 -m unittest` → OK.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Live sessions report how many tool calls have a known outcome

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T6: Ожидание ответа — только когда агент действительно стоял

Вопрос (`request_user_input*`) считается остановкой, только если основной агент после него не сделал ни одного вызова до вашей реплики (или до конца записи, если ответа нет). Async-вопрос, пока агент работает, — не остановка. `wait_min` — объединённые интервалы таких остановок (перекрытия не складываются). `waits` — по-прежнему число вопросов.

**Файлы:** `ui/builder/live.py` (блок `# --- waits` в `build_session`; цикл `wait` в `_friction`; `LIVE_HOW["wait"]`); тест — `test_live.py`.

**Шаг 1. Падающий тест (контрпример из ревью):**

```python
class WaitsTest(unittest.TestCase):
    def test_async_question_while_agent_works_is_not_a_stop(self):
        t1, t2 = T0 + dt.timedelta(hours=1), T0 + dt.timedelta(hours=2)
        go, stop, pair = _sid(T0, "5"), _sid(t1, "6"), _sid(t2, "8")
        q = json.dumps({"questions": [{"question": "Какой формат?"}]})
        hooks = [_hook(_ts(T0, 0), "SessionStart", go, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", go, turn_id="T1", prompt="Собери отчёт"),
                 *_call(T0, go, 10, "q1", q, tool="request_user_input_async", turn_id="T1"),
                 *_call(T0, go, 20, "q2", q, tool="request_user_input_async", turn_id="T1"),
                 *[r for k in range(5) for r in _call(T0, go, 60 + k * 120, f"b{k}", '{"command":"ls"}', turn_id="T1")],
                 _hook(_ts(T0, 10 + 20 * 60), "UserPromptSubmit", go, turn_id="T1", prompt="таблицей"),
                 # агент стоял; вызов субагента в это время не делает основного агента занятым
                 _hook(_ts(t1, 0), "SessionStart", stop, source="startup"),
                 _hook(_ts(t1, 1), "UserPromptSubmit", stop, turn_id="T1", prompt="Собери отчёт"),
                 *_call(t1, stop, 10, "q1", q, tool="request_user_input", turn_id="T1"),
                 *_call(t1, stop, 100, "s1", '{"command":"ls"}', aid="sub-1", atype="default"),
                 _hook(_ts(t1, 10 + 10 * 60), "UserPromptSubmit", stop, turn_id="T1", prompt="таблицей"),
                 # два вопроса в одну секунду и один ответ: 10 мин, а не 20
                 _hook(_ts(t2, 0), "SessionStart", pair, source="startup"),
                 _hook(_ts(t2, 1), "UserPromptSubmit", pair, turn_id="T1", prompt="Собери отчёт"),
                 *_call(t2, pair, 10, "q1", q, tool="request_user_input", turn_id="T1"),
                 *_call(t2, pair, 10, "q2", q, tool="request_user_input", turn_id="T1"),
                 _hook(_ts(t2, 10 + 10 * 60), "UserPromptSubmit", pair, turn_id="T1", prompt="таблицей")]
        ds, tl = run(hooks)
        s = {x["id"]: x for x in ds["sessions"]}
        # два вопроса, агент работал: ни простоя, ни «вашего времени» от вопроса — не 40 и не 20 мин
        self.assertEqual((s[go]["waits"], s[go]["wait_min"], s[go]["user_min"]), (2, 0.0, 0.0))
        self.assertNotIn("wait", s[go]["flags"])
        self.assertEqual((s[stop]["waits"], s[stop]["wait_min"], s[stop]["user_min"]), (1, 10.0, 10.0))
        self.assertEqual((s[pair]["waits"], s[pair]["wait_min"]), (2, 10.0))
        wait = next(f for f in ds["friction"] if f["key"] == "wait")
        self.assertEqual(wait["by_session"], {stop: 1, pair: 2})
        q_ev = [e for e in tl[go]["events"] if e["k"] == "wait"]
        self.assertTrue(q_ev and all(e["side"] == "агент продолжал работу" for e in q_ev))

    def test_permission_wait_of_unknown_length_stays_unknown(self):
        sid = C   # any id; agent is claude
        rows = [dict(_hook(_ts(T0, 1), "UserPromptSubmit", sid, agent="claude", prompt_id="p1", prompt="Собери отчёт"))]
        for r in rows:
            r["at"] = L.parse_ch_ts(r["ts"])
        S = L.build_session("claude", sid, rows, claude=[{"e": "tool_decision", "dsource": "user", "decision": "accept",
                                                         "at": T0 + dt.timedelta(seconds=5)}])
        self.assertIsNone(S["wait_min"])
```

Второй тест до правки проходит (сейчас `wait_min` — `None`, если есть хоть одно ожидание) и защищает от регрессии: остановка неизвестной длины (окно разрешения Claude) не должна превращаться в 0 мин.

**Шаг 2.** `python3 -m unittest test_live.WaitsTest` → `AssertionError: (2, 20.0, 19.8) != (2, 0.0, 0.0)`.

**Шаг 3. Реализация.** В `build_session` заменить блок от `# --- waits: question tool -> next prompt` до строки `S["wait_min"] = …` включительно:

```python
    # --- waits: вопрос вам -> ваша следующая реплика. Остановка — только если основной агент после вопроса
    # ничего не делал до ответа (async-вопрос, пока агент работает, — не остановка). Интервалы объединяются.
    p_at = [p["at"] for p in prompts]
    main_at = sorted(c["at"] for c in calls if not c["aid"])
    waits = []
    for c in calls:
        if c["tool"] in WAIT_TOOLS:
            i = bisect.bisect_right(p_at, c["at"])
            reply = prompts[i] if i < len(prompts) else None
            j = bisect.bisect_right(main_at, c["at"])
            busy = j < len(main_at) and (reply is None or main_at[j] < reply["at"])
            waits.append({"call": c, "at": c["at"], "reply": reply, "stopped": not busy,
                          "min": T.minutes(c["at"], reply["at"]) if reply else None})
    for e in decisions:
        if (e.get("dsource") or "") == "user":                      # окно разрешения Claude блокирует ход
            waits.append({"call": None, "at": e["at"], "reply": None, "min": None, "stopped": True, "decision": e})
    S["waits"] = waits
    stopped = [w for w in waits if w["stopped"] and w.get("call") is not None]
    iv = sorted((w["at"], w["reply"]["at"]) for w in stopped if w.get("reply"))
    total, ca, cb = 0.0, None, None
    for a, b in iv:
        if cb is None or a > cb:
            if cb is not None:
                total += T.minutes(ca, cb)
            ca, cb = a, b
        else:
            cb = max(cb, b)
    if cb is not None:
        total += T.minutes(ca, cb)
    S["wait_min"] = round(total, 1) if iv else (None if any(w["stopped"] for w in waits) else 0.0)
```

`wait_min`: объединённая длина остановок с ответом; `None` — остановки есть (в том числе окно разрешения Claude), но длину ни одной измерить нельзя; `0.0` — остановок не было.

`main_at` содержит и сам вопрос: `bisect_right` ставит `j` после всех вызовов с тем же временем, поэтому сам вопрос не делает агента «занятым».

«Ваше время» отсчитывается только от остановок: в блоке `# --- user time` заменить `+ [w["at"] for w in waits]` на `+ [w["at"] for w in waits if w["stopped"]]`. В `GENERAL_GAPS` в строке про `user_min` заменить «паузы «ответ/вопрос агента → ваша реплика»» на «паузы «ответ агента или его остановка на вопросе → ваша реплика»».

Лента сессии: в `_events` заменить

```python
                ev["side"] = f"ждал {w['min']:.1f} мин" if w and w["min"] is not None else "без ответа"
```
на
```python
                ev["side"] = ("агент продолжал работу" if w and not w["stopped"] else
                              f"ждал {w['min']:.1f} мин" if w and w["min"] is not None else "без ответа")
```

В `_friction` заменить цикл `for w in S["waits"]:`:

```python
    for w in S["waits"]:
        if w.get("call") is not None and w["stopped"]:
            tail = f"агент стоял {w['min']:.1f} мин до ответа" if w["min"] is not None else "агент остановился, ответа не было"
            out["wait"].append({"evidence": [ev(w["call"], f"{tail} · {tool_summary(w['call']['tool'], w['call']['tin'], 120)}")]})
```

`LIVE_HOW["wait"]`:

```python
    "wait": "Вопрос вам (request_user_input*), после которого основной агент не сделал ни одного вызова до вашей реплики. "
            "Async-вопросы, пока агент продолжает работу, не считаются.",
```

**Шаг 4.** `python3 -m unittest` → OK (тест фикстуры `(1, 1, 3.0)` у сессии A не меняется: между её вопросом и ответом вызовов нет).

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Count a question as a wait only when the agent actually stopped

Async questions while the agent keeps working are no longer reported as idle time; overlaps are merged.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T7: Карточки здоровья сбора данных

Три карточки про сам hottell получают `scope: "collection"` и вклад каждой сессии. «Нет PostToolUse»: только сессии, начатые при работающих хуках; не считаются только вызовы моложе 2 минут **на момент сборки**; вызовы в последние 2 минуты своей сессии считаются и называются отдельно (возможен обрыв хода). OTel-карточка берёт одно число для текста и плашки.

**Файлы:** `ui/builder/live.py` (`_finding`; `build_findings` → `collection_findings`; `build_dataset` передаёт `now`); тест — `test_live.py`.

**Шаг 1. Падающие тесты.** Заменить `Aggregates.test_findings`:

```python
    def test_findings(self):
        f = {x["title"]: x for x in self.ds["findings"]}
        self.assertEqual(set(f), {"Связать нативный OTel Codex с сессиями", "Получать код выхода Bash в хуке PostToolUse Codex"})
        self.assertTrue(all(x["scope"] == "collection" and x["source"] == "detector" for x in f.values()))
        otel = f["Связать нативный OTel Codex с сессиями"]
        self.assertIn("codex.api_request — 50", otel["what"])
        self.assertEqual(otel["sessions"], [P])
        self.assertIn("У остальных 1 токены", otel["what"])
        self.assertEqual(otel["impact"]["value"], f"{len(otel['sessions'])} сессий")
        self.assertEqual(otel["impact_by_session"], {P: 1})
        bash = f["Получать код выхода Bash в хуке PostToolUse Codex"]
        self.assertIn("У 5 из 5 результатов Bash", bash["what"])
```

Новый класс:

```python
class CollectionFindingsTest(unittest.TestCase):
    def lost_card(self, ds):
        return next((x for x in ds["findings"] if x["title"].startswith("Найти, почему Codex не присылает PostToolUse")), None)

    def test_post_lost_in_sessions_started_under_hooks(self):
        sid = _sid(T0, "2")
        mcp = json.dumps({"query": "is:pr"})
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Найди мои PR")]
        for k in range(10):
            hooks += _call(T0, sid, 10 + k * 5, f"b{k}", '{"command":"ls"}', turn_id="T1")
        for k in range(4):
            hooks += _call(T0, sid, 80 + k * 5, f"m{k}", mcp, tool="mcp__codex_apps__github__search_prs", post=False, turn_id="T1")
        hooks += _call(T0, sid, 400, "last", '{"command":"ls"}', post=False, turn_id="T1")
        f = self.lost_card(run(hooks, now=T0 + dt.timedelta(days=1))[0])
        self.assertEqual(f["scope"], "collection")
        self.assertIn("у 5 из 15 вызовов нет PostToolUse", f["what"])
        self.assertIn("в последние 2 минуты своей сессии — 1", f["what"])
        self.assertIn("MCP codex_apps — 4", f["what"])
        self.assertNotIn("async", f["snip"])

    def test_grace_expires_for_finished_sessions(self):
        # контрпример из ревью: короткая завершённая сессия, три результата пропали в самом конце
        sid = _sid(T0, "7")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Проверь сервис")]
        for k in range(3):
            hooks += _call(T0, sid, 10 + k * 10, f"x{k}", '{"command":"ls"}', post=False, turn_id="T1")
        self.assertIsNone(self.lost_card(run(hooks, now=T0 + dt.timedelta(seconds=60))[0]))   # результат ещё может прийти
        f = self.lost_card(run(hooks, now=T0 + dt.timedelta(days=1))[0])
        self.assertIn("у 3 из 3 вызовов нет PostToolUse", f["what"])

    def test_otel_card_counts_sessions_once(self):
        # сессия с токенами из журнала (ход T1) и с codex.sse_event (ход T2), плюс сессия без токенов:
        # на плашке и в тексте — 1. Старая формула вычитала связанную сессию, уже исключённую как «из журнала», и давала 0.
        t1 = T0 + dt.timedelta(hours=1)
        both, none = _sid(T0, "9"), _sid(t1, "0")
        hooks = [_hook(_ts(T0, 0), "SessionStart", both, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", both, turn_id="T1", prompt="Посчитай"),
                 *_call(T0, both, 5, "a1", '{"command":"ls"}', turn_id="T1"),
                 _hook(_ts(T0, 20), "Stop", both, turn_id="T1", t_resp="1", t_in="100", t_cached="50", t_out="5",
                       t_model="gpt-6-sol"),
                 _hook(_ts(T0, 30), "UserPromptSubmit", both, turn_id="T2", prompt="Ещё раз"),
                 _hook(_ts(T0, 50), "Stop", both, turn_id="T2"),
                 _hook(_ts(t1, 0), "SessionStart", none, source="startup"),
                 _hook(_ts(t1, 1), "UserPromptSubmit", none, turn_id="T1", prompt="Посчитай ещё"),
                 *_call(t1, none, 5, "n1", '{"command":"ls"}', turn_id="T1")]
        sse = [{"ts": _ts(T0, 40), "sid": both, "kind": "response.completed", "model": "gpt-6-sol",
                "inp": "10", "cached": "5", "outp": "1", "reas": "0"}]
        otel = [{"svc": "Codex Desktop", "e": "codex.api_request", "endpoint": "/models", "linked": 0, "n": 5, "convs": 0}]
        ds, _ = run(hooks, sse=sse, codex_otel=otel)
        f = next(x for x in ds["findings"] if x["title"] == "Связать нативный OTel Codex с сессиями")
        self.assertEqual(f["sessions"], [none])
        self.assertEqual(f["impact"]["value"], "1 сессий")              # старая формула давала 0
        self.assertIn("У остальных 1 токены", f["what"])
```

**Шаг 2.** `python3 -m unittest test_live.Aggregates.test_findings test_live.CollectionFindingsTest` → падает: старое название карточки, нет `scope`/`impact_by_session`, `TypeError` на `None` вместо карточки, OTel-плашка «0 сессий».

**Шаг 3. Реализация.**

3a. `_finding` — заменить целиком:

```python
def _finding(fid, sev, title, what, ev, sessions, *, impact=None, impact_by_session=None, impact_unit=None, where=None,
             snip=None, readiness="hypothesis", alternative_causes=(), preconditions=(), verification=None, pattern_id=None,
             kind="diagnostic", scope="work"):
    return {"id": "live:" + hashlib.sha1(fid.encode()).hexdigest()[:20], "sev": sev, "title": title, "what": what,
            "ev": ev, "impact": impact, "impact_by_session": impact_by_session, "impact_unit": impact_unit,
            "where": where, "snip": snip, "kind": kind, "pattern_id": pattern_id, "scope": scope,
            "readiness": readiness, "decision": "not_requested", "execution": "not_applied", "effect": "not_measured",
            "source": "detector", "lang": "ru", "sessions": sessions, "cause": None,
            "alternative_causes": list(alternative_causes), "preconditions": list(preconditions), "exceptions": [],
            "verification": verification, "rollback": None, "expected_effect": None}
```

(По умолчанию `readiness="hypothesis"`: у детекторов причина не проверена. Существующие вызовы с `readiness="needs_spec"` сохраняют своё значение.)

3b. Переименовать `def build_findings(sessions, codex_otel):` в `def collection_findings(sessions, codex_otel, now):`, удалить из неё блок `# (d) friction detectors…` (переедет в T8; `return out` остаётся). Ниже:

```python
def build_findings(sessions, codex_otel, now):
    return collection_findings(sessions, codex_otel, now)
```

В `build_dataset` заменить `"findings": build_findings(built, raw.get("codex_otel") or []),` на `"findings": build_findings(built, raw.get("codex_otel") or [], now),`.

3c. Перед `collection_findings`:

```python
RESULT_GRACE_S = 120   # вызов моложе 2 минут на момент сборки: результат ещё может прийти
```

3d. Заменить блок `# (a) Codex PreToolUse without PostToolUse` (от `codex = [...]` до конца `out.append(_finding("codex-pre-post", …))`):

```python
    # (a) Codex: вызовы без PostToolUse — только в сессиях, начатых при работающих хуках
    codex = [S for S in sessions if S["agent"] == "codex" and S["calls"]]
    under = [S for S in codex if _started_under_hooks(S)]
    lost, total = [], 0
    for S in under:
        end = S["calls"][-1]["at"]
        for c in S["calls"]:
            if c.get("no_pre") or (now - c["at"]).total_seconds() < RESULT_GRACE_S:
                continue
            total += 1
            if c["state"] == "unknown":
                lost.append((S, c, (end - c["at"]).total_seconds() < RESULT_GRACE_S))
    if len(lost) >= 3 and len(lost) / total >= 0.05:
        label = lambda c: f"MCP {c['mcp']}" if c["mcp"] else c["name"]
        top = ", ".join(f"{k} — {v}" for k, v in collections.Counter(label(c) for _, c, _ in lost).most_common(4))
        per = collections.Counter(S["sid"] for S, _, _ in lost)
        at_end = sum(1 for *_, e in lost if e)
        what = (f"В {len(per)} сессиях Codex, начатых при работающих хуках, у {len(lost)} из {total} вызовов нет PostToolUse "
                f"({len(lost) / total * 100:.0f} %), из них в последние 2 минуты своей сессии — {at_end} (возможен обрыв хода). "
                f"Чаще всего: {top}. Сессии, начатые до установки хуков ({len(codex) - len(under)}), не считаются — "
                "они в «Ограничениях данных».")
        ev, seen = [], set()
        for S, c, _ in lost:
            if S["sid"] not in seen and len(ev) < 8:
                seen.add(S["sid"])
                ev.append({"sid": S["sid"], "line": c.get("_line"), "at": T.iso(c["at"]), "text": f"нет PostToolUse · {label(c)}"})
        out.append(_finding(
            "codex-post-lost", "warn", "Найти, почему Codex не присылает PostToolUse для части вызовов", what, ev, sorted(per),
            impact={"value": f"{len(lost)} вызовов", "label": "без результата"}, impact_by_session=dict(per), impact_unit="вызовов",
            where="Codex → хук PostToolUse; ~/.codex/hooks.json — как его пишет hottell install (install.go, codexEvents)",
            snip=("Диагностика: в новой сессии Codex вызвать инструмент из списка выше и сравнить в otel_logs "
                  "countIf(Body='agent.hook.PreToolUse') и countIf(Body='agent.hook.PostToolUse') по его tool_use_id. "
                  "Post нет — проверить, вызывает ли Codex PostToolUse для такого инструмента. Post есть в debug-логе hottell, "
                  "но нет в ClickHouse — искать потерю в спуле или дрейнере."),
            readiness="needs_spec",
            alternative_causes=["Codex не вызывает PostToolUse для части инструментов (MCP-коннекторы и т. п.).",
                                "Вызов оборван: ход прерван или окно закрыто до результата.",
                                "Потеря событий в спуле или дрейнере hottell."],
            verification="В новых сессиях доля вызовов без PostToolUse — меньше 5 %.",
            scope="collection"))
```

3e. В блоке `# (b)` заменить всё от `if codex_sids and all_events:` до конца его `out.append(...)`:

```python
    missing = [S for S in codex_sids if S not in linked_sessions]
    if missing and all_events:
        ep = ", ".join(f"{k} — {v}" for k, v in endpoints.most_common(3))
        what = (f"Нативный OTel Codex: {all_events} событий, из них с conversation.id — {linked_events}. "
                f"codex.api_request — {api_total} (с conversation.id — {api_linked}; endpoint: {ep or '—'}), токенов в них нет. "
                f"Токены с привязкой к сессии (codex.sse_event response.completed) есть только у {len(linked_sessions)} из "
                f"{len(all_codex)} сессий Codex"
                + (f"; ещё у {len(covered)} токены за ход взяты из журнала сессии через hottell" if covered else "")
                + f". У остальных {len(missing)} токены, число ответов модели и стоимость здесь null.")
        ev = [{"sid": S["sid"], "line": 1, "at": T.iso(S["start"]), "text": "у этой сессии в OTel нет токенов с conversation.id"}
              for S in sorted(missing, key=lambda S: -len(S["calls"]))[:5]]
        out.append(_finding(
            "codex-otel-unlinked", "warn", "Связать нативный OTel Codex с сессиями", what, ev, [S["sid"] for S in missing],
            impact={"value": f"{len(missing)} сессий", "label": "без токенов и стоимости"},
            impact_by_session={S["sid"]: 1 for S in missing}, impact_unit="сессий",
            where="~/.codex/config.toml → [otel], [otel.exporter.otlp-http]; сервисы Codex Desktop / codex-app-server",
            snip=("Диагностика: в одной короткой сессии Codex Desktop проверить, какие события codex.* приходят с conversation.id "
                  "и есть ли codex.sse_event с kind=response.completed от codex-app-server; сравнить с документацией Codex по OTel."),
            readiness="needs_spec",
            alternative_causes=["codex-app-server (десктоп) не отправляет в OTel события с токенами ответа модели.",
                                "События с токенами приходят без conversation.id.",
                                "Экспорт логов включён только у части процессов Codex."],
            verification="У новой сессии Codex есть события с её conversation.id и токенами ответа.",
            scope="collection"))
```

3f. В блоке `# (c)` в вызов `_finding("codex-bash-exit", …)` добавить `readiness="needs_spec", scope="collection", impact_by_session=dict(collections.Counter(c["_sid"] for c in no_code)), impact_unit="результатов",` (`c["_sid"]` ставит `build_dataset` до вызова карточек).

**Шаг 4.** `python3 -m unittest` → OK.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Data-health cards: lost PostToolUse only under hooks with an expiring grace, one number for OTel

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T8: Карточки по работе — гипотезы с тем, что проверить до правки

Только сессии `kind != "system"`. Все — `readiness: "hypothesis"`; правка в карточке — кандидат, «Что проверить до правки» обязательно; проверка пользы — на сопоставимых задачах, исход и стоимость, а не метрика, которую улучшает простое дробление работы.

| Карточка | Когда |
|---|---|
| «Проверить, мешает ли сжатие контекста в длинных сессиях» | сессия с ≥ 3 сжатиями или двумя за ≤ 10 мин |
| «Проверить, требует ли инструкция перечитывать файлы целиком» | ≥ 3 эпизодов перечитывания по правилу T4 |
| «Разобрать эпизоды «…»» | retry, thrash, mcpfail, coldcache, wait — ≥ 3 эпизодов |

**Файлы:** `ui/builder/live.py` (перед `build_findings`); тест — `test_live.py`.

**Шаг 1. Падающий тест:**

```python
class WorkFindingsTest(unittest.TestCase):
    def test_cards_are_hypotheses_and_skip_service_sessions(self):
        t1, t2, t3 = (T0 + dt.timedelta(hours=h) for h in (1, 2, 3))
        work, sug, close, spread = _sid(T0, "3"), _sid(t1, "4"), _sid(t2, "b"), _sid(t3, "c")

        def session(t, sid, prompt, gaps_s):
            rows = [_hook(_ts(t, 0), "SessionStart", sid, source="startup"),
                    _hook(_ts(t, 1), "UserPromptSubmit", sid, turn_id="T1", prompt=prompt),
                    *_call(t, sid, 10, f"{sid[-4:]}-1", '{"command":"ls"}', turn_id="T1")]
            return rows + [_hook(_ts(t, 100 + g), "PostCompact", sid, turn_id="T1", trig="auto") for g in gaps_s]

        hooks = (session(T0, work, "Собери отчёт по продажам", [0, 60, 120])            # три сжатия
                 + session(t1, sug, "# Overview\nGenerate 0 to 3 hyperpersonalized suggestions", [0, 60, 120])
                 + session(t2, close, "Почини сборку", [0, 5 * 60])                       # два сжатия за 5 мин
                 + session(t3, spread, "Обнови README", [0, 40 * 60]))                    # два сжатия, далеко — нет
        ds, _ = run(hooks)
        long = next(f for f in ds["findings"] if f["pattern_id"] == "compact")
        self.assertEqual(long["sessions"], sorted([work, close]))       # служебная и редкие сжатия не считаются
        self.assertEqual((long["readiness"], long["scope"]), ("hypothesis", "work"))
        self.assertTrue(long["preconditions"])
        self.assertEqual(long["impact_by_session"], {work: 3, close: 2})
        self.assertNotIn("сжатий на сессию", long["verification"])      # не метрика, которую даёт дробление
        self.assertFalse(any(f["readiness"] == "prepared" for f in ds["findings"]))

    def test_reread_card(self):
        sid = _sid(T0, "d")
        hooks = [_hook(_ts(T0, 0), "SessionStart", sid, source="startup"),
                 _hook(_ts(T0, 1), "UserPromptSubmit", sid, turn_id="T1", prompt="Разберись в документах")]
        step = iter(range(10, 10000, 10))
        for name in ("a.md", "b.md", "c.md"):
            for _ in range(3):
                hooks += _call(T0, sid, next(step), f"t{len(hooks)}", json.dumps({"command": f"cat docs/{name}"}),
                               resp="same", turn_id="T1")
        ds, _ = run(hooks)
        f = next(x for x in ds["findings"] if x["pattern_id"] == "reread")
        self.assertEqual((f["readiness"], f["scope"]), ("hypothesis", "work"))
        self.assertTrue(f["preconditions"])
        self.assertEqual(f["impact_by_session"], {sid: 6})              # по 2 лишних чтения на файл
```

Общая карточка «Разобрать эпизоды» тоже обязана сказать, что проверить до правки. В конец `WaitsTest.test_async_question_while_agent_works_is_not_a_stop` (тест из T6: сессии stop и pair дают 3 остановки, значит карточка по ожиданию есть):

```python
        card = next(f for f in ds["findings"] if f["pattern_id"] == "wait")
        self.assertEqual((card["readiness"], card["scope"]), ("hypothesis", "work"))
        self.assertTrue(card["preconditions"])
```

(Эти строки — здесь, а не в T6: `scope` появляется в T7, `preconditions` у общей карточки — в этой задаче; в T6 тест упал бы на `KeyError: 'scope'`.)

**Шаг 2.** `python3 -m unittest test_live.WorkFindingsTest test_live.WaitsTest` → `StopIteration` во всех трёх: карточек по работе ещё нет. (Если реализовать T8 без `preconditions` у общей карточки, `WaitsTest` падает с `AssertionError: [] is not true`.)

**Шаг 3. Реализация.** Перед `def build_findings`:

```python
COMPACT_MANY = 3
COMPACT_CLOSE_MIN = 10.0
HANDOFF_CANDIDATE = ("Кандидат правки — только если разбор подтвердит, что после сжатия терялось нужное:\n"
                     "1. Когда задача закончена или было 2–3 сжатия, попросить агента записать состояние в docs/handoff.md: "
                     "что сделано, что дальше, какие файлы и команды.\n"
                     "2. Продолжить в новой сессии с репликой «продолжи по docs/handoff.md».")
REREAD_CANDIDATE = ("Кандидат правки — только если в AGENTS.md/CLAUDE.md действительно есть требование перечитывать этот файл: "
                    "«файл целиком — один раз за сессию и после сжатия контекста; перед решением — только нужный раздел».")


def _plain(e):
    return {k: v for k, v in e.items() if k != "_cost"}


def work_findings(users):
    """Гипотезы о вашей работе (сессии kind != system): у каждой — что проверить до правки и как мерить пользу."""
    out = []
    # (a) сжатия в длинных сессиях: число сжатий само по себе не доказывает потерю деталей
    longs = []
    for S in users:
        at = [r["at"] for r in S["compacts"]]
        close = [T.minutes(a, b) for a, b in zip(at, at[1:]) if T.minutes(a, b) <= COMPACT_CLOSE_MIN]
        if len(at) >= COMPACT_MANY or close:
            longs.append((S, close))
    if longs:
        n = sum(len(S["compacts"]) for S, _ in longs)
        S0, close0 = max(longs, key=lambda x: len(x[0]["compacts"]))
        subs = len({c["aid"] for c in S0["calls"] if c["aid"]})
        what = (f"Сессий с тремя и более сжатиями контекста или сжатиями подряд — {len(longs)}, сжатий в них — {n}. "
                f"Больше всего у {S0['sid'][:8]}: {T.minutes(S0['start'], S0['end']) / 60:.1f} ч, вызовов — {len(S0['calls'])}, "
                f"субагентов — {subs}, сжатий — {len(S0['compacts'])}"
                + (f", два сжатия с разницей {min(close0):.0f} мин" if close0 else "")
                + ". Мешало ли это работе, по событиям не видно — нужен разбор эпизодов.")
        ev = [{"sid": S["sid"], "line": r.get("_line"), "at": T.iso(r["at"]),
               "text": f"сжатие {k + 1}/{len(S['compacts'])} ({r.get('trig') or 'триггер не указан'})"}
              for S, _ in longs for k, r in enumerate(S["compacts"])][:8]
        out.append(_finding(
            "work-long-session", "warn", "Проверить, мешает ли сжатие контекста в длинных сессиях", what, ev,
            sorted(S["sid"] for S, _ in longs),
            impact={"value": f"{n} сжатий", "label": "в длинных сессиях"},
            impact_by_session={S["sid"]: len(S["compacts"]) for S, _ in longs}, impact_unit="сжатий",
            where="Привычка работы с длинными задачами", snip=HANDOFF_CANDIDATE, kind="habit", pattern_id="compact",
            preconditions=["Открыть эпизоды после сжатий: перечитывал ли агент уже прочитанное, переспрашивал ли, повторял ли сделанное.",
                           "Найти сопоставимые задачи без сжатий и сравнить исход и стоимость."],
            alternative_causes=["Задача одна и большая — сжатия неизбежны и не мешают.",
                                "Сжатие вызывают большие ответы инструментов, а не длина работы."],
            verification=("На сопоставимых задачах после правки исход не хуже, стоимость не выше, повторов после сжатия меньше. "
                          "Одно уменьшение числа сжатий пользой не считается: его даёт простое дробление работы.")))
    # (b) перечитывание по строгому правилу T4
    rr = [(S, it) for S in users for it in S["friction"]["reread"]]
    if len(rr) >= T.REREAD_MIN:
        files = collections.defaultdict(lambda: {"extra": 0, "sessions": set()})
        for S, it in rr:
            f = files[os.path.basename(it["path"])]
            f["extra"] += it["extra"]
            f["sessions"].add(S["sid"])
        top = sorted(files.items(), key=lambda kv: -kv[1]["extra"])[:4]
        per = collections.Counter()
        for S, it in rr:
            per[S["sid"]] += it["extra"]
        ktok = sum(it["bytes"] for _, it in rr) / 4 / 1000
        what = ("Один агент перечитывал то же место файла и получал тот же ответ, без сжатия и правки между чтениями: "
                f"эпизодов — {len(rr)}, сессий — {len(per)}, лишних чтений — {sum(per.values())} (≈ {ktok:.0f} тыс. токенов). "
                "Чаще всего: " + "; ".join(f"{name} — {v['extra']} в {len(v['sessions'])} сес." for name, v in top) + ".")
        out.append(_finding(
            "work-reread", "warn", "Проверить, требует ли инструкция перечитывать файлы целиком", what,
            [_plain(it["evidence"][-1]) for _, it in sorted(rr, key=lambda x: -x[1]["extra"])[:8]], sorted(per),
            impact={"value": f"{sum(per.values())} чтений", "label": "лишних"}, impact_by_session=dict(per), impact_unit="чтений",
            where="AGENTS.md / CLAUDE.md проектов этих сессий", snip=REREAD_CANDIDATE, kind="project_rule", pattern_id="reread",
            preconditions=["Найти в инструкциях требование перечитывать эти файлы.",
                           "Открыть эпизоды: нужен ли был файл целиком или хватило бы раздела."],
            alternative_causes=["Агент забыл содержимое после длинной цепочки вызовов — инструкция ни при чём.",
                                "Проверка перед важным шагом — повторное чтение оправдано."],
            verification="На сопоставимых задачах после правки лишних чтений этих файлов вдвое меньше, исход не хуже."))
    # (c) остальные детекторы — «разобрать эпизоды»
    meta = {k: (n, sev) for k, n, sev, _ in T.FRICTION_META}
    for key in ("retry", "thrash", "mcpfail", "coldcache", "wait"):
        occ = [(S, it) for S in users for it in S["friction"].get(key, [])]
        if len(occ) < 3:
            continue
        name, sev = meta[key]
        per = collections.Counter(S["sid"] for S, _ in occ)
        out.append(_finding(
            f"friction-{key}", sev, f"Разобрать эпизоды «{name.lower()}»",
            f"Детектор «{name}» сработал {len(occ)} раз в {len(per)} сессиях. Правило: {LIVE_HOW.get(key) or dict((k, h) for k, _, _, h in T.FRICTION_META)[key]}",
            [_plain(it["evidence"][0]) for _, it in occ[:8]], sorted(per),
            impact={"value": f"{len(occ)} эпизодов", "label": "за окно данных"}, impact_by_session=dict(per), impact_unit="эпизодов",
            snip="Открыть эпизоды по ссылкам и решить, повторяется ли причина; правку описывать только после разбора.",
            preconditions=["Открыть эпизоды по ссылкам и проверить, повторяется ли одна и та же причина.",
                           "Убедиться, что эпизоды не объясняются неполным сбором данных (блок «Здоровье сбора данных»)."],
            pattern_id=key, kind="habit"))
    return out
```

Тело `build_findings`:

```python
def build_findings(sessions, codex_otel, now):
    return collection_findings(sessions, codex_otel, now) + work_findings([S for S in sessions if S["kind"] != "system"])
```

**Шаг 4.** `python3 -m unittest` → OK.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py && git commit -m "Work cards stay hypotheses: what to check before a change, comparable-task verification

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T9: Skills — настоящие имена и честное «доступно»

**Файлы:** `ui/builder/live.py` (`skill_name` после `read_targets`; сбор `acts`; `aggregate_skills`), `ui/static/index.html` (`renderSkills`); тест — `test_live.py`.

**Шаг 1. Падающие тесты.** В `Aggregates.test_skills` заменить `self.assertEqual(sk["available"], 3)` на:

```python
        self.assertEqual(sk["available"], 5)        # снимок {demo, other, third} ∪ открытые {demo, guide, demo-skill}
        self.assertGreaterEqual(sk["available"], len([r for r in sk["rows"] if r["state"] != "unused"]))
```

В `class Helpers`:

```python
    def test_skill_name(self):
        self.assertEqual(L.skill_name("/nope/analytics/skill/SKILL.md"), "analytics")
        self.assertEqual(L.skill_name("/nope/playwright/SKILL.md"), "playwright")
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp, "skill", "SKILL.md")
            p.parent.mkdir()
            p.write_text("---\nname: hottell-analytics\ndescription: разбор\n---\n# x\n", encoding="utf-8")
            self.assertEqual(L.skill_name(str(p)), "hottell-analytics")
```

**Шаг 2.** → `3 != 5` и `AttributeError: … skill_name`.

**Шаг 3.** После `read_targets`:

```python
def skill_name(path):
    """Имя skill: поле name из фронтматтера SKILL.md; иначе папка, а для общих имён (skill, skills) — папка выше."""
    try:
        with open(os.path.expanduser(path), encoding="utf-8") as fh:
            head = fh.read(2048)
        m = re.search(r"(?m)^name:\s*['\"]?([^'\"\n]+?)['\"]?\s*$", head)
        if head.startswith("---") and m:
            return m.group(1).strip()
    except OSError:
        pass
    d = os.path.dirname(path)
    name = os.path.basename(d)
    if name.lower() in ("skill", "skills", ""):
        name = os.path.basename(os.path.dirname(d)) or name
    return name or path
```

В сборе `acts` заменить `"name": os.path.basename(os.path.dirname(p)) or p,` на `"name": skill_name(p),`.

В `aggregate_skills` заменить первую строку `return` на:

```python
    names = set(available) | set(rows)
    return {"available": len(names) if available else None, "snapshot_complete": complete if available else None,
```

В `index.html` в `renderSkills` заменить объявление `const h1=…` (до `;`) на:

```js
  const known=K.snapshot_complete===false?"известных":"доступных";
  const h1=!S.length?"За выбранный период сессий нет.":act.length
    ?`${act.length} ${plural(act.length,"skill","skills","skills")} из ${avail??"?"} ${known} реально открывались в ${S.length} ${plural(S.length,"сессии","сессиях","сессиях")}. ${avail!=null&&avail>act.length&&K.snapshot_complete!==false?`Остальные ${avail-act.length} попадают списком в контекст каждой сессии и ни разу не понадобились.`:""}`
    :`В этих сессиях агент не открыл ни одного skill${avail!=null?` из ${avail} ${known}`:""}: их список всё равно загружается в контекст каждой сессии.`;
```

**Шаг 4.** `python3 -m unittest` → OK.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/builder/live.py ui/builder/test_live.py ui/static/index.html && git commit -m "Skills: names from SKILL.md frontmatter, available = snapshot plus opened

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T10: Интерфейс — «Без служебных · Все» и колонка «Тип»

Автотестов у страницы нет (см. «Изменения после ревью»); проверка — T14.

**Файл:** `ui/static/index.html`.

**Шаг 1. Разметка.** После `<div class="seg" role="group" aria-label="Агент">…</div>`:

```html
      <div class="seg" role="group" aria-label="Служебные сессии" id="kind-seg" hidden>
        <button data-kind="work" aria-pressed="true">Без служебных</button>
        <button data-kind="all" aria-pressed="false">Все</button>
      </div>
```

В шапке таблицы сессий `<th>Исход</th>` → `<th id="sess-outcome-h">Исход</th>`.

**Шаг 2.** В `const st={…}` добавить `kind:"work"`. Заменить `filt`:

```js
function filt(offset=0){if(offset&&!st.days)return [];return (D.sessions||[]).filter(s=>inPeriod(s,st.days,offset)&&(st.agent==="all"||s.agent===st.agent)&&(st.proj==="all"||s.project===st.proj)&&(st.kind==="all"||s.kind!=="system"))}
```

После `const FRI=…`:

```js
const KINDS={user:["ваша","ok"],system:["служебная",""],automation:["расписание","acc"]};
function setKind(k){st.kind=k;document.querySelectorAll("#kind-seg [data-kind]").forEach(x=>x.setAttribute("aria-pressed",String(x.dataset.kind===k)));render()}
```

**Шаг 3.** Заменить `renderSessions`:

```js
function renderSessions(){
  let S=filt();const fr=FRI();if(st.flag)S=S.filter(s=>(s.flags||[]).includes(st.flag));
  const hid=st.kind==="work"?(D.sessions||[]).filter(s=>s.kind==="system"&&inPeriod(s,st.days,0)&&(st.agent==="all"||s.agent===st.agent)&&(st.proj==="all"||s.project===st.proj)&&(!st.flag||(s.flags||[]).includes(st.flag))).length:0;
  const title=st.flag?`Сессии · ${fr[st.flag]?fr[st.flag].name:st.flag} · ${S.length}`:`Сессии · ${S.length}`;
  $("sess-title").innerHTML=esc(title)+(hid?` <button class="link" data-kind="all">+ ${hid} служебных</button>`:"");
  $("sess-outcome-h").textContent=LIVE()?"Тип":"Исход";
  $("sess-tbl").innerHTML=[...S].sort((a,b)=>T(b.start)-T(a.start)).map(s=>{const o=OUTCOME[s.outcome]||OUTCOME.unknown;const k=KINDS[s.kind];
    const last=LIVE()&&k?`<span class="tag ${k[1]}" title="${esc(s.kind_reason||"")}">${k[0]}</span>`:`<span class="tag ${o[1]}">${o[0]}</span>`;
    return `<tr class="row" tabindex="0" data-sid="${s.id}"><td class="mono">${dd(T(s.start))} ${hm(T(s.start))}</td><td><span class="tag ${s.agent}">${s.agent==="claude"?"Claude Code":"Codex"}</span></td><td>${esc(s.project)} <span class="muted mono">${esc(s.branch||"")}</span></td><td class="wrap" style="min-width:220px;max-width:420px">${esc((s.first||"").slice(0,120))}${(s.first||"").length>120?"…":""}</td><td class="r">${fmtD(s.wall_min)}</td><td class="r">${s.prompts??"—"}</td><td class="r">${s.errors??"—"}</td><td class="r">${est$(s.cost_usd,s.cost_basis)}</td><td>${last}</td><td><div class="flags">${(s.flags||[]).map(f=>`<span class="tag ${fr[f]&&fr[f].sev==="bad"?"bad":fr[f]&&fr[f].sev==="warn"?"warn":""}">${esc(fr[f]?fr[f].name:f)}</span>`).join("")}</div></td></tr>`}).join("")||`<tr><td colspan="10" class="empty">Нет сессий под этот фильтр</td></tr>`;
}
```

**Шаг 4.** В общем обработчике кликов сразу после проверки `[data-restore]`:

```js
  const kd=e.target.closest("[data-kind]");if(kd){setKind(kd.dataset.kind);return}
```

В `load()` после заполнения `$("proj")`:

```js
  $("kind-seg").hidden=!(D.sessions||[]).some(s=>s.kind==="system");
```

В `renderFix` в `const basis=…` заменить `${S.length} ${plural(S.length,"сессии","сессий","сессий")} (детекторы` на `${S.length} ${plural(S.length,"сессии","сессий","сессий")} (${st.kind==="work"?"без служебных; ":""}детекторы`.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/static/index.html && git commit -m "Live UI: hide explicit Codex service sessions by default, session type column

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T11: Интерфейс — наблюдаемая оценка вместо нуля

Стоимость — всегда наблюдаемая оценка с «≈» (если есть хоть одна оценка) и долей сессий, где она записана. «≥» не используется: оценка — не доказанная нижняя граница. Внутри сессии стоимость тоже может покрывать не все ходы — об этом говорит подпись.

**Файл:** `ui/static/index.html`.

**Шаг 1. Помощники** — после `const costNote=…`:

```js
// Стоимость под фильтр — наблюдаемая оценка: сумма там, где она записана, и у скольких сессий она есть.
function costCov(S){const k=S.filter(s=>s.cost_usd!=null);return {v:sum(k,s=>s.cost_usd),known:k.length,n:S.length,basis:k.length&&k.every(s=>s.cost_basis==="otel_reported")?"otel_reported":"estimate"}}
const cov$=c=>c.known?est$(c.v,c.basis):"—";
const covNote=c=>`наблюдаемая оценка: стоимость записана у ${c.known} из ${c.n} сессий, внутри сессии может быть неполной`;
function coverageLine(S){
  if(!LIVE()||!S.length)return "";
  const n=S.length,full=S.filter(s=>s.sources&&s.sources.hooks==="recorded").length,calls=sum(S,s=>s.calls),known=sum(S,s=>s.outcome_known),cost=S.filter(s=>s.cost_usd!=null).length;
  return `Полнота данных: хуки с начала сессии — у ${full} из ${n} · исход вызова известен — ${calls?pct(known/calls):"—"} · стоимость записана — у ${cost} из ${n} сессий`;
}
function setCov(id,S){const t=coverageLine(S);$(id).hidden=!t;$(id).textContent=t}
```

CSS в конец `<style>`: `.cov{font-size:12.5px;color:var(--ink-2);background:var(--surface);border:1px solid var(--line);padding:8px 12px}`.

Разметка: первой строкой в `<section … id="v-overview">` — `<div class="cov" id="ov-cov" hidden></div>`; в `<section … id="v-fix">` после `<div class="hero" id="fix-hero"></div>` — `<div class="cov" id="fix-cov" hidden></div>`.

**Шаг 2. `renderOverview`.**

- После `const S=filt(),P=…;` — `const cc=costCov(S);setCov("ov-cov",S);`.
- Заменить строку `const calls=sum(S,s=>s.calls),errs=…;`:
```js
  const calls=sum(S,s=>s.calls),errs=sum(S,s=>s.errors),unk=sum(S,s=>s.unknown_results),pcalls=sum(P,s=>s.calls),perrs=sum(P,s=>s.errors);
  const hasKnown=S.some(s=>s.outcome_known!=null),known=sum(S,s=>s.outcome_known),pknown=sum(P,s=>s.outcome_known);
  const errV=hasKnown?(known?errs/known:null):(calls?errs/calls:null),perrV=hasKnown?(pknown?perrs/pknown:0):(pcalls?perrs/pcalls:0);
```
- KPI расходов: `est$(cost),cost,pcost,true,LIVE()?…:…` → `cov$(cc),cc.v,pcost,true,cc.known<cc.n?covNote(cc):LIVE()?"Claude — по OTel · Codex — оценка по журналу":"по прайсу API · подписка"`.
- KPI ошибок:
```js
    kpi("Ошибки инструментов",errV==null?"—":pct(errV),errV,perrV,true,hasKnown?`среди вызовов с известным исходом (${calls?pct(known/calls):"—"} всех)`:(unk?`ещё ${unk} с неизвестным исходом`:""));
```
- В `spend-sub` `${est$(cost)}` → `${cov$(cc)}${cc.known<cc.n?" · "+covNote(cc):""}`.
- «Куда уходят деньги» — заменить три строки `const bp={}…`/`$("by-project")…`:
```js
  const bp={};S.forEach(s=>(bp[s.project]=bp[s.project]||[]).push(s));
  const bpa=Object.entries(bp).map(([k,ss])=>[k,costCov(ss)]).sort((a,b)=>b[1].v-a[1].v);const mx=bpa[0]?bpa[0][1].v||1:1;
  $("by-project").innerHTML=bpa.map(([k,c])=>`<div class="li"><span class="t">${esc(k)}</span><div class="bar"><i style="width:${c.v/mx*100}%"></i></div><span class="r num" title="${esc(covNote(c))}">${cov$(c)}${c.known&&c.known<c.n?` <span class="muted">${c.known}/${c.n}</span>`:""}</span></div>`).join("")||`<div class="empty">Нет сессий</div>`;
```
- «Результат в git»: `cost:0` → `cs:[]`; `if(s.commits)g.cost+=s.cost_usd||0` → `if(s.commits)g.cs.push(s)`; ячейка `${g.c?est$(g.cost/g.c):"—"}` → `${g.c&&g.cs.every(s=>s.cost_usd!=null)?est$(sum(g.cs,s=>s.cost_usd)/g.c):"—"}`.

**Шаг 3. «Трение».** `<th class="r">$ в этих сессиях</th>` → `<th class="r">$ эпизодов</th>`. В `renderFriction` `const c=sum(S.filter(s=>ss.includes(s.id)),s=>s.cost_usd);` → `const c=f.cost_usd!=null&&ss.length===(f.sessions||[]).length?f.cost_usd:null;`, ячейка `${ss.length?est$(c):"—"}` → `${c!=null?est$(c):"—"}`.

**Шаг 4. Skills.** В строке таблицы `${r.corrections_after??"—"}` → `${S.some(s=>s.corrections!=null)?(r.corrections_after??"—"):"—"}`.

**Шаг 5.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/static/index.html && git commit -m "Live UI: observed estimates with their coverage instead of null as zero

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T12: Интерфейс — выводы отдельно от здоровья данных, карточки под фильтр

**Файл:** `ui/static/index.html`.

**Шаг 1. Разметка.** В `<section … id="v-fix">` после `<div id="fix-list" …></div>` — `<div id="fix-health"></div>`.

**Шаг 2. Карточка под выбранные сессии** — перед `function findingCard`:

```js
// Эффект и доказательства карточки — по выбранным сессиям; текст карточки остаётся общим, это сказано явно.
function impactFor(f,ids){
  const all=f.sessions||[],sel=all.filter(x=>ids.has(x));
  if(!ids||!f.impact_by_session||sel.length===all.length)return {impact:f.impact,note:""};
  const v=sel.reduce((n,sid)=>n+(f.impact_by_session[sid]||0),0);
  return {impact:{value:`${v} ${f.impact_unit||""}`.trim(),label:`${f.impact?f.impact.label:""} · в выбранных сессиях`},
          note:`Текст карточки — по всем ${all.length} ${plural(all.length,"сессии","сессиям","сессиям")}; в выбранных — ${sel.length}.`};
}
```

В `findingCard` сигнатуру `function findingCard(f,i)` → `function findingCard(f,i,ids)`, первой строкой тела:

```js
  const {impact,note}=impactFor(f,ids),ev=ids?(f.ev||[]).filter(e=>ids.has(e.sid)):(f.ev||[]);
```

Далее в `findingCard`: `f.ev&&f.ev.length?…evidenceRows(f.ev)…` → `ev.length?…evidenceRows(ev)…`; `${f.impact?esc(f.impact.value):"—"}` → `${impact?esc(impact.value):"—"}`; `${f.impact?esc(f.impact.label):"эффект не измерен"}` → `${impact?esc(impact.label):"эффект не измерен"}`; после `<p class="what">${esc(f.what||"")}</p>` добавить `${note?`<p class="muted" style="margin:0;font-size:12px">${esc(note)}</p>`:""}`.

В `renderSkills`: `F.map((f,i)=>findingCard(f,"sk"+i))` → `F.map((f,i)=>findingCard(f,"sk"+i,ids))`.

**Шаг 3. `renderFix`.** Первую строку заменить:

```js
  const S=filt(),ids=idsOf(S),all=sortFindings(visibleFindings(S)),F=all.filter(f=>f.scope!=="collection"),C=all.filter(f=>f.scope==="collection");
  setCov("fix-cov",S);
```

`shown.map((f,i)=>findingCard(f,i))` → `shown.map((f,i)=>findingCard(f,i,ids))`. Перед `const done=…`:

```js
  $("fix-health").innerHTML=C.length?`<details class="more p"><summary>Здоровье сбора данных · ${C.length} — что hottell пока записывает не полностью; к вашей работе не относится</summary><div style="display:grid;gap:12px;margin-top:12px">${C.map((f,i)=>findingCard(f,"h"+i,ids)).join("")}</div></details>`:"";
```

**Шаг 4.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/static/index.html && git commit -m "Live UI: work hypotheses first, data health folded; cards recount impact for the filter

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T13: Документация

**Шаг 1.** В конец раздела «Живая версия (`live.py`)» в `ui/CONTRACT.md`:

```markdown
Поля сверх общей схемы:

- `Session.kind` — `system` (явная служебная задача Codex: подсказки Desktop, память; папка `~/.codex/memories`) |
  `automation` (реплика `<heartbeat>`) | `user` (остальное, в том числе короткие сессии и сессии без реплик);
  `Session.kind_reason` — сработавшее правило. Интерфейс по умолчанию скрывает только `system`.
- `Session.outcome_known` — вызовы с известным исходом (успех или ошибка).
- `Session.waits` — число вопросов вам; `wait_min` — объединённые интервалы, когда основной агент после вопроса не делал
  ни одного вызова до вашей реплики. Async-вопросы, пока агент работает, не считаются; эпизоды `wait` — только такие остановки.
- `Friction.by_session` — `{"<sid>": эпизодов}`; интерфейс считает эпизоды по нему.
- `reread`: одно место файла ≥ 3 раз подряд одним агентом с одинаковым хэшем всего ответа (`resp_hash`, cityHash64 в
  ClickHouse), без сжатия и правки между чтениями; без результата, на несколько файлов и `tail` — не считаются.
  Эпизод несёт `path`, `extra`, `bytes`.
- `Finding.scope` — `work` | `collection` (здоровье сбора данных, показывается свёрнутым). Карточки `work` — только по
  сессиям `kind != system`, все `readiness: hypothesis` с `preconditions`. `impact_by_session` и `impact_unit` —
  вклад сессий для пересчёта эффекта под фильтр.
- `skills.available` — объединение снимка и открытых skills; при `snapshot_complete = false` — «известных».
  Имя skill — `name` из фронтматтера `SKILL.md`, иначе папка.
```

**Шаг 2.** В `ui/README.md` под таблицей «Две версии»:

```markdown
В живой версии по умолчанию скрыты только явные служебные задачи Codex (подсказки Desktop, память) — кнопка «Все».
«Что исправить» показывает гипотезы о работе — у каждой сказано, что проверить до правки, — и отдельно свёрнутое
«Здоровье сбора данных». Рядом с цифрами указано, у какой доли сессий и вызовов они известны; стоимость — наблюдаемая оценка.
```

**Шаг 3.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add ui/CONTRACT.md ui/README.md && git commit -m "Document live session kinds, waits, coverage fields and finding scope

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### T14: Проверка

**Шаг 1. Тесты.**
```bash
cd /Users/elvismusli/Life/projects/ai-hottell/ui/builder && python3 -m unittest
cd /Users/elvismusli/Life/projects/ai-hottell/ui && go vet ./... && go test ./...
cd /Users/elvismusli/Life/projects/ai-hottell/hottell && go vet ./... && go test ./...
```

**Шаг 2. Стенд.** Если ClickHouse не отвечает (`curl -s 'http://127.0.0.1:8123/?query=SELECT%201'`), поднимать **существующие** контейнеры: `docker start ai-hottell-local-clickhouse-1 ai-hottell-local-collector-1`. Не запускать `docker compose -f local/docker-compose.yml up -d` из корня репозитория: он создаёт второй пустой проект `local` (известная ловушка; исправление — отдельная задача про `name:` в compose).

**Шаг 3. Фиксированное окно** — сборка, сравнимая с прототипом:
```bash
cd /Users/elvismusli/Life/projects/ai-hottell && python3 ui/builder/live.py --until 2026-10-01T02:00:00Z --out /tmp/hottell-live-check
```
Ориентиры прототипа (приложение) — не цель, а проверка: заметное расхождение разбирать, а не подгонять пороги.

| Величина | Прототип |
|---|---|
| Сессий: system / automation / user | 10 / 1 / 20 |
| Эпизодов перечитывания по правилу T4 (без служебных) | 2 — карточки нет (порог 3) |
| Остановок агента на вопросе | 0 |
| Карточка «нет PostToolUse» (все сессии под хуками, включая служебные) | 54 из 1018, из них 15 в последние 2 мин своей сессии — все 15 в служебных (до правки D «незакрытый ход живой сессии» было 56 из 1047) |
| То же без служебных и без последних 2 мин сессии | 36 из 937 |
| OTel-карточка: плашка и текст | 22 и 22 |

Цифры 54/1018 и 36/937 — одно и то же, посчитанное по-разному: карточка здоровья данных честно включает служебные сессии (сбор у них тот же). Итоговая сборка ветки (`live.py --until 2026-10-01T02:00:00Z`, 110 тестов OK) дала эти величины; типы сессий, перечитывание, остановки и OTel совпали с прототипом.

**Шаг 4. Обе версии во встроенном браузере** (`sh ui/start-local.sh`, затем `get_page_text`/`read_page`). Инварианты, а не заранее выбранные выводы:

| Где | Инвариант |
|---|---|
| :8801 · все вкладки | Ни одной карточки «готово к применению»; у каждой карточки «по работе» заполнено «Что проверить до правки» |
| :8801 · Сессии | «Без служебных»: нет строк с типом «служебная»; «Все»: их число = числу в ссылке «+ N служебных»; короткие и безрепличные сессии видны |
| :8801 · Что исправить | Карточки здоровья данных — только в свёрнутом блоке; в тексте карточек нет «async: true»; у OTel-карточки число на плашке = числу в тексте |
| :8801 · фильтр проекта | При выборе одного проекта у карточки с сессиями других проектов — пометка «в выбранных — M» и эффект по выбранным; доказательства — только выбранных сессий |
| :8801 · Обзор | «Расходы» с «≈» и подписью о доле сессий, где стоимость записана (если не у всех); в «Результат в git» нет «$0.00» там, где стоимость неизвестна |
| :8801 · Трение / Обзор / Что исправить | Число эпизодов одного сигнала одинаково на всех трёх вкладках |
| :8801 · Skills | «… из N известных/доступных», N ≥ числа открытых; нет skill с именем `skill` |
| :8800 «Разобранные сессии» | Нет переключателя «Без служебных · Все»; колонка «Исход»; «Что исправить» без свёрнутого блока; страница без ошибок в консоли |

**Шаг 5.** Скриншоты «Что исправить» и «Обзора» обеих версий для PR.

---

### T15: PR

```bash
cd /Users/elvismusli/Life/projects/ai-hottell && git add docs/specs/2026-10-01-live-honest-dashboard.plan.md && git commit -m "Plan: honest live dashboard

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin claude/live-honest-fix
gh pr create --base camp --title "Живой дашборд: честные цифры, гипотезы без завышенной достоверности" --body-file <файл с описанием>
```

В описании: таблица «Что не так сейчас», «Изменения после ревью», скриншоты T14, ограничения (пороги детекторов выбраны по одному дню данных; эффект не измерен; автотеста страницы нет). Последняя строка: `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

---

## Приложение: как воспроизвести цифры

Окно: события до `2026-10-01 02:00:00` UTC, ClickHouse стенда (`ai-hottell-local`), база `otel`.

**Запрос 1 — кто читал CONSTITUTION.md в 01a0f058** (результат: субагенты — 76 чтений, 55 агентов; основной — 11):

```sql
SELECT if(LogAttributes['agent_id'] = '', 'основной', 'субагент') who, count() reads,
       uniqExact(LogAttributes['agent_id']) agents
FROM otel_logs
WHERE ServiceName = 'agent-hooks' AND Body = 'agent.hook.PreToolUse'
  AND LogAttributes['session_id'] LIKE '01a0f058%' AND Timestamp < '2026-10-01 02:00:00'
  AND LogAttributes['tool_input'] LIKE '%CONSTITUTION.md%'
  AND match(LogAttributes['tool_input'], '(cat|sed -n|head|tail) ')
GROUP BY who
```

**Запрос 2 — хэш всего ответа для сравнения чтений** (проверка выражения из T4; одиночные чтения CONSTITUTION.md: у основного агента 01a0f058 — 4 чтения, 1 уникальный ответ):

```sql
WITH replaceRegexpOne(LogAttributes['tool_response'],
       '^(?:(?:Chunk ID|Wall time|Process exited with code|Original token count)[^\\n]*\\n)*(?:Output:\\n)?', '') AS body
SELECT substring(LogAttributes['session_id'], 1, 8) s, if(LogAttributes['agent_id'] = '', 'main', 'sub') who,
       count() reads, uniqExact(cityHash64(body)) bodies
FROM otel_logs
WHERE Body = 'agent.hook.PostToolUse' AND LogAttributes['tool_input'] LIKE '%CONSTITUTION.md%'
  AND LogAttributes['tool_input'] NOT LIKE '%&&%' AND LogAttributes['tool_input'] NOT LIKE '%;%'
  AND Timestamp < '2026-10-01 02:00:00'
GROUP BY s, who HAVING reads >= 3 ORDER BY reads DESC
```

**Прототип правил** (типы сессий, перечитывание по хэшу, остановки на вопросе, потери PostToolUse) — скрипт `proto.py` этой сессии повторял правила T2, T4, T6, T7 поверх текущего `live.py` с добавленным `resp_hash` и `--until 2026-10-01T02:00:00Z`. Результаты — таблица в T14, шаг 3. После реализации T14 шаг 3 даёт те же величины уже из настоящего `live.py`.

**Ожидания на вопросе** (все 8 — `request_user_input_async`): за «258 мин ожидания» в 01a0f058 — 1072 вызова (669 основного агента), за «26,5 мин» — 277 (127), за «11,1 мин» в 01a0f142 — 42 (42). Ни в одном случае основной агент не стоял между вопросом и ответом.
