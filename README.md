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
│  └─ games.sample.json         (範本)
├─ CURSOR_FIRST_PROMPT.md
├─ cmd/
│  └─ game-asset-pipeline/
│     └─ main.go
└─ internal/
   ├─ app/
   │  ├─ app.go
   │  ├─ run.go
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
