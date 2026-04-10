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
   │  ├─ batch_gemini_batchmode.go
   │  ├─ check_batch_job.go
   │  ├─ recover_batch_results.go
   │  ├─ sync_batch_job.go
   │  ├─ batch_gemini_prompt.go
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
   │     └─ encode_final.go
   ├─ matcher/
   │  └─ matcher.go
   ├─ model/
   │  └─ model.go
   └─ util/
      └─ util.go
 └─ output/                 (跑完自動產生；batch-gemini 時可在 gemini_batch/raw、…/final 見分層輸出)

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

## Gemini 批次產圖（`batch-gemini`）— 模式可切換

`imageGeneration.postprocessEnabled` 可切換兩種模式（**與頂層 `sizes`（width/height）無關**；那組仍給 `run` / `batch` 的 WebP 用。`imageGeneration.sizes` 另有 `aspectRatio` + `targetWidth` / `targetHeight`）：

- **raw+postprocess**（`postprocessEnabled: true`，預設）：Gemini 先產 raw，再做本地固定尺寸後處理產生 final。
- **raw-only**（`postprocessEnabled: false`）：只產 raw，不做本地後處理，不輸出 final。

### 模型預設切換（`modelPreset`）

在同一個 `imageGeneration` 區塊內可定義 `presets` 並用 `modelPreset` 一鍵切換路線：

- `gemini_default_realtime` → `providerRoute=gemini`, `executionMode=realtime`，走既有 Gemini `generateContent` image route（支援 source image + prompt + aspectRatio）。
- `gemini_default_batch` → `providerRoute=gemini`, `executionMode=batch`，走 Gemini Batch API（`batchGenerateContent`，非同步，目標 24 小時內完成）。
- `gemini_25_realtime_cheap` → `providerRoute=gemini`, `executionMode=realtime`, `model=gemini-2.5-flash-image`，偏低成本的 Gemini 即時圖片模式。
- `gemini_25_batch_cheap` → `providerRoute=gemini`, `executionMode=batch`, `model=gemini-2.5-flash-image`，偏低成本的 Gemini Batch 圖片模式。
- `imagen_fast_test` → `providerRoute=imagen`，走 Imagen `:predict` route（**不走 generateContent**，**low-cost text-to-image test route**）。

> 能力差異（請避免誤用）：
> - `gemini_default_realtime`：支援 **source image + prompt** 的 image adaptation / extension workflow（即時回應）。
> - `gemini_default_batch`：同樣是 Gemini image workflow，但透過 Batch API 非同步處理，成本較低、速度較慢（可接受 24 小時週期）。
> - `gemini_25_realtime_cheap`：與 `gemini_default_realtime` 路線相同（Gemini 即時 API），主要差異是模型改成 `gemini-2.5-flash-image`，通常成本更低；能力以該模型當下可用規格為準。
> - `gemini_25_batch_cheap`：與 `gemini_default_batch` 路線相同（Gemini Batch API），主要差異是模型改成 `gemini-2.5-flash-image`，通常成本更低；能力以該模型當下可用規格為準。
> - `imagen_fast_test`：目前為 **text-to-image predict route**，不會把 source image 當 reference input 送入模型。

`gemini_default_batch` 注意事項：

- Batch API 建立作業不是 idempotent；重複送同一批會建立新作業。專案改為把 metadata 落在 `outputDir/jobs/*.json`（每個 job 一份），在 `overwrite=false` 下依目前 config + item 清單比對可重用 job，避免不同 batch job 互相覆蓋。
- 舊版單檔 `outputDir/gemini_batch_job_meta.json` 已棄用；若檔案仍存在，程式會提示 deprecated 警告。
- 本實作使用 inlined requests（方便直接重用既有 request 組裝）；大量圖片可能碰到 payload 大小限制，需分批跑或改成 JSONL / Files API。

程式啟動時會顯示：

- `[INFO] batch-gemini mode = raw+postprocess`
- 或 `[INFO] batch-gemini mode = raw-only`

