package main

import (
	"fmt"
	"strings"
)

// Регистрация MCP-сервера у агентов — та же хирургия, что с хуками: только
// своя запись, чужие серверы и ключи не трогаются, без бэкапов.
//   Claude Code: ~/.claude.json, mcpServers.hottell (пользовательский уровень).
//   Codex:       ~/.codex/config.toml, таблица [mcp_servers.hottell].

const mcpName = "hottell"
const codexMCPTable = "mcp_servers." + mcpName

func codexMCPBlock(bin string) string {
	return "[" + codexMCPTable + "]\ncommand = " + tomlQuote(bin) + "\nargs = [\"mcp\"]\n"
}

func registerMCP(p paths, bin string) error {
	doc, err := readJSONFile(p.claudeJSON)
	if err != nil {
		return err
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		doc["mcpServers"] = servers
	}
	want := map[string]any{"type": "stdio", "command": bin, "args": []any{"mcp"}, "env": map[string]any{}}
	if cur, ok := servers[mcpName].(map[string]any); ok && cur["command"] == bin {
		fmt.Println("MCP Claude Code: уже зарегистрирован")
	} else {
		servers[mcpName] = want
		if err := writeJSONFile(p.claudeJSON, doc); err != nil {
			return err
		}
		fmt.Println("MCP Claude Code: зарегистрирован в", p.claudeJSON)
	}

	text, err := readTextFile(p.codexConfig)
	if err != nil {
		return err
	}
	block := codexMCPBlock(bin)
	if strings.TrimSpace(tomlFamily(text, codexMCPTable)) == strings.TrimSpace(block) {
		fmt.Println("MCP Codex: уже зарегистрирован")
		return nil
	}
	if err := writeTextFile(p.codexConfig, tomlSetFamily(text, codexMCPTable, block)); err != nil {
		return err
	}
	fmt.Println("MCP Codex: зарегистрирован в", p.codexConfig)
	return nil
}

func unregisterMCP(p paths) error {
	doc, err := readJSONFile(p.claudeJSON)
	if err != nil {
		fmt.Println(p.claudeJSON, "—", err, "(пропущен)")
	} else if servers, ok := doc["mcpServers"].(map[string]any); ok {
		if _, ok := servers[mcpName]; ok {
			delete(servers, mcpName)
			if err := writeJSONFile(p.claudeJSON, doc); err != nil {
				return err
			}
			fmt.Println(p.claudeJSON, "— MCP hottell снят")
		}
	}
	text, err := readTextFile(p.codexConfig)
	if err != nil {
		return err
	}
	if out, ok := tomlRemoveFamily(text, codexMCPTable); ok {
		if err := writeTextFile(p.codexConfig, out); err != nil {
			return err
		}
		fmt.Println(p.codexConfig, "— MCP hottell снят")
	}
	return nil
}

func mcpRegistered(p paths) (claude, codex bool) {
	if doc, err := readJSONFile(p.claudeJSON); err == nil {
		if servers, ok := doc["mcpServers"].(map[string]any); ok {
			_, claude = servers[mcpName]
		}
	}
	if text, err := readTextFile(p.codexConfig); err == nil {
		codex = tomlFamily(text, codexMCPTable) != ""
	}
	return
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}
