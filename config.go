package main

import (
	"context"
	"encoding/json"
	"os"

	"code.crute.us/mcrute/golib/secrets"
)

type b2Config struct {
	AccountID string `mapstructure:"id"`
	Key       string `mapstructure:"key"`
}

type configEntry struct {
	Disabled        bool   `json:"disabled,omitempty"`
	Repo            string `json:"repo"`
	Password        string `json:"password,omitempty"`
	VaultMaterial   string `json:"vault_material,omitempty"`
	B2VaultMaterial string `json:"b2_vault_material,omitempty"`
	B2AccountId     string `json:"b2_account_id,omitempty"`
	B2Key           string `json:"b2_key,omitempty"`
}

func (e configEntry) ExtraConfig() any {
	if e.B2AccountId != "" || e.B2Key != "" {
		return b2Config{
			AccountID: e.B2AccountId,
			Key:       e.B2Key,
		}
	}
	return nil
}

type ConfigFile []*configEntry

func NewConfigFileFromFile(ctx context.Context, name string, sc secrets.Client) (ConfigFile, error) {
	fd, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	var out ConfigFile
	if err := json.NewDecoder(fd).Decode(&out); err != nil {
		return nil, err
	}

	// Skip processing secrets if Vault isn't enabled
	if sc == nil {
		return out, nil
	}

	// Populate secrets from Vault if needed
	for _, cfg := range out {
		if cfg.Password == "" && cfg.VaultMaterial != "" {
			var secret secrets.ApiKey
			if _, err := sc.Secret(ctx, cfg.VaultMaterial, &secret); err != nil {
				return nil, err
			}
			cfg.Password = secret.Key
		}

		if cfg.B2Key == "" && cfg.B2VaultMaterial != "" {
			var secret b2Config
			if _, err := sc.Secret(ctx, cfg.B2VaultMaterial, &secret); err != nil {
				return nil, err
			}
			cfg.B2AccountId = secret.AccountID
			cfg.B2Key = secret.Key
		}
	}

	return out, nil
}
