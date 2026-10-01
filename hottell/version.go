package main

import "runtime/debug"

// Версия вшивается при сборке из git-тега (схема vX.Y.Z):
//
//	go build -ldflags "-X main.version=0.0.1 -X main.commit=abc1234"
//
// Сборка без ldflags — dev+<sha> из VCS-метаданных Go (с +dirty при
// незакоммиченных правках).
var (
	version = "dev"
	commit  = ""
)

func versionString() string {
	if version != "dev" {
		return version
	}
	sha, dirty := vcsInfo()
	if sha == "" {
		return "dev"
	}
	v := "dev+" + sha
	if dirty {
		v += ".dirty"
	}
	return v
}

func commitString() string {
	if commit != "" {
		return commit
	}
	sha, _ := vcsInfo()
	return sha
}

func vcsInfo() (sha string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			sha = s.Value
			if len(sha) > 7 {
				sha = sha[:7]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return sha, dirty
}
