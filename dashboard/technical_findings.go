package main

import "fmt"

// TechnicalFinding describes only what the recorded Hooks/OTel evidence can
// establish. It never infers task outcome or agent quality from event counts.
type TechnicalFinding struct {
	Code     string `json:"code"`
	Finding  string `json:"finding"`
	Evidence string `json:"evidence"`
	Meaning  string `json:"meaning"`
}

func technicalFindings(retro *TelemetryReport, hook *Session, hookAvailable bool, otel *OTelReport, otelAvailable bool) []TechnicalFinding {
	findings := make([]TechnicalFinding, 0, 5)
	if retro != nil && retro.SourceCoverage["hook"].Status == "missing" && retro.SourceCoverage["otel"].Status == "missing" {
		findings = append(findings, TechnicalFinding{
			Code: "historical-capture-gap", Finding: "В локальном историческом срезе нет записанных Hooks и OTel, привязанных к этой сессии.",
			Evidence: "Исторический срез: Hooks — нет; OTel — нет. Хронология восстановлена из транскрипта отдельно.",
			Meaning:  "По техническому потоку нельзя проверить ход исходной сессии, её длительность и ошибки инструментов.",
		})
	}
	if hookAvailable && hook != nil && hook.Events > 0 {
		findings = append(findings, TechnicalFinding{
			Code: "hook-recorded", Finding: "В текущем хранилище есть Hook-события с точным ID сессии.",
			Evidence: fmt.Sprintf("agent-hooks: %d событий, %d вводов, %d начал и %d завершений вызовов; период записи %s — %s.", hook.Events, hook.Prompts, hook.ToolStarts, hook.ToolEnds, hook.First, hook.Last),
			Meaning:  "Можно анализировать активность сборщика за этот период. Эти события сами по себе не подтверждают результат задачи.",
		})
		if hook.HasStart == 0 || hook.HasEnd == 0 {
			findings = append(findings, TechnicalFinding{
				Code: "hook-boundary-gap", Finding: "В записанных Hooks нет полной пары начала и конца сессии.",
				Evidence: fmt.Sprintf("agent-hooks: %d событий; SessionStart=%d, SessionEnd=%d; период записи %s — %s.", hook.Events, hook.HasStart, hook.HasEnd, hook.First, hook.Last),
				Meaning:  "Длительность всей сессии из Hooks сейчас не вычисляется. Это не доказывает, что задача завершилась с ошибкой.",
			})
		}
		if hook.ToolStarts > hook.ToolEnds {
			findings = append(findings, TechnicalFinding{
				Code: "hook-tool-end-gap", Finding: "Записано больше начал вызовов инструментов, чем завершений.",
				Evidence: fmt.Sprintf("agent-hooks: PreToolUse=%d, PostToolUse/PostToolUseFailure=%d.", hook.ToolStarts, hook.ToolEnds),
				Meaning:  "По этим событиям нельзя надёжно посчитать завершённые вызовы и частоту ошибок. Причина расхождения требует проверки настроек и периода сбора.",
			})
		}
	}
	if otelAvailable && otel != nil && otel.Queried {
		if otel.logCount() > 0 {
			findings = append(findings, TechnicalFinding{
				Code: "otel-logs-recorded", Finding: "В текущем хранилище есть OTel логи, связанные с точным ID сессии.",
				Evidence: fmt.Sprintf("OTel: %d логов в %d группах событий; %d трейсов, %d записей метрик.", otel.logCount(), len(otel.LogGroups), otel.Traces, otel.MetricRecords),
				Meaning:  "Можно рассматривать типы и время зарегистрированных событий. Логи сами по себе не подтверждают результат задачи.",
			})
		}
		if otel.Coverage == "complete" && otel.Traces == 0 && otel.MetricRecords == 0 {
			findings = append(findings, TechnicalFinding{
				Code: "otel-trace-metric-gap", Finding: "В текущем хранилище для этого ID нет связанных OTel трейсов и метрик.",
				Evidence: fmt.Sprintf("Точное совпадение ID: %d логов, %d трейсов, %d записей метрик.", otel.logCount(), otel.Traces, otel.MetricRecords),
				Meaning:  "Глобальные данные без ID нельзя приписать сессии; задержки и стоимость по ним здесь не оцениваются.",
			})
		}
	}
	return findings
}
