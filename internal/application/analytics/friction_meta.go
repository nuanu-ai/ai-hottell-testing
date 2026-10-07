package analytics

// MaxFrictionEvidence is how many evidence lines a friction signal keeps over all its episodes:
// the newest, as the colleague's aggregate_friction.
const MaxFrictionEvidence = 60

// frictionTextMax is the length an evidence text is cut to, as the colleague's _friction.
const frictionTextMax = 200

// FrictionEpisode is one episode of a friction signal of a session, the item of the colleague's
// S["friction"][key]. Evidence.Line stays 0 until the session timeline (HT-257) numbers the events.
type FrictionEpisode struct {
	// Path, Extra and Bytes are a reread's: the file, the reads past the first and their bytes.
	Path  string
	Extra int
	Bytes int64
	// Evidence is what the episode rests on, oldest first.
	Evidence []Evidence
	// Cost is the episode's cost in USD, nil for a signal that has none (coldcache has one).
	Cost *float64
}

// frictionEvidence is one evidence line of the session sid at at.
func frictionEvidence(sid, at, text string) Evidence {
	return Evidence{SID: sid, At: at, Text: Clean(text, frictionTextMax)}
}

// FrictionEvidence is the evidence a friction signal shows for its episodes: their lines, the
// MaxFrictionEvidence newest in time order.
func FrictionEvidence(eps []FrictionEpisode) []Evidence {
	var all []Evidence
	for _, ep := range eps {
		all = append(all, ep.Evidence...)
	}
	return nonNil(Newest(all, MaxFrictionEvidence, evidenceAt))
}

// The keys of the friction signals.
const (
	FrictionRetry      = "retry"
	FrictionThrash     = "thrash"
	FrictionMCPFail    = "mcpfail"
	FrictionColdCache  = "coldcache"
	FrictionCompact    = "compact"
	FrictionReread     = "reread"
	FrictionWait       = "wait"
	FrictionAbort      = "abort"
	FrictionCorrection = "correction"
)

// FrictionSignal is what the dashboard shows of a friction signal beside its episodes: its key,
// name, severity and how it is counted.
type FrictionSignal struct {
	Key  string
	Name string
	Sev  Severity
	How  string
}

// FrictionMeta lists the friction signals in their order, as the colleague's FRICTION_META, with
// how each is counted on the server: his LIVE_HOW text, which reads hooks and native telemetry
// instead of the rollout and review.json of his local stand, and FRICTION_META's own text for
// mcpfail, which LIVE_HOW does not name.
func FrictionMeta() []FrictionSignal {
	return []FrictionSignal{
		{
			FrictionRetry, "Повтор с той же ошибкой", SevBad,
			"Один и тот же вызов (одинаковый tool_input) ≥ 3 раз в окне 10 вызовов, все с ошибкой, без правок между. " +
				"Ошибки Bash у Codex не видны (нет кода выхода) — для них повторы не ловятся.",
		},
		{
			FrictionThrash, "Цикл правка → тест", SevBad,
			"≥ 3 провала тестовой команды подряд с правками между ними (только где ошибка видна: Claude, apply_patch).",
		},
		{FrictionMCPFail, "Сбой MCP", SevBad, "≥ 3 ошибок одного MCP-сервера за сессию (статус failed или isError)."},
		{
			FrictionColdCache, "Холодный кэш", SevWarn,
			"Только Claude Code: ваша реплика после паузы > 5 мин от прошлого api_request; первый запрос после неё " +
				"читает из кэша < 50 % входа. Стоимость — cost_usd этих запросов по данным Claude Code.",
		},
		{FrictionCompact, "Сжатие контекста", SevWarn, "События хуков PreCompact/PostCompact (сжатие посреди работы)."},
		{
			FrictionReread, "Перечитывание файлов", SevWarn,
			"Одно место файла прочитано ≥ 3 раз подряд одним агентом (субагенты — отдельно) с одинаковым ответом " +
				"(хэш записанного ответа), без сжатия контекста и правки файла между чтениями. Чтения без результата " +
				"или с ошибкой, команды на несколько файлов и хвост логов (tail) не считаются.",
		},
		{FrictionWait, "Агент ждал вас", SevInfo, LiveHowWait},
		{FrictionAbort, "Прерванный ход", SevInfo, "Хук Interrupt (Codex); у Claude Code прерывание хуками не записывается."},
		{FrictionCorrection, "Ваша коррекция", SevWarn, "Разметки реплик в живом варианте нет — не считается."},
	}
}

// FrictionKeys are the keys of the friction signals in their order.
func FrictionKeys() []string {
	meta := FrictionMeta()
	keys := make([]string, len(meta))
	for i, m := range meta {
		keys[i] = m.Key
	}
	return keys
}

// FrictionHow is how the signal key is counted; "" for an unknown key.
func FrictionHow(key string) string {
	for _, m := range FrictionMeta() {
		if m.Key == key {
			return m.How
		}
	}
	return ""
}

// Friction is the friction of one session: its episodes by signal key.
type Friction map[string][]FrictionEpisode

// Flags are the keys of the signals the session has episodes of, in the order of FrictionKeys.
func (f Friction) Flags() []string {
	flags := []string{}
	for _, k := range FrictionKeys() {
		if len(f[k]) > 0 {
			flags = append(flags, k)
		}
	}
	return flags
}

// LiveHowWait is how the wait signal is counted, as LIVE_HOW["wait"] of the colleague's builder
// says it, with the permission windows our hooks add.
const LiveHowWait = "Вопрос вам (request_user_input*, AskUserQuestion), пока основной агент не сделал ни одного вызова. " +
	"Обычный вопрос длится до своего результата (или до вашей реплики, если она раньше), async — до вашей " +
	"следующей реплики. Async-вопросы, пока агент продолжает работу, не считаются; пока выполнялся другой вызов " +
	"агента — не простой. Окно разрешения (хук PermissionRequest) длится до следующего события основного агента; " +
	"без него окно Claude (tool_decision от вас) или окно, после которого ничего нет, — простой без длины."
