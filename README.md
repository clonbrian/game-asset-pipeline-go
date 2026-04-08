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
   │  ├─ gemini/
   │  │  └─ gemini.go
   │  └─ postprocess/
   │     ├─ covercrop.go
   │     └─ png.go
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
- Optional: **Gemini 兩段式批次**（`batch-gemini`）：先用 API 產出固定比例 **raw**，再以純 Go **固定像素**後處理成最終檔（與頂層 `sizes` WebP 管線互不共用欄位）。

## Gemini 批次產圖（`batch-gemini`）— raw + 固定尺寸後處理

流程分兩段（**與頂層 `sizes`（width/height）無關**；那組仍給 `run` / `batch` 的 WebP 用。`imageGeneration.sizes` 另有 `aspectRatio` + `targetWidth` / `targetHeight`）：

1. **Raw（Gemini）**  
   遞迴掃描 `imageGeneration.inputDir`，每張來源圖、每個版型呼叫一次 [Gemini `generateContent`](https://ai.google.dev/api/rest/v1beta/models/generateContent)：原圖以 **inline** 送入，並以 `generationConfig.imageConfig` 設定 **`aspectRatio`**（例如 1:1 / 16:9 / 9:16）與 **`imageSize`**，做 image-to-image 構圖。  
   產出檔名：`{檔名}__{版型名}__raw.png`（例如 `banner01__square__raw.png`）。

2. **Final（本地）**  
   讀取 raw PNG，用 **等比例放大／縮小至可完整覆蓋** `targetWidth×targetHeight`，再 **水平＋垂直置中裁切** 成精確像素（語意等同 CSS `object-fit: cover`）。**不**做非等比例拉伸，故最終檔寬高固定、不飄。  
   產出檔名：`{檔名}__{版型名}.png`（例如 `banner01__square.png`，固定為 **210×210 / 325×234 / 294×400** 等，由 config 決定）。

後處理實作於 `internal/imagegen/postprocess`（`golang.org/x/image/draw` Catmull-Rom 縮放），**不依賴 ffmpeg 或外部桌面工具**。

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

每個 `imageGeneration.sizes[]` 物件請包含：

- **`aspectRatio`**：僅給 Gemini raw 階段（例如 `1:1`、`16:9`、`9:16`）。
- **`targetWidth` / `targetHeight`**：僅給本地後處理，**最終檔**的精確像素（例如 210×210、325×234、294×400）。

### 3) 執行批次產圖（單一指令）

專案根目錄：

```bash
make batch-gemini
```

或明確指定 config：

```bash
go run ./cmd/game-asset-pipeline batch-gemini -config ./config.json
```

執行時終端會顯示進度：`[n/總數]` 的 **SKIP / FAIL / OK**（中間可能出現無序號的 `[RAW]` / `[REUSE]` 日誌）。結束後會印出 **`outputDir`** 與報表路徑。

### 4) 產出檔名規則

假設來源為 `banner01.png`、版型 `square`：

| 階段 | 檔名 | 說明 |
|------|------|------|
| Raw | `banner01__square__raw.png` | Gemini 依 `aspectRatio` 產出的中間檔 |
| Final | `banner01__square.png` | 本地後處理後 **固定** `targetWidth×targetHeight` |

`wide`、`tall` 同理（`__wide__raw.png` / `__wide.png` 等）。

### 5) 報表（manifest / report）

寫入 `outputDir`：

- `gemini_batch_report.json` / `gemini_batch_report.csv`：每筆含 **`inputFile`、`rawOutputFile`、`finalOutputFile`、`sizeName`、`status`（success / failed / skipped）、`error`**

### 5b) `overwrite: false` 時的 skip / resume

- 若 **final**（`__{name}.png`）已存在 → **整個版型 skip**（不再打 Gemini、不再後處理）。
- 若 **final 不存在** 但 **raw**（`__{name}__raw.png`）已存在 → **略過 Gemini**，直接讀 raw 做本地後處理產出 final（適合 API 已成功但後處理中斷後重跑）。
- `overwrite: true` 時 → 一律重打 Gemini 覆寫 raw，並重算 final（不因 final 已存在而跳過）。

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
| `targetWidth and targetHeight must be > 0` | 在 `imageGeneration.sizes` 補齊 `targetWidth` / `targetHeight` |
| `aspectRatio is required` | 該 size 必須有非空 `aspectRatio` 供 Gemini 使用 |

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
