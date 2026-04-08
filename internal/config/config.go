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
	applyImageGenerationDefaults(cfg.ImageGeneration)
	return &cfg, nil
}

func applyImageGenerationDefaults(ig *model.ImageGenerationSpec) {
	if ig == nil {
		return
	}
	if ig.Provider == "" {
		ig.Provider = "gemini"
	}
	if ig.APIKeyEnv == "" {
		ig.APIKeyEnv = "GEMINI_API_KEY"
	}
	if ig.ImageSize == "" {
		ig.ImageSize = "1K"
	}
	if ig.InputDir == "" {
		ig.InputDir = "./input"
	}
	if ig.OutputDir == "" {
		ig.OutputDir = "./output"
	}
	if ig.Concurrency <= 0 {
		ig.Concurrency = 2
	}
	if ig.Retry <= 0 {
		ig.Retry = 3
	}
	if ig.TimeoutMs <= 0 {
		ig.TimeoutMs = 120000
	}
	if len(ig.SupportedExtensions) == 0 {
		ig.SupportedExtensions = []string{".png", ".jpg", ".jpeg", ".webp"}
	}
	if ig.PromptTemplate == "" {
		ig.PromptTemplate = "Create a production-ready game asset adaptation from the provided source image. Preserve the core subject, logo, key art style, and visual identity. Expand or recompose the scene naturally for the requested aspect ratio. Keep the subject clean, centered or compositionally balanced, avoid unwanted text changes, avoid distortion, avoid duplicate limbs or objects, and keep it suitable for polished game marketing assets."
	}
	if len(ig.Sizes) == 0 {
		ig.Sizes = []model.ImageGenSizeSpec{
			{Name: "square", AspectRatio: "1:1"},
			{Name: "wide", AspectRatio: "16:9"},
			{Name: "tall", AspectRatio: "9:16"},
		}
	}
}
