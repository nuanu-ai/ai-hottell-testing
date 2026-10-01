package mcp

import (
	"context"
	"encoding/json"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// installInput is the empty input of get_install_instructions.
type installInput struct{}

// installOutput is the result of get_install_instructions.
type installOutput struct {
	Command     string   `json:"command"`
	Summary     string   `json:"summary"`
	ManualSteps []string `json:"manual_steps"`
}

// installOutputSchema is the output schema of get_install_instructions as mcp.md gives it:
// manual_steps is a plain array, where a schema inferred from installOutput would allow null.
const installOutputSchema = `{
  "type": "object",
  "required": ["command", "summary", "manual_steps"],
  "additionalProperties": false,
  "properties": {
    "command": {
      "type": "string",
      "description": "Команда установки: curl -fsSL <HT_PUBLIC_ORIGIN>/install.sh | sh. Токенов и ключей в ней нет."
    },
    "summary": {
      "type": "string",
      "description": "Короткий текст для пользователя: что делает установка."
    },
    "manual_steps": {
      "type": "array",
      "description": "Ручные шаги после установки, по порядку.",
      "items": { "type": "string" }
    }
  }
}`

// installSummary tells the user what the installation does.
const installSummary = "Скрипт скачивает бинарь hottell для этого Mac, находит сервер hottell в конфиге Claude Code " +
	"или Codex и запускает фоновый процесс под launchd. Он ставит хуки и нативный OTel агентов по настройкам " +
	"со страницы «Что отправлять». Ключей и токенов в команде нет."

// installStepForeignHooks tells the agent what to do when the installation stopped at hooks
// of others that run the hottell binary: hottell install removes them only with the flag,
// which the user has to agree to.
func installStepForeignHooks(command string) string {
	return "Если установка остановилась на чужих хуках (foreign hooks), которые запускают бинарь hottell, " +
		"покажите пользователю их список из вывода установки. Только с его согласия запустите установку снова " +
		"с флагом, который удаляет эти хуки: " + command + " -s -- --replace-foreign-hooks. " +
		"Конфиги агентов перед удалением сохраняются в резервную копию."
}

// The steps left to the user after the installation, in order.
const (
	installStepRestart = "Перезапустите открытые сессии Claude Code и Codex, чтобы они подхватили хуки и нативный OTel."
	installStepTrust   = "Codex: если hottell status сообщает, что доверие к хукам не записано, выполните /hooks в Codex " +
		"и подтвердите хуки hottell."
)

// addInstall adds the tool get_install_instructions, which answers how to install the hottell
// binary on this machine: the install command on origin, what it does and the manual steps
// left after it. The command carries no key or token: the binary finds the MCP server in the
// agent's config.
func addInstall(server *sdkmcp.Server, origin string) {
	tool := &sdkmcp.Tool{
		Name: "get_install_instructions",
		Description: "Возвращает команду установки бинаря hottell на эту машину, что делает установка " +
			"и ручные шаги после неё. Вызывай, когда пользователь просит поставить телеметрию hottell. " +
			"Покажи пользователю команду и summary; выполнить команду сам можно только после " +
			"подтверждения пользователя, иначе предложи выполнить её в терминале. После установки " +
			"передай пользователю manual_steps.",
		Annotations:  &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
		OutputSchema: json.RawMessage(installOutputSchema),
	}
	command := "curl -fsSL " + origin + "/install.sh | sh"
	out := installOutput{
		Command:     command,
		Summary:     installSummary,
		ManualSteps: []string{installStepForeignHooks(command), installStepRestart, installStepTrust},
	}
	sdkmcp.AddTool(server, tool, func(context.Context, *sdkmcp.CallToolRequest, installInput) (
		*sdkmcp.CallToolResult, installOutput, error,
	) {
		return nil, out, nil
	})
}
