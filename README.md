game-asset-pipeline-go/
├─ go.mod
├─ README.md
├─ config.json                要跑的設定檔
├─ providers.json             要跑的來源清單
├─ games.json                 要跑的遊戲清單
├─ Makefile
├─ config.sample.json           (範本)
├─ input/
│  ├─ providers.sample.json     (範本)
│  ├─ games.sample.json         (範本)
│  └─ gemini_batch_sample/      (放要送 Gemini 的測試圖)
├─ CURSOR_FIRST_PROMPT.md
├─ cmd/
│  └─ game-asset-pipeline/
│     └─ main.go
└─ internal/
   ├─ app/
   │  ├─ app.go
   │  ├─ run.go
   │  ├─ batch.go
   │  ├─ batch_gemini.go
   │  ├─ server.go
   │  └─ zip.go
   ├─ config/
   │  └─ config.go
   ├─ downloader/
   │  └─ downloader.go
   ├─ extractors/
   │  ├─ extractors.go
   │  ├─ html.go
   │  └─ jsonfeed.go
   ├─ imagex/
   │  └─ imagex.go
   ├─ imagegen/
   │  └─ gemini/
   │     └─ gemini.go
   ├─ matcher/
   │  └─ matcher.go
   ├─ model/
   │  └─ model.go
   └─ util/
      └─ util.go
 └─ output/                 (跑完自動產生)

      # Game Asset Pipeline (Go) - Local v1

Pure local batch pipeline:
- Reads `providers.json` + `games.json` (paths defined in `config.json`)
- Downloads provider pages/feeds
- Extracts candidate images (generic HTML <img> + simple JSON feed)
- Matches candidate images to game list
- Resizes to 3 sizes and outputs WebP
- Produces review files + manifests
- Produces a zip package for delivery
- Optional: **Gemini image-to-image** batch (`batch-gemini`) to adapt each source image to multiple aspect ratios via the Gemini API (not local crop).

## Gemini 批次產圖（`batch-gemini`）

此流程會遞迴掃描 `imageGeneration.inputDir` 內支援副檔名的圖片，對每張圖依 `imageGeneration.sizes` 各呼叫一次 [Gemini `generateContent`](https://ai.google.dev/api/rest/v1beta/models/generateContent)：將**原圖以 inline 圖片**一併送入模型，並用 `generationConfig.imageConfig` 指定 `aspectRatio` 與 `imageSize`，以 image-to-image 方式重構構圖（非單純本地裁切）。

### 1) 設定 `GEMINI_API_KEY`

使用 Google AI Studio / Gemini API 金鑰，**不要寫進程式或 config**。

- **Windows (PowerShell，目前工作階段)**：`$env:GEMINI_API_KEY="你的金鑰"`
- **macOS / Linux**：`export GEMINI_API_KEY="你的金鑰"`

config 裡的 `apiKeyEnv` 預設為 `GEMINI_API_KEY`，若改用其他環境變數名稱，請同步修改 `imageGeneration.apiKeyEnv`。

### 2) 設定 `inputDir` / `outputDir`

在 `config.json` 的 `imageGeneration` 區塊中設定（範例見 `config.sample.json`）：

- **`inputDir`**：放來源圖的資料夾（會遞迴掃描）。
- **`outputDir`**：產出 PNG 與報表目錄；預設範例為 `./output/gemini_batch`。
- **`enabled`**：必須設為 `true` 才會執行（避免誤觸 API）。

可將測試圖放在專案內建範例資料夾：`input/gemini_batch_sample/`。

### 3) 執行批次產圖（單一指令）

專案根目錄：

```bash
make batch-gemini
```

或明確指定 config：

```bash
go run ./cmd/game-asset-pipeline batch-gemini -config ./config.json
```

執行時終端會顯示每一個 job 的進度（`[目前/總數]`、OK / FAIL / SKIP）。結束後會印出 **`outputDir` 的絕對相對路徑**以及報表檔位置。

### 4) 產出檔名規則

對每個原始檔 `banner01.png`、版型名稱 `square`：

- 輸出檔名：`banner01__square.png`（兩個底線 `__`）
- 同樣會有 `banner01__wide.png`、`banner01__tall.png`（依 `sizes` 設定）

### 5) 報表（manifest / report）

寫入 `outputDir`：

- `gemini_batch_report.json`：`successCount` / `failedCount` / `skippedCount` 與每筆 `inputFile`、`outputFile`、`sizeName`、`status`、`error`
- `gemini_batch_report.csv`：同上，方便用試算表開啟

`overwrite: false` 時，若輸出檔已存在則 **SKIP**（方便中斷後重跑、接續未完成項目）。

### 6) Prompt 組裝

- 共用：`imageGeneration.promptTemplate`
- 每個版型：若該 size 有設定 `sizePrompt` 則用之；否則依 `name` 使用內建補句（`square` / `wide` / `tall`）

實際送進模型的文字為：**`promptTemplate` + 換行 + 版型補句**。

### 7) 常見錯誤排查

| 狀況 | 處理方式 |
|------|----------|
| `environment variable GEMINI_API_KEY is empty` | 設定環境變數，或改 `apiKeyEnv` 指向你已設定的變數名 |
| `imageGeneration.enabled is false` | 將 `enabled` 改為 `true` |
| `http 429` / Resource exhausted | 降低 `concurrency`；程式會對 429/503/500 做簡單指数退避重試（次數見 `retry`） |
| `no image in response` / safety block | 更換素材或調整 `promptTemplate`；檢查 `gemini_batch_report.json` 的 `error` |
| 模型不存在或名稱錯誤 | 修改 `model`（勿寫死在程式，以 config 為準）；確認 AI Studio 帳號可使用該模型 |
| `No images under inputDir` | 確認路徑正確、副檔名在 `supportedExtensions` 內 |

---

## Quick Start

### 1) Requirements
- Go 1.21+
- Network access to provider URLs
- (Optional) If providers block unknown user agents, set `user_agent` in config.

### 2) Prepare config and inputs
Copy sample files:

```bash
cp config.sample.json config.json
cp input/providers.sample.json providers.json
cp input/games.sample.json games.json


// provider：對應 providers.json 的那一家供應商名稱（一定要一致）

// game_name：你要比對的主要名字

// english_title：目前先跟 game_name 一樣就好（之後要改字才會用到）

// output_slug：輸出資料夾/檔名用（建議填，避免中文或空白造成麻煩）

// aliases：別名（越多越容易 match 到素材）

貼下面這個到GPT三次

Outpaint this image to fit a {210}x{210} canvas.
Rules:
- Keep original content intact (NO crop, NO stretch).
- Extend the background naturally to fill the new canvas (no blur, no gradient).
- Keep the game title fully visible.
- Match the original art style and lighting.

Outpaint this image to fit a {325}x{234} canvas.
Rules:
- Keep original content intact (NO crop, NO stretch).
- Extend the background naturally to fill the new canvas (no blur, no gradient).
- Keep the game title fully visible.
- Match the original art style and lighting.

Outpaint this image to fit a {294}x{400} canvas.
Rules:
- Keep original content intact (NO crop, NO stretch).
- Extend the background naturally to fill the new canvas (no blur, no gradient).
- Keep the game title fully visible.
- Match the original art style and lighting.
