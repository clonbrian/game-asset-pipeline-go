package app

import (
	"fmt"
	"strings"
)

// SyncBatchJob performs one non-blocking GET: if the job already succeeded it runs RecoverBatchResults;
// if still in progress it prints status only; if failed/cancelled/expired it exits with error (no recover, no polling).
func (a *App) SyncBatchJob(jobID string) error {
	jobID = strings.TrimSpace(jobID)
	in, err := a.inspectBatchJob(jobID)
	if err != nil {
		return err
	}
	st := in.Status

	terminalFail := in.IsFailed || in.Mapped == "failed" || in.Mapped == "cancelled" || in.Mapped == "expired"
	okRecover := in.Mapped == "succeeded" && in.IsSuccess

	switch {
	case okRecover:
		fmt.Println("[sync-batch-job] succeeded → recovering images (same path as recover-batch-results)")
		fmt.Printf("  jobId: %s | state: %s | status: %s | model: %s\n",
			in.JobIDDisplay, strings.TrimSpace(st.State), in.Mapped, in.ModelDisplay)
		fmt.Printf("  outputDir: %s/recovered/%s/\n", in.OutputDir, sanitizeRecoveryJobDir(in.CanonicalID))
		fmt.Println()
		return a.RecoverBatchResults(jobID)

	case terminalFail:
		fmt.Println("[sync-batch-job] terminal failure — recover skipped")
		fmt.Printf("  jobId: %s | state: %s | status: %s\n", in.JobIDDisplay, emptyAsDash(strings.TrimSpace(st.State)), in.Mapped)
		if in.ErrSummary != "" {
			fmt.Printf("  error: %s\n", truncateForLog(in.ErrSummary, 800))
		}
		return fmt.Errorf("batch job not recoverable (status=%s)", in.Mapped)

	default:
		// running / pending / unknown-not-done — exit 0, no recover
		fmt.Println("[sync-batch-job] job not finished — recover skipped (single poll only, no wait)")
		fmt.Printf("  jobId: %s | state: %s | status: %s | model: %s\n",
			in.JobIDDisplay, emptyAsDash(strings.TrimSpace(st.State)), in.Mapped, in.ModelDisplay)
		syncPrintTime("createdAt", st.CreateTime)
		syncPrintTime("updatedAt", st.UpdateTime)
		return nil
	}
}

func syncPrintTime(label, v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		fmt.Printf("  %s: (n/a)\n", label)
		return
	}
	fmt.Printf("  %s: %s\n", label, v)
}
