package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Хирургия TOML текстом, без парсера-сериализатора: config.toml Codex правят
// руками и другие процессы, round-trip через библиотеку потерял бы комментарии
// и порядок. Единица правки — семейство таблиц: заголовок [name] и все его
// подтаблицы [name.*] до следующего чужого заголовка. Комментарии и пустые
// строки прямо перед чужим заголовком остаются за ним.

// tomlHeader — имя таблицы из строки-заголовка ("[a.b]" -> "a.b", "[[x]]" -> "x").
func tomlHeader(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	if i := strings.Index(t, "#"); i > 0 && !strings.Contains(t[:i], "\"") {
		t = strings.TrimSpace(t[:i])
	}
	t = strings.TrimPrefix(strings.TrimSuffix(t, "]"), "[")
	t = strings.TrimPrefix(strings.TrimSuffix(t, "]"), "[")
	return strings.TrimSpace(t), true
}

func inFamily(header, name string) bool {
	return header == name || strings.HasPrefix(header, name+".")
}

// tomlFamilyRange — [start, end) строк семейства name; start = -1, если его нет.
// Семейство может встречаться кусками — берётся первый непрерывный.
func tomlFamilyRange(lines []string, name string) (int, int) {
	start := -1
	for i, l := range lines {
		h, ok := tomlHeader(l)
		if !ok {
			continue
		}
		if start < 0 {
			if inFamily(h, name) {
				start = i
			}
			continue
		}
		if !inFamily(h, name) {
			end := i
			for end > start+1 {
				p := strings.TrimSpace(lines[end-1])
				if p == "" || strings.HasPrefix(p, "#") {
					end--
					continue
				}
				break
			}
			return start, end
		}
	}
	if start < 0 {
		return -1, -1
	}
	return start, len(lines)
}

// tomlFamily — текст семейства (пусто, если нет).
func tomlFamily(text, name string) string {
	lines := strings.Split(text, "\n")
	s, e := tomlFamilyRange(lines, name)
	if s < 0 {
		return ""
	}
	return strings.Join(lines[s:e], "\n")
}

// tomlRemoveFamily — текст без семейства name и признак, что было что удалять.
func tomlRemoveFamily(text, name string) (string, bool) {
	lines := strings.Split(text, "\n")
	s, e := tomlFamilyRange(lines, name)
	if s < 0 {
		return text, false
	}
	out := append(append([]string{}, lines[:s]...), lines[e:]...)
	return strings.Join(out, "\n"), true
}

// tomlSetFamily — заменить семейство name блоком block (на том же месте) или
// дописать его в конец файла.
func tomlSetFamily(text, name, block string) string {
	block = strings.TrimRight(block, "\n")
	lines := strings.Split(text, "\n")
	s, e := tomlFamilyRange(lines, name)
	if s >= 0 {
		out := append(append([]string{}, lines[:s]...), strings.Split(block, "\n")...)
		if e < len(lines) && strings.TrimSpace(lines[e]) != "" {
			out = append(out, "")
		}
		out = append(out, lines[e:]...)
		return strings.Join(out, "\n")
	}
	t := strings.TrimRight(text, "\n")
	if t != "" {
		t += "\n\n"
	}
	return t + block + "\n"
}

// tomlQuote — строка TOML в двойных кавычках.
func tomlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// tomlValue — значение ключа key внутри текста семейства (сырой литерал,
// строки без кавычек); подтаблица задаётся полным именем заголовка.
func tomlValue(family, table, key string) (string, bool) {
	cur := ""
	for _, l := range strings.Split(family, "\n") {
		if h, ok := tomlHeader(l); ok {
			cur = h
			continue
		}
		if cur != table {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			if end := strings.LastIndex(v, `"`); end > 0 {
				return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(v[1:end]), true
			}
		}
		if i := strings.Index(v, " #"); i > 0 && !strings.HasPrefix(v, "{") && !strings.HasPrefix(v, "[") {
			v = strings.TrimSpace(v[:i])
		}
		return v, true
	}
	return "", false
}

func readTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// writeTextFile — атомарно, с сохранением прав существующего файла.
func writeTextFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".hottell-tmp"
	if err := os.WriteFile(tmp, []byte(text), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
