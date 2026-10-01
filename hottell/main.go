package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

const defaultConfigPath = "~/.config/hottell/config.json"
const defaultBinDir = "~/.local/bin"

// hottell — шиппер событий lifecycle-хуков Claude Code и Codex в otel-lab.
//
//	hottell -agent claude|codex [-config path]   хук-режим: stdin -> спул, выход 0
//	hottell drain [-config path]                 фоновый отправщик спула (синглтон)
//	hottell install -token-file path|-token-stdin [-bin-dir dir]
//	hottell token [-config path]                 токен со stdin (без эха) -> проверка -> файл токена
//	hottell uninstall [-bin-dir dir]
//	hottell status [-bin-dir dir]
//	hottell mcp                                  MCP-сервер (stdio) для агентов
//	hottell version
func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "version" {
		if c := commitString(); c != "" {
			fmt.Printf("hottell %s (%s)\n", versionString(), c)
		} else {
			fmt.Println("hottell", versionString())
		}
		return
	}

	switch first(args) {
	case "mcp":
		runMCP()
		return
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		tokenFile := fs.String("token-file", "", "файл с bearer-токеном приёма")
		tokenStdin := fs.Bool("token-stdin", false, "прочитать токен со stdin")
		binDir := fs.String("bin-dir", defaultBinDir, "куда положить бинарь")
		_ = fs.Parse(args[1:])
		if err := runInstall(defaultPaths(*binDir), *tokenFile, *tokenStdin); err != nil {
			fmt.Fprintln(os.Stderr, "install:", err)
			os.Exit(1)
		}
		return
	case "uninstall":
		fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
		binDir := fs.String("bin-dir", defaultBinDir, "откуда убрать бинарь")
		_ = fs.Parse(args[1:])
		if err := runUninstall(defaultPaths(*binDir)); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall:", err)
			os.Exit(1)
		}
		return
	case "token":
		fs := flag.NewFlagSet("token", flag.ExitOnError)
		cfgPath := fs.String("config", defaultConfigPath, "путь к конфигу")
		_ = fs.Parse(args[1:])
		os.Exit(runTokenCmd(context.Background(), os.Stdin, os.Stdout, os.Stderr, loadConfig(*cfgPath)))
	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		binDir := fs.String("bin-dir", defaultBinDir, "каталог бинаря")
		_ = fs.Parse(args[1:])
		runStatus(defaultPaths(*binDir))
		return
	}

	if len(args) > 0 && args[0] == "drain" {
		fs := flag.NewFlagSet("drain", flag.ContinueOnError)
		cfgPath := fs.String("config", defaultConfigPath, "путь к конфигу")
		_ = fs.Parse(args[1:])
		cfg := loadConfig(*cfgPath)
		cfg.sourcePath = *cfgPath
		runDrain(cfg)
		return
	}

	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	agent := fs.String("agent", "", "кто зовёт: claude или codex")
	cfgPath := fs.String("config", defaultConfigPath, "путь к конфигу")
	if fs.Parse(args) != nil || *agent == "" {
		// Даже на кривых аргументах — выход 0: телеметрия не мешает агенту.
		fmt.Fprintln(os.Stderr, "использование: hottell -agent claude|codex | drain | mcp | install | token | uninstall | status | version")
		return
	}
	cfg := loadConfig(*cfgPath)
	cfg.sourcePath = *cfgPath
	runHook(cfg, *agent)
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