### raw+postprocess 流程

1. **Raw（Gemini）**  
   遞迴掃描 `imageGeneration.inputDir`，每張來源圖、每個版型呼叫一次 [Gemini `generateContent`](https://ai.google.dev/api/rest/v1beta/models/generateContent)：原圖以 **inline** 送入，並以 `generationConfig.imageConfig` 設定 **`aspectRatio`**（例如 1:1 / 16:9 / 9:16）與 **`imageSize`**，做 image-to-image 構圖。  
   產出目錄：**`{outputDir}/raw/`**，檔名：`{檔名}__{版型名}__raw.png`（例如 `output/gemini_batch/raw/banner01__square__raw.png`）。

2. **Final（本地）**  
   讀取 Gemini 產物（通常為 **PNG**），用 **等比例放大／縮小至可完整覆蓋** `targetWidth×targetHeight`，再 **水平＋垂直置中裁切**（`object-fit: cover`），最後以 **WebP** 寫出。**不**做非等比例拉伸，像素尺寸固定。  
   產出目錄：**`{outputDir}/final/`**，檔名：`{檔名}__{版型名}.webp`（例如 `output/gemini_batch/final/banner01__square.webp`）。`finalFormat` 預設 **`webp`**（目前僅實作此格式）。

### raw-only 流程（`postprocessEnabled: false`）

- 只呼叫 Gemini 產 `raw/{filename}__{size}__raw.png`
- 不做 cover/crop / resize / final encode
- 不寫 `final/` 內任何檔案
- 報表 `finalOutputFile` 會是空字串；`status=success` 代表 raw 成功

報表 **`gemini_batch_report.json` / `.csv`** 仍在 **`{outputDir}` 根層**（與 `raw/`、`final/` 同層）。

**目錄樹範例**（`outputDir = ./output/gemini_batch`）：

```
output/gemini_batch/
  raw/           ← Gemini 中間檔，一律 .png
  final/         ← 本地後處理成品，.webp
  jobs/          ← Gemini Batch metadata（每個 job 一份 .json）
  gemini_batch_report.json
  gemini_batch_report.csv
```

後處理：`golang.org/x/image/draw`（Catmull-Rom 縮放）+ **[github.com/deepteams/webp](https://github.com/deepteams/webp)**（**純 Go** WebP encode，無 CGO／無 ffmpeg）。

### 1) 設定 `GEMINI_API_KEY`

使用 Google AI Studio / Gemini API 金鑰，**不要寫進程式或 config**。

- **Windows (PowerShell，目前工作階段)**：`$env:GEMINI_API_KEY="你的金鑰"`
- **macOS / Linux**：`export GEMINI_API_KEY="你的金鑰"`

config 裡的 `apiKeyEnv` 預設為 `GEMINI_API_KEY`，若改用其他環境變數名稱，請同步修改 `imageGeneration.apiKeyEnv`。

### 2) 設定 `inputDir` / `outputDir`

在 `config.json` 的 `imageGeneration` 區塊中設定（範例見 `config.sample.json`）：

- **`inputDir`**：放來源圖的資料夾（會遞迴掃描）。
- **`outputDir`**：輸出根目錄（語意不變）。程式會自動建立 **`outputDir/raw`**（Gemini 中間檔）與 **`outputDir/final`**（固定尺寸成品）；報表寫在 **`outputDir`** 底下。
- **`outputDir/jobs`**：Batch job metadata 目錄。每個 Gemini job 都會寫成單獨檔案（例如 `batches_xxx.json`），讓多次 batch 可並存並安全續接。
- **`enabled`**：必須設為 `true` 才會執行（避免誤觸 API）。

**`timeoutMs`（HTTP 客戶端）**

- **`timeoutMs` > 0**：對 Gemini 請求使用該毫秒數作為 `http.Client.Timeout`（整個請求含讀 body）。
- **`timeoutMs` ≤ 0**（例如 **`0`**）：**不設定** HTTP client timeout（可能長時間等待）；啟動時會印 **`[WARN] batch-gemini running with no HTTP timeout`**。
- **JSON 省略 `timeoutMs`**：視為未指定，載入後預設 **120000**（120 秒），維持舊行為。

`batch-gemini` 對 Gemini 呼叫使用 `context.Background()`，**不**另建會自動 cancel 的 deadline context；429/503/500 等退避重試邏輯不變。

可將測試圖放在專案內建範例資料夾：`input/gemini_batch_sample/`。

每個 `imageGeneration.sizes[]` 物件請包含：

- **`aspectRatio`**：僅給 Gemini raw 階段（例如 `1:1`、`16:9`、`9:16`）。
- **`targetWidth` / `targetHeight`**：僅給本地後處理，**最終檔**的精確像素（例如 210×210、325×234、294×400）。

其他常用欄位：

- **`postprocessEnabled`**（可選）：預設 **`true`**。`true` 走完整流程（raw+postprocess）；`false` 只產 raw（raw-only）。
- **`modelPreset`**（可選）：預設 **`gemini_default_realtime`**。可切換 `gemini_default_batch`、`gemini_25_realtime_cheap`、`gemini_25_batch_cheap`、`imagen_fast_test`，不需手動改多個欄位。
- **`presets`**（可選）：集中放 `providerRoute`、`executionMode`、`model`、（Gemini 可選）`imageSize`。

- **`keepRaw`**（可選）：預設 **`true`**。為 `true` 時將 Gemini 回傳內容寫入 `raw/` 的 `.png`，便於 **`overwrite: false` 時用 raw 續跑 final**；為 `false` 時本輪不寫 raw 檔（僅記憶體解碼），報表 `rawOutputFile` 可能為空字串，且無法只靠本輪產物從 raw 恢復。
- **`finalFormat`**（可選）：預設 **`webp`**。目前僅支援 `webp`；編碼集中於 `internal/imagegen/postprocess`。

> 注意：當 `postprocessEnabled=false`（raw-only）時，`keepRaw=false` 會被忽略，程式會強制保留 raw 並印 warning。

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

#### 查詢 Gemini Batch job 狀態（`check-batch-job`）

**以 Google Batch API 為準**：只要手上有 **batch job id**（`batches/...`），就會直接 **GET** 遠端資源。**不必**先有本地 metadata；若 `outputDir/jobs/*.json`（或舊版根目錄 `gemini_batch_job_meta.json`）裡剛好有同一個 job，會額外印出 **`metadata` 路徑**，並僅在 **`model` / `executionMode` / `modelPreset` / `providerRoute`** 等欄位上，於 API 未提供時用 metadata 補齊；若兩者皆無則顯示 **`(unknown from API/metadata)`**，**不會**再用目前 config 冒充該 job 的真實設定。

**為何以前會看到空的 `state`？** 官方 **`batches.get`** 回傳的是 **long-running `Operation`** JSON（含 `done`、`metadata`、`error` 等），`GenerateContentBatch` 的 **`state` / `createTime` / `model` 等多半在 `metadata` 物件裡**，而不是頂層欄位。舊版只解頂層，會得到空字串。程式已改為從 `metadata`（必要時併入 `response`）解析，且 **`batch-gemini` 的 `GetBatch` / 輪詢** 也使用同一套抽取邏輯，避免輪詢時讀不到狀態。

若仍無法解析 **`state`**，會自動把完整回應寫入 **`{outputDir}/check_batch_debug/batches_xxx_response.json`**（可讀、盡量 pretty JSON）。加上 **`-debug`** 時會另將完整 JSON 印到 **stderr**。

```bash
go run ./cmd/game-asset-pipeline check-batch-job -config ./config.json -job-id "batches/q9n0bilxls18if2jhz7mjkrswvb0d3loe1hg"
# 除錯：stderr 印出完整 JSON，並寫入 check_batch_debug/
go run ./cmd/game-asset-pipeline check-batch-job -config ./config.json -job-id "batches/..." -debug
```

`-job-id` **必填**；可寫完整 `batches/<id>` 或只寫 `<id>`（程式會正規化）。格式錯誤、缺 API key、HTTP **404**（查無此 job）或非 2xx 時會回報清楚錯誤；非 2xx 時會把 **HTTP status** 與 **response body（截斷）** 印到 stderr。

或使用 Makefile（須設定 **`JOB=`**）：

```bash
make check-batch-job JOB=batches/q9n0bilxls18if2jhz7mjkrswvb0d3loe1hg
```

- API key 來自 **`imageGeneration.apiKeyEnv`**（與 `batch-gemini` 相同）。
- 終端會印出區塊 **`[INFO] Batch job status`**，包含：`jobId`、`state (raw)`、對照用的 **`status (mapped)`**（`running` / `succeeded` / `failed` / `cancelled` / `expired` / `unknown`）、`providerRoute` 與 **`model`**（優先 API 回傳，否則用 config）、`createdAt` / `updatedAt` / `completedAt`（無則 `(n/a)`）、`isDone` / `isSuccess` / `isFailed`、`metadata` 路徑、以及失敗時的 **`errorMessage`**。
- 若 **`status (mapped)` 仍為 `running`**，且 **`createTime`/`updateTime` 可解析**後已超過 **24 小時**，會多印 **`[WARN]`**。

**狀態映射（摘要）**：將 API 的 `state` 字串轉成大寫後比對關鍵字——含 **`RUNNING` / `PROCESSING` / `PENDING`** → `running`，`isDone=false`；含 **`SUCCEEDED`** 或明確 **`COMPLETED`** → `succeeded`，`isDone=true` 且 **`isSuccess=true`**；含 **`FAILED` / `CANCELLED`（`CANCELED`）/ `EXPIRED`** → 對應 `failed` / `cancelled` / `expired`，皆 **`isDone=true` 且 `isFailed=true`**（成功僅在 `succeeded`）。若出現未涵蓋字串則為 **`unknown`**（布林旗標維持保守預設）。

**metadata 與 API 的差異**：metadata 是本機記錄「上次建立 batch 時寫下的 job id 與設定」，可能過期或與遠端不同步；**查狀態請以 API 輸出為準**，metadata 僅供快速找到本機對應檔案。

#### 回收已完成 Batch 的內嵌圖片（`recover-batch-results`）

**不會**重新送出 batch；僅對 **`state` 已成功**（`BATCH_STATE_SUCCEEDED` / `SUCCEEDED`）的 job 呼叫 **`batches.get`**，解析 **`output.inlinedResponses`**（支援 **`responses`** 與巢狀 **`inlinedResponses`** 兩種欄位），從每筆 **`response.candidates[].content.parts`** 讀取 **`inlineData` / `inline_data`** 的 base64 圖片並寫入檔案。

```bash
go run ./cmd/game-asset-pipeline recover-batch-results -config ./config.json -job-id "batches/你的jobId"
```

- 使用 **`imageGeneration.apiKeyEnv`** 與 **`imageGeneration.outputDir`**（僅路徑與金鑰，不用 `modelPreset` 猜模型）。
- 輸出目錄：**`{outputDir}/recovered/{sanitizedJobId}/`**，例如 `output/gemini_batch/recovered/batches_g7gbxfiwnm4z66s37kbomsza2rsat9dvehoi/`。
- 檔名優先 **`metadata.item_id`**，否則 **`item_0000`** 起編；副檔名依 MIME（`.png` / `.jpg` / `.webp`，未知為 `.bin`）。
- 結束會印 **`recovered` / `skipped`（無圖、僅文字等）/ `errors`** 計數；單筆錯誤不會讓整批在未寫出任何檔案前直接成功（若全失敗會非 0 結束）。

#### 一鍵查狀態並順手回收（`sync-batch-job`，24hr batch 用）

**不輪詢、不阻塞**：只做 **一次** `batches.get`（與 `check-batch-job` 相同，沿用 **`inspectBatchJob` → `FetchBatchJobStatus`** 的 LRO / metadata 解析）。

- **已成功**（`succeeded`）：接著呼叫 **`RecoverBatchResults`**，圖片寫入 **`{outputDir}/recovered/{sanitizedJobId}/`**，並印出與 `recover-batch-results` 相同的 **recovered / skipped / errors** 與目錄。
- **進行中**（`running` / `pending` / 其他未完成）：**exit 0**，簡短印 **state、status、model、createdAt、updatedAt**，並註明 **未執行 recover**。
- **失敗 / 取消 / 過期等終止錯誤**：**不 recover**，印 **state、status、error 摘要**，**exit 非 0**。

```bash
go run ./cmd/game-asset-pipeline sync-batch-job -config ./config.json -job-id "batches/你的jobId"
make sync-batch-job JOB=batches/你的jobId
```

### 4) 產出路徑與檔名

假設 `outputDir` 為 `./output/gemini_batch`，來源為 `banner01.png`、版型 `square`：

| 階段 | 路徑 | 說明 |
|------|------|------|
| Raw | `output/gemini_batch/raw/banner01__square__raw.png` | Gemini 依 `aspectRatio` 產出的中間檔 |
| Final | `output/gemini_batch/final/banner01__square.webp` | 本地後處理後 **固定** `targetWidth×targetHeight`（WebP） |

`wide`、`tall` 同理（`__wide__raw.png` / `__wide.webp`）。在 raw-only 模式下只有 raw 檔。

### 5) 報表（manifest / report）

寫入 `outputDir`：

- `gemini_batch_report.json` / `gemini_batch_report.csv`：每筆含 **`inputFile`、`rawOutputFile`、`finalOutputFile`、`providerRouteUsed`、`executionModeUsed`、`sourceImageUsed`、`batchJobId`、`batchItemId`、`sizeName`、`status`（success / failed / skipped）、`error`**

### 5b) `overwrite: false` 時的 skip / resume

路徑以 **`finalDir = outputDir/final`**、**`rawDir = outputDir/raw`** 為準。

- `postprocessEnabled=true`：
  - 若 **`finalDir`** 內對應 **`.webp` final** 已存在 → **整個版型 skip**（不再打 Gemini、不再後處理）。
  - 若 **final `.webp` 不存在** 但 **`rawDir`** 內 **`__{name}__raw.png`** 已存在 → **略過 Gemini**，直接讀 raw 做本地後處理寫入 `finalDir`（適合 API 已成功但後處理中斷後重跑）。
- `postprocessEnabled=false`（raw-only）：
  - 若 **`rawDir`** 內對應 `__{name}__raw.png` 已存在 → **整個版型 skip**。
- `overwrite: true` 時 → 一律重打 Gemini（並在 `keepRaw: true` 時覆寫 raw），並重算 `finalDir` 內 WebP（不因 final 已存在而跳過）。

### 6) Prompt 組裝

程式在 **`internal/app/batch_gemini_prompt.go`** 組裝，順序為：

1. **`promptTemplate`**（config）
2. **標題／文字保留區塊**（程式內建）：要求保留原標題用字、可讀與視覺突出、適度放大標題區、**不得**相對原圖縮小標題、不替換拼寫或改寫、維持 logo／標題層級與行銷構圖。
3. **版型補句**：若該 size 有 **`sizePrompt`** 則使用並再附上一句標題提示；否則依 `name` 使用內建（**square**／**wide**／**tall** 皆含「標題要大、清楚、主導畫面」等語意）。

段落之間以空行連接；請勿只改 config 範例而不跑程式，實際送出的文字以上述程式為準。

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
| `finalFormat ... is not supported` | 目前僅支援 `finalFormat: "webp"` |
| 請求一直卡住 | 檢查是否設了 `timeoutMs: 0`（無 HTTP timeout）；若要避免久候可改為正數毫秒或省略鍵使用預設 120s |

---

## Quick Start

### 1) Requirements
- Go 1.24+（含 `batch-gemini` 所用之依賴）
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
