package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type configBackupEntry struct {
	Name   string      `json:"name"`
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode"`
}

func agentConfigPaths(p paths) []string {
	return []string{p.claudeSettings, p.claudeJSON, p.codexHooks, p.codexConfig}
}

var backupNames = []string{"claude-settings.json", "claude.json", "codex-hooks.json", "codex-config.toml"}

// Backups live outside cfgDir/stateDir so uninstall cannot delete them.
// manifest.json is written last: an incomplete backup never permits installation.
func backupAgentConfigs(p paths) (string, error) {
	root := p.cfgDir + "-backups"
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", err
	}
	entries := make([]configBackupEntry, len(backupNames))
	for i, path := range agentConfigPaths(p) {
		entries[i].Name = backupNames[i]
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("бэкап %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("бэкап %s: не обычный файл", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, backupNames[i]), data, 0o600); err != nil {
			return "", err
		}
		entries[i].Exists, entries[i].Mode = true, info.Mode().Perm()
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		return "", err
	}
	fmt.Println("бэкап конфигов агентов:", dir)
	fmt.Printf("полный откат: hottell restore -backup %q\n", dir)
	return dir, nil
}

// Restore is an explicit whole-file rollback, never part of uninstall. It backs up
// the current configs first because they may have changed since installation.
func restoreAgentConfigs(p paths, dir string) error {
	if dir == "" {
		return fmt.Errorf("укажите каталог: hottell restore -backup <каталог>")
	}
	raw, err := os.ReadFile(filepath.Join(expandHome(dir), "manifest.json"))
	if err != nil {
		return err
	}
	var entries []configBackupEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	if len(entries) != len(backupNames) {
		return fmt.Errorf("неполный manifest.json")
	}
	// A missing/null exists field is not evidence that the original file was
	// absent: accepting its bool zero value would turn corruption into deletion.
	var required []struct {
		Exists *bool        `json:"exists"`
		Mode   *os.FileMode `json:"mode"`
	}
	if err := json.Unmarshal(raw, &required); err != nil {
		return err
	}
	for _, entry := range required {
		if entry.Exists == nil || entry.Mode == nil {
			return fmt.Errorf("manifest.json: обязательные поля exists/mode отсутствуют или null")
		}
	}
	contents := make([][]byte, len(entries))
	for i, entry := range entries {
		if entry.Name != backupNames[i] {
			return fmt.Errorf("неверный файл в manifest.json")
		}
		if entry.Exists {
			contents[i], err = os.ReadFile(filepath.Join(expandHome(dir), entry.Name))
			if err != nil {
				return err
			}
		}
	}
	if _, err := backupAgentConfigs(p); err != nil {
		return err
	}
	for i, path := range agentConfigPaths(p) {
		if !entries[i].Exists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		mode := entries[i].Mode.Perm()
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(path+".hottell-restore-tmp", contents[i], 0o600); err != nil {
			return err
		}
		if err := os.Chmod(path+".hottell-restore-tmp", mode); err != nil {
			return err
		}
		if err := os.Rename(path+".hottell-restore-tmp", path); err != nil {
			return err
		}
	}
	fmt.Println("конфиги агентов восстановлены; перезапустите Claude Code и Codex")
	return nil
}
