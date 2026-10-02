package state

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrVersionMismatch means the settings cache's Version disagrees with the version
// inside its document.
var ErrVersionMismatch = errors.New("settings version does not match the document")

// Credentials are what the binary needs to send to the service: where and with which
// collector token.
type Credentials struct {
	IngestURL      string `json:"ingest_url"`
	CollectorToken string `json:"collector_token"`
}

// SettingsCache is the last settings document received from the service
// (docs/specs/hottell-contract/settings.schema.json) with its version. The hook reads
// only this cache and never goes to the network.
type SettingsCache struct {
	// Version is the document's version field; 0 when nothing is cached.
	Version int64
	// Document is the settings document as the service sent it; nil when nothing is
	// cached.
	Document json.RawMessage
}

// NewSettingsCache returns the cache of a settings document with the document's version.
func NewSettingsCache(doc json.RawMessage) (SettingsCache, error) {
	version, err := documentVersion(doc)
	if err != nil {
		return SettingsCache{}, fmt.Errorf("decode settings document: %w", err)
	}
	return SettingsCache{Version: version, Document: doc}, nil
}

// ReadCredentials returns the saved credentials, or zero ones when none are saved.
func (p Paths) ReadCredentials() (Credentials, error) {
	var creds Credentials
	data, err := readFile(p.StateFile())
	if err != nil || data == nil {
		return creds, err
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("decode %s: %w", p.StateFile(), err)
	}
	return creds, nil
}

// WriteCredentials saves the credentials atomically with mode 0600.
func (p Paths) WriteCredentials(creds Credentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	return WriteFileAtomic(p.StateFile(), data)
}

// ReadSettings returns the cached settings, or an empty cache when none is saved.
func (p Paths) ReadSettings() (SettingsCache, error) {
	data, err := readFile(p.SettingsFile())
	if err != nil || data == nil {
		return SettingsCache{}, err
	}
	version, err := documentVersion(data)
	if err != nil {
		return SettingsCache{}, fmt.Errorf("decode %s: %w", p.SettingsFile(), err)
	}
	return SettingsCache{Version: version, Document: data}, nil
}

// WriteSettings saves the settings document atomically with mode 0600. The cache's
// Version must equal the version inside the document.
func (p Paths) WriteSettings(cache SettingsCache) error {
	version, err := documentVersion(cache.Document)
	if err != nil {
		return fmt.Errorf("decode settings document: %w", err)
	}
	if version != cache.Version {
		return fmt.Errorf("%w: cache %d, document %d", ErrVersionMismatch, cache.Version, version)
	}
	return WriteFileAtomic(p.SettingsFile(), cache.Document)
}

// documentVersion reads the version field of a settings document, which must be a
// single JSON object; an absent field is the schema's default 0.
func documentVersion(doc []byte) (int64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc, &fields); err != nil {
		return 0, err
	}
	if fields == nil {
		return 0, errors.New("settings document is not a JSON object")
	}
	raw, ok := fields["version"]
	if !ok {
		return 0, nil
	}
	var version int64
	if err := json.Unmarshal(raw, &version); err != nil {
		return 0, fmt.Errorf("settings version: %w", err)
	}
	if version < 0 {
		return 0, fmt.Errorf("negative settings version %d", version)
	}
	return version, nil
}
