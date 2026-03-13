package app

import (
	"time"

	"game-asset-pipeline-go/internal/downloader"
	"game-asset-pipeline-go/internal/model"
)

type App struct {
	Cfg *model.Config
	DL  *downloader.Client
}

func New(cfg *model.Config) *App {
	to := time.Duration(cfg.TimeoutSeconds) * time.Second
	dl := downloader.New(to, cfg.UserAgent)
	return &App{Cfg: cfg, DL: dl}
}
