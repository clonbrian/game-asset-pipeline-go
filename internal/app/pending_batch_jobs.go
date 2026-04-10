package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-asset-pipeline-go/internal/imagegen/gemini"
)

const pendingBatchRegistryFileName = "pending_batch_jobs.json"

// PendingBatchJobEntry is one row in pending_batch_jobs.json under imageGeneration.outputDir.
type PendingBatchJobEntry struct {
	JobID         string `json:"jobId"`
	CreatedAt     string `json:"createdAt"`
	ProviderRoute string `json:"providerRoute"`
	Model         string `json:"model"`
	ModelPreset   string `json:"modelPreset"`
	OutputDir     string `json:"outputDir"`
	Status        string `json:"status"`
	LastCheckedAt string `json:"lastCheckedAt,omitempty"`
	ErrorSummary  string `json:"errorSummary,omitempty"`
}

// PendingBatchRegistry is the on-disk JSON shape for pending_batch_jobs.json.
type PendingBatchRegistry struct {
	Jobs []PendingBatchJobEntry `json:"jobs"`
}

func pendingBatchRegistryPath(outputDir string) string {
	return filepath.Join(strings.TrimSpace(outputDir), pendingBatchRegistryFileName)
}

func loadPendingBatchRegistry(path string) (PendingBatchRegistry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PendingBatchRegistry{}, nil
		}
		return PendingBatchRegistry{}, err
	}
	var reg PendingBatchRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		return PendingBatchRegistry{}, fmt.Errorf("parse pending registry %s: %w", path, err)
	}
	return reg, nil
}

func savePendingBatchRegistry(path string, reg PendingBatchRegistry) error {
	return writeJSON(path, reg)
}

func pendingJobIndexByCanonical(jobs []PendingBatchJobEntry, canonicalID string) int {
	want, err := gemini.ValidateAndNormalizeBatchJobID(canonicalID)
	if err != nil {
		return -1
	}
	for i := range jobs {
		got, err := gemini.ValidateAndNormalizeBatchJobID(jobs[i].JobID)
		if err != nil {
			continue
		}
		if got == want {
			return i
		}
	}
	return -1
}

// appendPendingBatchJobIfMissing records a newly submitted batch job (status=pending).
func (a *App) appendPendingBatchJobIfMissing(outputDir, jobID, providerRoute, model, modelPreset string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("empty batch job id")
	}
	canonicalID, err := gemini.ValidateAndNormalizeBatchJobID(jobID)
	if err != nil {
		return err
	}
	out := strings.TrimSpace(outputDir)
	if out == "" {
		return fmt.Errorf("empty outputDir for pending registry")
	}
	path := pendingBatchRegistryPath(out)
	reg, err := loadPendingBatchRegistry(path)
	if err != nil {
		return err
	}
	if pendingJobIndexByCanonical(reg.Jobs, canonicalID) >= 0 {
		return nil
	}
	reg.Jobs = append(reg.Jobs, PendingBatchJobEntry{
		JobID:         canonicalID,
		CreatedAt:     time.Now().Format(time.RFC3339),
		ProviderRoute: strings.TrimSpace(providerRoute),
		Model:         strings.TrimSpace(model),
		ModelPreset:   strings.TrimSpace(modelPreset),
		OutputDir:     out,
		Status:        "pending",
	})
	return savePendingBatchRegistry(path, reg)
}

// ensurePendingBatchJobEntry upserts a pending row for an in-flight job (reuse path).
func (a *App) ensurePendingBatchJobEntry(outputDir, jobID, providerRoute, model, modelPreset string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("empty batch job id")
	}
	canonicalID, err := gemini.ValidateAndNormalizeBatchJobID(jobID)
	if err != nil {
		return err
	}
	out := strings.TrimSpace(outputDir)
	if out == "" {
		return fmt.Errorf("empty outputDir for pending registry")
	}
	path := pendingBatchRegistryPath(out)
	reg, err := loadPendingBatchRegistry(path)
	if err != nil {
		return err
	}
	idx := pendingJobIndexByCanonical(reg.Jobs, canonicalID)
	if idx >= 0 {
		e := &reg.Jobs[idx]
		st := strings.ToLower(strings.TrimSpace(e.Status))
		if st == "recovered" || st == "done" {
			return nil
		}
		if st == "failed" {
			e.Status = "pending"
			e.ErrorSummary = ""
			return savePendingBatchRegistry(path, reg)
		}
		return nil
	}
	reg.Jobs = append(reg.Jobs, PendingBatchJobEntry{
		JobID:         canonicalID,
		CreatedAt:     time.Now().Format(time.RFC3339),
		ProviderRoute: strings.TrimSpace(providerRoute),
		Model:         strings.TrimSpace(model),
		ModelPreset:   strings.TrimSpace(modelPreset),
		OutputDir:     out,
		Status:        "pending",
	})
	return savePendingBatchRegistry(path, reg)
}

// upsertPendingRegistryTerminal sets status (recovered / done / failed) for a job, appending if missing.
func (a *App) upsertPendingRegistryTerminal(outputDir, jobID, status, errSummary string, providerRoute, model, modelPreset string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil
	}
	canonicalID, err := gemini.ValidateAndNormalizeBatchJobID(jobID)
	if err != nil {
		return err
	}
	out := strings.TrimSpace(outputDir)
	if out == "" {
		return fmt.Errorf("empty outputDir for pending registry")
	}
	path := pendingBatchRegistryPath(out)
	reg, err := loadPendingBatchRegistry(path)
	if err != nil {
		return err
	}
	idx := pendingJobIndexByCanonical(reg.Jobs, canonicalID)
	status = strings.TrimSpace(status)
	if idx >= 0 {
		reg.Jobs[idx].Status = status
		reg.Jobs[idx].ErrorSummary = strings.TrimSpace(errSummary)
		return savePendingBatchRegistry(path, reg)
	}
	reg.Jobs = append(reg.Jobs, PendingBatchJobEntry{
		JobID:         canonicalID,
		CreatedAt:     time.Now().Format(time.RFC3339),
		ProviderRoute: strings.TrimSpace(providerRoute),
		Model:         strings.TrimSpace(model),
		ModelPreset:   strings.TrimSpace(modelPreset),
		OutputDir:     out,
		Status:        status,
		ErrorSummary:  strings.TrimSpace(errSummary),
	})
	return savePendingBatchRegistry(path, reg)
}
