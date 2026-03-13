package config

import (
	"encoding/json"
	"fmt"
	"os"

	"game-asset-pipeline-go/internal/model"
)

func LoadConfig(path string) (*model.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg model.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}

	// defaults
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "GameAssetPipelineBot/1.0"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./output"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "./work"
	}
	if cfg.IncomingDir == "" {
		cfg.IncomingDir = "./incoming"
	}
	if cfg.Retry.MaxAttempts <= 0 {
		cfg.Retry.MaxAttempts = 2
	}
	if cfg.Retry.BackoffMillis <= 0 {
		cfg.Retry.BackoffMillis = 700
	}
	if cfg.Extract.MaxAssetsPerProvider <= 0 {
		cfg.Extract.MaxAssetsPerProvider = 800
	}
	if cfg.Extract.MinimumMatchScore <= 0 {
		cfg.Extract.MinimumMatchScore = 35
	}
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = "127.0.0.1:8080"
	}
	if len(cfg.Sizes) == 0 {
		cfg.Sizes = []model.SizeSpec{
			{Name: "sm", Width: 320, Height: 180},
			{Name: "md", Width: 640, Height: 360},
			{Name: "lg", Width: 1280, Height: 720},
		}
	}
	return &cfg, nil
}
