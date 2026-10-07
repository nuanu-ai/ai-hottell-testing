package redact

import "sync"

// Memo remembers what SecretsAndBlobs gave each text, so a text that comes again in one job is
// masked once (HT-482). SecretsAndBlobs depends on the text alone, so a remembered answer is the
// one it would give again. A Memo lives as long as the job that made it and keeps every text it
// was given; a nil Memo masks every time. It is safe for concurrent use.
type Memo struct {
	mu     sync.Mutex
	masked map[string]string
}

// NewMemo returns an empty Memo.
func NewMemo() *Memo {
	return &Memo{masked: map[string]string{}}
}

// SecretsAndBlobs is SecretsAndBlobs(text), masked once per text.
func (m *Memo) SecretsAndBlobs(text string) string {
	if m == nil {
		return SecretsAndBlobs(text)
	}
	m.mu.Lock()
	out, ok := m.masked[text]
	m.mu.Unlock()
	if ok {
		return out
	}
	out = SecretsAndBlobs(text)
	m.mu.Lock()
	m.masked[text] = out
	m.mu.Unlock()
	return out
}
