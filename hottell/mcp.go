package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP-режим: тот же бинарь — stdio-сервер для агентов. Тулы описаны в своих
// файлах и регистрируются здесь; реализация тула может потом уйти в бэк,
// интерфейс (имя, вход, выход) от этого не меняется.

func newMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "hottell", Version: versionString()}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "ping", Description: "Проверка связи: отвечает pong и версией hottell."}, toolPing)
	registerSessionTools(s)
	registerOtelTools(s)
	registerScopeTools(s)
	return s
}

func runMCP() {
	if err := newMCPServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}

type pingOut struct {
	Pong    string `json:"pong"`
	Version string `json:"version"`
}

func toolPing(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, pingOut, error) {
	return nil, pingOut{Pong: "pong", Version: versionString()}, nil
}
