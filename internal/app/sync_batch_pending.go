package app

import (
	"fmt"
	"strings"
	"time"
)

// SyncBatchPending polls each registry entry once (no wait loop): succeeded jobs run recoverBatchResultsWithBaseDir;
// terminal failures update the registry; in-flight jobs refresh lastCheckedAt.
func (a *App) SyncBatchPending(quiet bool) error {
	ig := a.Cfg.ImageGeneration
	if ig == nil {
		return fmt.Errorf("config.imageGeneration is missing")
	}
	out := strings.TrimSpace(ig.OutputDir)
	path := pendingBatchRegistryPath(out)

	reg, err := loadPendingBatchRegistry(path)
	if err != nil {
		return err
	}
	if len(reg.Jobs) == 0 {
		if !quiet {
			fmt.Println("[sync-batch-pending] no entries (registry missing or empty)")
			fmt.Printf("  registry path: %s\n", path)
		}
		printSyncBatchPendingSummary(quiet, path, 0, 0, 0, 0, 0)
		return nil
	}

	if !quiet {
		fmt.Println("[sync-batch-pending] registry loaded")
		fmt.Printf("  path: %s\n", path)
		fmt.Printf("  entries: %d\n\n", len(reg.Jobs))
	}

	var recoveredRun, failedRun, skippedRun int

	for i := range reg.Jobs {
		e := &reg.Jobs[i]
		jobID := strings.TrimSpace(e.JobID)
		if jobID == "" {
			skippedRun++
			continue
		}

		st0 := strings.ToLower(strings.TrimSpace(e.Status))
		if st0 == "recovered" || st0 == "done" || st0 == "failed" {
			skippedRun++
			continue
		}

		metaDir := strings.TrimSpace(e.OutputDir)
		if metaDir == "" {
			metaDir = out
		}

		in, err := a.inspectBatchJobWithMetaDir(jobID, metaDir)
		now := time.Now().Format(time.RFC3339)
		if err != nil {
			if !quiet {
				fmt.Printf("[WARN] %s inspect failed: %v\n", jobID, err)
			}
			e.LastCheckedAt = now
			if saveErr := savePendingBatchRegistry(path, reg); saveErr != nil && !quiet {
				fmt.Printf("[WARN] could not save pending registry: %v\n", saveErr)
			}
			continue
		}

		terminalFail := in.IsFailed || in.Mapped == "failed" || in.Mapped == "cancelled" || in.Mapped == "expired"
		okRecover := in.Mapped == "succeeded" && in.IsSuccess

		switch {
		case okRecover:
			if !quiet {
				fmt.Printf("[sync-batch-pending] %s → succeeded, recovering → %s/recovered/%s/\n",
					jobID, strings.TrimSpace(e.OutputDir), sanitizeRecoveryJobDir(in.CanonicalID))
			}
			recoverBase := strings.TrimSpace(e.OutputDir)
			if recoverBase == "" {
				recoverBase = out
			}
			if err := a.recoverBatchResultsWithBaseDir(jobID, recoverBase); err != nil {
				if !quiet {
					fmt.Printf("[ERROR] %s recover failed: %v\n", jobID, err)
				}
				e.LastCheckedAt = now
				e.ErrorSummary = err.Error()
				_ = savePendingBatchRegistry(path, reg)
				continue
			}
			e.Status = "recovered"
			e.ErrorSummary = ""
			e.LastCheckedAt = now
			recoveredRun++
			_ = savePendingBatchRegistry(path, reg)

		case terminalFail:
			sum := strings.TrimSpace(in.ErrSummary)
			if sum == "" {
				sum = "status=" + in.Mapped
			}
			if !quiet {
				fmt.Printf("[sync-batch-pending] %s → terminal failure (%s)\n", jobID, in.Mapped)
				if sum != "" {
					fmt.Printf("  error: %s\n", truncateForLog(sum, 800))
				}
			}
			e.Status = "failed"
			e.ErrorSummary = sum
			e.LastCheckedAt = now
			failedRun++
			_ = savePendingBatchRegistry(path, reg)

		default:
			if !quiet {
				fmt.Printf("[sync-batch-pending] %s → still in progress (%s)\n", jobID, in.Mapped)
			}
			e.LastCheckedAt = now
			_ = savePendingBatchRegistry(path, reg)
		}
	}

	regFinal, _ := loadPendingBatchRegistry(path)
	pendingN := 0
	for _, e := range regFinal.Jobs {
		if strings.ToLower(strings.TrimSpace(e.Status)) == "pending" {
			pendingN++
		}
	}

	printSyncBatchPendingSummary(quiet, path, pendingN, recoveredRun, failedRun, skippedRun, len(regFinal.Jobs))
	return nil
}

func printSyncBatchPendingSummary(quiet bool, registryPath string, pending, recovered, failed, skipped, total int) {
	if quiet {
		if recovered > 0 || failed > 0 {
			fmt.Printf("[INFO] autoSyncPendingBatches: pending=%d recovered=%d failed=%d skipped=%d (registry entries=%d)\n",
				pending, recovered, failed, skipped, total)
			fmt.Printf("  registry: %s\n", registryPath)
		}
		return
	}
	fmt.Printf("\n[sync-batch-pending] summary\n")
	fmt.Printf("  registry: %s\n", registryPath)
	fmt.Printf("  pending count (after run): %d\n", pending)
	fmt.Printf("  recovered count (this run): %d\n", recovered)
	fmt.Printf("  failed count (this run): %d\n", failed)
	fmt.Printf("  skipped count (already terminal / empty id): %d\n", skipped)
}
