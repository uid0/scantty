package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uid0/scantty/internal/theme"
)

type Config struct {
	OMS      ServiceConfig
	ForgeKey ServiceConfig
	Cache    CacheConfig
	Scanner  ScannerConfig
	Theme    string
}

type ServiceConfig struct {
	BaseURL    string
	AuthToken  string
	ClientCert string
	ClientKey  string
	CACert     string
	Timeout    time.Duration
}

type CacheConfig struct {
	Path string
	TTL  time.Duration
}

type ScannerConfig struct {
	Source       string
	IdleFlushGap time.Duration
}

const (
	envOMSURL        = "SCANTTY_OMS_URL"
	envOMSToken      = "SCANTTY_OMS_TOKEN"
	envForgeKeyURL   = "SCANTTY_FORGEKEY_URL"
	envForgeKeyToken = "SCANTTY_FORGEKEY_TOKEN"
	envForgeKeyCert  = "SCANTTY_FORGEKEY_CLIENT_CERT"
	envForgeKeyKey   = "SCANTTY_FORGEKEY_CLIENT_KEY"
	envForgeKeyCA    = "SCANTTY_FORGEKEY_CA_CERT"
	envCachePath     = "SCANTTY_CACHE_PATH"
	envScannerSource = "SCANTTY_SCANNER_SOURCE"
	envTheme         = "SCANTTY_THEME"
)

func Load() (Config, error) {
	cache, err := defaultCachePath()
	if err != nil {
		return Config{}, err
	}
	prefs, err := LoadPrefs()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		OMS: ServiceConfig{
			BaseURL:   strings.TrimRight(os.Getenv(envOMSURL), "/"),
			AuthToken: os.Getenv(envOMSToken),
			Timeout:   15 * time.Second,
		},
		ForgeKey: ServiceConfig{
			BaseURL:    strings.TrimRight(os.Getenv(envForgeKeyURL), "/"),
			AuthToken:  os.Getenv(envForgeKeyToken),
			ClientCert: os.Getenv(envForgeKeyCert),
			ClientKey:  os.Getenv(envForgeKeyKey),
			CACert:     os.Getenv(envForgeKeyCA),
			Timeout:    15 * time.Second,
		},
		Cache: CacheConfig{
			Path: getEnvOr(envCachePath, cache),
			TTL:  10 * time.Minute,
		},
		Scanner: ScannerConfig{
			Source:       getEnvOr(envScannerSource, "stdin"),
			IdleFlushGap: 50 * time.Millisecond,
		},
		Theme: theme.Resolve(os.Getenv(envTheme), prefs.Theme),
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var problems []string
	if c.OMS.BaseURL == "" {
		problems = append(problems, fmt.Sprintf("%s is unset", envOMSURL))
	}
	if c.ForgeKey.BaseURL == "" {
		problems = append(problems, fmt.Sprintf("%s is unset", envForgeKeyURL))
	}
	if len(problems) > 0 {
		return errors.New("scantty config: " + strings.Join(problems, "; "))
	}
	return nil
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultCachePath() (string, error) {
	home, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "scantty", "cache.db"), nil
}
