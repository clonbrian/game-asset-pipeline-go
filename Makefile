.PHONY: batch-gemini check-batch-job recover-batch-results sync-batch-job sync-batch-pending build

# One-shot Gemini folder batch (reads config.json → imageGeneration.*)
batch-gemini:
	go run ./cmd/game-asset-pipeline batch-gemini -config ./config.json

# Query Gemini Batch API (set JOB=batches/...), e.g. make check-batch-job JOB=batches/abc123
check-batch-job:
	go run ./cmd/game-asset-pipeline check-batch-job -config ./config.json -job-id "$(JOB)"

recover-batch-results:
	go run ./cmd/game-asset-pipeline recover-batch-results -config ./config.json -job-id "$(JOB)"

sync-batch-job:
	go run ./cmd/game-asset-pipeline sync-batch-job -config ./config.json -job-id "$(JOB)"

sync-batch-pending:
	go run ./cmd/game-asset-pipeline sync-batch-pending -config ./config.json

build:
	go build -o game-asset-pipeline ./cmd/game-asset-pipeline
