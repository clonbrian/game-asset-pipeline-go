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
	if ig.ModelPreset == "" {
		ig.ModelPreset = "gemini_default_realtime"
	}
	if len(ig.Presets) == 0 {
		ig.Presets = map[string]model.ImageModelPreset{
			"gemini_default_realtime": {
				ProviderRoute: "gemini",
				ExecutionMode: "realtime",
				Model:         "gemini-3.1-flash-image-preview",
				ImageSize:     "512",
			},
			"gemini_default_batch": {
				ProviderRoute: "gemini",
				ExecutionMode: "batch",
				Model:         "gemini-3.1-flash-image-preview",
				ImageSize:     "512",
			},
			"gemini_25_realtime_cheap": {
				ProviderRoute: "gemini",
				ExecutionMode: "realtime",
				Model:         "gemini-2.5-flash-image",
			},
			"gemini_25_batch_cheap": {
				ProviderRoute: "gemini",
				ExecutionMode: "batch",
				Model:         "gemini-2.5-flash-image",
			},
			"imagen_fast_test": {
				ProviderRoute: "imagen",
				ExecutionMode: "realtime",
				Model:         "imagen-4.0-fast-generate-001",
			},
			// backward compatibility
			"gemini_default": {
				ProviderRoute: "gemini",
				ExecutionMode: "realtime",
				Model:         "gemini-3.1-flash-image-preview",
				ImageSize:     "512",
			},
		}
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
	if ig.TimeoutMs == nil {
		v := 120000
		ig.TimeoutMs = &v
	}
	if len(ig.SupportedExtensions) == 0 {
		ig.SupportedExtensions = []string{".png", ".jpg", ".jpeg", ".webp"}
	}
	if ig.PromptTemplate == "" {
		ig.PromptTemplate = "Create a production-ready game asset adaptation from the provided source image. Preserve the core subject, logo, key art style, and visual identity. Expand or recompose the scene naturally for the requested aspect ratio. Keep the subject clean, centered or compositionally balanced, avoid unwanted text changes, avoid distortion, avoid duplicate limbs or objects, and keep it suitable for polished game marketing assets."
	}
	if ig.PostprocessEnabled == nil {
		t := true
		ig.PostprocessEnabled = &t
	}
	if ig.KeepRaw == nil {
		t := true
		ig.KeepRaw = &t
	}
	if ig.FinalFormat == "" {
		ig.FinalFormat = "webp"
	}
	if len(ig.Sizes) == 0 {
		ig.Sizes = []model.ImageGenSizeSpec{
			{Name: "square", AspectRatio: "1:1", TargetWidth: 210, TargetHeight: 210},
			{Name: "wide", AspectRatio: "16:9", TargetWidth: 325, TargetHeight: 234},
			{Name: "tall", AspectRatio: "9:16", TargetWidth: 294, TargetHeight: 400},
		}
	}
}
