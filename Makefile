.PHONY: batch-gemini build

# One-shot Gemini folder batch (reads config.json → imageGeneration.*)
batch-gemini:
	go run ./cmd/game-asset-pipeline batch-gemini -config ./config.json

build:
	go build -o game-asset-pipeline ./cmd/game-asset-pipeline
