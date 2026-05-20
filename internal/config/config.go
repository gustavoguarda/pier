package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SaasURL     string `yaml:"saas_url"`
	HTTPBaseURL string `yaml:"http_base_url"`
	Token       string `yaml:"token"`
	BasePath    string `yaml:"base_path"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.SaasURL == "" {
		return nil, fmt.Errorf("saas_url is required")
	}
	if cfg.BasePath == "" {
		return nil, fmt.Errorf("base_path is required")
	}
	if cfg.HTTPBaseURL == "" {
		return nil, fmt.Errorf("http_base_url is required")
	}

	return &cfg, nil
}
