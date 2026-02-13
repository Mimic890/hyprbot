package storage

import "time"

type Chat struct {
	ID                int64
	Type              string
	Title             string
	DefaultPresetName *string
	CreatedAt         time.Time
}

type ProviderInstance struct {
	ID             int64
	ChatID         int64
	Name           string
	Kind           string
	BaseURL        string
	EncAPIKey      *string
	EncHeadersJSON *string
	ConfigJSON     string
	CreatedAt      time.Time
}

type Preset struct {
	ChatID             int64
	Name               string
	ProviderInstanceID int64
	Model              string
	SystemPrompt       string
	ParamsJSON         string
	CreatedAt          time.Time
}

type PresetWithProvider struct {
	Preset
	Provider ProviderInstance
}

type AuditEntry struct {
	ChatID   int64
	UserID   int64
	Action   string
	MetaJSON string
}
