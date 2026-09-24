# Discord VRChat Image Converter Bot 規格書

## 1. 專案概述

本專案是一個使用 Go 開發的 Discord Bot，用於自動處理 Discord 特定頻道中的圖片附件。

主要使用情境為 VRChat。部分 VRChat 世界中的圖片檢視器要求使用者提供一個直接指向圖片檔案的 HTTP URL，且圖片通常需要符合特定格式與尺寸限制，例如：

* 必須為 JPEG 或 PNG。
* 寬度不得超過指定值。
* 高度不得超過指定值。
* 可選擇限制最終檔案大小。
* URL 必須可以直接取得圖片內容。

目前使用流程通常為：

```text
手機或電腦上的原始圖片
→ 手動轉檔
→ 手動縮圖
→ 上傳 Discord
→ 複製 Discord attachment URL
→ 在 VRChat 中貼上 URL
```

本 Bot 的目標是將上述流程自動化為：

```text
使用者上傳圖片到指定 Discord 頻道
→ Bot 自動下載圖片
→ 自動旋轉、轉換格式、縮小尺寸
→ Bot 將處理後圖片重新上傳至 Discord
→ Bot 回覆可直接複製的 Discord CDN URL
→ 使用者在 VRChat 中直接貼上 URL
```

Bot 同時需要支援 Discord signed attachment URL 的更新機制。由於 Discord CDN attachment URL 具有有效期限，Bot 必須在輸出訊息上提供 `Refresh URL` 按鈕，讓使用者可以重新取得新的有效 URL。

---

# 2. 專案目標

本專案必須達成以下主要目標：

1. 自動偵測指定 Discord 頻道中的圖片附件。
2. 支援 JPEG、PNG、WebP、HEIC / HEIF 等常見圖片格式。
3. 將不相容格式轉換成 JPEG 或 PNG。
4. 根據設定限制圖片最大寬度與高度。
5. 保持圖片原始長寬比。
6. 預設不得放大原本較小的圖片。
7. 正確處理 EXIF orientation。
8. 預設將輸出色彩空間轉換為 sRGB。
9. 可選擇保留 alpha transparency。
10. 可選擇限制最終圖片檔案大小。
11. 每張圖片獨立產生一則 Bot 回覆。
12. 回覆必須包含處理後圖片附件。
13. 回覆必須包含可直接複製的 CDN URL code block。
14. 回覆必須顯示 CDN URL 的相對過期時間。
15. 回覆必須提供 `Refresh URL` 按鈕。
16. Bot 的功能設定必須可以透過 Discord slash command 管理。
17. 設定必須在 Bot restart 後保留。
18. Bot 必須可以透過 Docker 部署於 Linux server。

---

# 3. 非目標

以下項目不屬於第一版實作範圍：

* 不建立 Web 管理介面。
* 不建立獨立圖片 CDN。
* 不使用 Cloudflare R2、S3 或其他 object storage。
* 不提供永久圖片 URL。
* 不處理訊息內的一般 HTTP / HTTPS 圖片 URL。
* 不建立 Redis。
* 不建立外部 queue service。
* 不建立 Kubernetes deployment。
* 不建立使用者帳號系統。
* 不建立圖片瀏覽 gallery。
* 不提供圖片長期保存機制。
* 不保證 Discord CDN URL 永久有效。
* 不提供圖片編輯功能，例如 crop、filter、watermark。
* 第一版不處理 animated image 的完整動畫輸出。

---

# 4. 技術選型

## 4.1 程式語言

使用：

```text
Go
```

建議使用目前穩定版本的 Go。

專案必須使用 Go Modules。

---

## 4.2 Discord SDK

使用：

```text
github.com/disgoorg/disgo
```

不得改用其他 Discord SDK，除非有明確技術原因並在 PR 中說明。

Bot 必須使用 Discord Gateway 接收一般 guild message events，並使用 Discord interactions 處理 slash command 與 button interaction。

---

## 4.3 圖片處理

使用：

```text
github.com/davidbyttow/govips/v2/vips
```

底層依賴：

```text
libvips
libheif
HEVC decoder
```

Linux container 必須確認 libvips build/runtime 支援 HEIC / HEIF decode。

推薦使用 Debian 或 Ubuntu 系列 base image，以降低 libvips 與 HEIC plugin 安裝複雜度。

---

## 4.4 資料庫

使用 SQLite。

Go 存取方式使用：

```text
database/sql
```

可以選擇成熟的 SQLite driver，但不使用 ORM。

SQLite 只用於儲存設定與 schema migration 狀態。

圖片與 Discord message 對應關係不需要存入資料庫。

---

# 5. 整體架構

```text
                           Discord
                              │
             ┌────────────────┴────────────────┐
             │                                 │
      Gateway Message                    Interaction
             │                                 │
             ▼                                 ▼
      Message Handler                  Command / Button Handler
             │                                 │
             ▼                                 ▼
      Attachment Filter                  Config Service
             │                                 │
             ▼                                 ▼
        Worker Pool                      SQLite Database
             │
             ▼
       Image Processor
          govips
             │
             ▼
       Discord Upload
             │
             ▼
      Result Message
             │
             ▼
       Signed CDN URL
```

主要 package 應保持鬆耦合。

Discord event handler 不應直接包含大量圖片處理邏輯。

---

# 6. 建議專案結構

```text
.
├── cmd/
│   └── bot/
│       └── main.go
│
├── internal/
│   ├── bot/
│   │   ├── bot.go
│   │   ├── messages.go
│   │   ├── interactions.go
│   │   └── commands.go
│   │
│   ├── config/
│   │   ├── model.go
│   │   ├── service.go
│   │   └── defaults.go
│   │
│   ├── database/
│   │   ├── database.go
│   │   ├── migrations.go
│   │   └── guild_config.go
│   │
│   ├── image/
│   │   ├── processor.go
│   │   ├── decoder.go
│   │   ├── resize.go
│   │   ├── encoder.go
│   │   └── limits.go
│   │
│   ├── discordurl/
│   │   ├── expiry.go
│   │   └── message.go
│   │
│   └── worker/
│       └── pool.go
│
├── migrations/
│
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── README.md
└── SPEC.md
```

不要求完全遵守上述目錄名稱，但責任分離應大致相同。

---

# 7. Discord Bot 權限

Bot 應只要求實際需要的權限。

預期需要：

* View Channels
* Send Messages
* Send Messages in Threads，如果未來要支援 thread
* Attach Files
* Read Message History
* Use Application Commands

若 Bot 要自動監聽一般頻道訊息中的 attachments，需要啟用 Discord Gateway 的 Message Content Intent。

Bot 不應要求：

* Administrator
* Manage Messages
* Manage Channels

除非未來有新需求。

---

# 8. Discord 頻道監聽行為

Bot 只處理被加入 allowlist 的 Discord channel。

每個 guild 可以設定多個允許的 channel。

當 Bot 收到 message create event 時，處理流程如下：

```text
收到 MessageCreate
→ 是否為 guild message
→ 是否來自 bot
→ channel 是否在 allowlist
→ 是否包含 attachment
→ 逐一檢查 attachment
→ 對支援的圖片建立轉換工作
```

Bot 必須忽略所有 bot message，包括自己產生的 message。

這是 MUST requirement，以避免無限轉換迴圈。

---

# 9. 多附件行為

Discord 一則訊息可能包含多個 attachment。

每張圖片必須獨立處理。

例如：

```text
User message
├── image1.heic
├── image2.png
└── image3.jpg
```

Bot 必須建立：

```text
Bot reply #1 → image1
Bot reply #2 → image2
Bot reply #3 → image3
```

不得將多張圖片合併成同一個輸出 attachment。

---

# 10. 圖片輸入格式

第一版至少支援：

* JPEG
* PNG
* WebP
* HEIC
* HEIF

可以額外支援 libvips 已支援的其他靜態圖片格式，但不是 acceptance criteria。

不得單純根據：

* 副檔名
* Discord Content-Type

判斷圖片格式。

實際格式應由 libvips decode 結果確認。

---

# 11. Animated image

GIF、animated WebP 等格式在第一版不要求保留動畫。

如果輸入為動畫圖片，可以：

```text
讀取第一 frame
→ 轉換為靜態圖片
```

未來可以另外增加完整 animated image support。

---

# 12. 圖片處理 Pipeline

標準 image processing pipeline：

```text
Download
→ Decode
→ Autorotate
→ Validate dimensions
→ Normalize color space
→ Determine output format
→ Resize if required
→ Strip metadata if configured
→ Encode
→ Check file-size constraint
→ Retry encode / resize if required
→ Upload
```

處理順序不得隨意改變，尤其 autorotate 必須在最終尺寸計算之前完成。

---

# 13. EXIF Orientation

Bot 必須處理手機照片常見的 EXIF orientation。

例如實際 pixel matrix：

```text
4032 × 3024
```

但 EXIF 表示需要旋轉 90 度時，後續尺寸判斷必須使用：

```text
3024 × 4032
```

而不是原始未旋轉尺寸。

輸出圖片應實際旋轉 pixel data，而不是依賴 EXIF orientation metadata。

---

# 14. 色彩空間

輸出圖片應統一轉換為：

```text
sRGB
```

這是固定 pipeline 行為，而非使用者設定。

目的是提高不同 VRChat image viewer 與顯示環境的相容性。

---

# 15. Metadata

預設：

```text
strip_metadata = true
```

應移除：

* EXIF metadata
* GPS location
* camera model
* capture timestamp
* 不必要的 embedded metadata

這可以避免使用者手機照片中的位置資訊被重新公開。

此行為可以透過 slash command 關閉。

---

# 16. 圖片尺寸限制

設定包含：

```text
max_width
max_height
```

預設值：

```text
1500
1500
```

這只是預設值，必須可以修改。

Resize 必須：

* 保持 aspect ratio。
* 不 crop。
* 不 stretch。
* 預設不 upscale。

例如：

```text
3000 × 2000
→ 1500 × 1000
```

```text
1000 × 3000
→ 500 × 1500
```

```text
1200 × 800
→ 1200 × 800
```

公式概念：

```text
scale = min(
    max_width / width,
    max_height / height,
    1.0
)
```

---

# 17. Alpha Transparency

設定：

```text
preserve_alpha
```

預設：

```text
true
```

當圖片含有 alpha channel：

若：

```text
preserve_alpha = true
```

則必須輸出 PNG。

若：

```text
preserve_alpha = false
```

則可以輸出 JPEG。

轉成 JPEG 前必須先將 transparent pixels flatten 到背景色。

預設背景：

```text
#FFFFFF
```

第一版不需要提供背景色設定。

---

# 18. 輸出格式策略

輸出必須只使用：

```text
JPEG
PNG
```

建議決策流程：

```text
Input JPEG
→ JPEG

Input HEIC / HEIF
→ JPEG

Input WebP without alpha
→ JPEG

Input WebP with alpha
→ PNG if preserve_alpha=true

Input PNG with alpha
→ PNG if preserve_alpha=true

Input PNG without alpha
→ PNG

Any image with alpha and preserve_alpha=false
→ JPEG
```

若開啟最大檔案大小限制，且 PNG 無法符合限制，可以在不需要 alpha 的情況下轉成 JPEG。

---

# 19. JPEG Quality

設定：

```text
jpeg_quality
```

預設：

```text
90
```

允許範圍建議：

```text
1–100
```

Slash command 應拒絕範圍外的值。

---

# 20. 最大輸出檔案大小

最大輸出檔案大小為 optional configuration。

資料庫可表示為：

```text
NULL
```

代表：

```text
Disabled
```

啟用時，例如：

```text
4 MiB
```

Bot 必須盡可能讓最終輸出符合此限制。

---

# 21. JPEG 檔案大小控制

若 JPEG encode 後超過最大大小：

第一階段：

```text
保持 dimensions
→ 降低 JPEG quality
```

應使用合理搜尋策略，例如 binary search，而不是逐一測試每個 quality。

建議最低 quality：

```text
40
```

若降低至最低 quality 後仍然超過限制：

```text
逐步降低 dimensions
→ 重新嘗試 quality search
```

必須設定合理的最大 retry 次數，以避免無限迴圈。

---

# 22. PNG 檔案大小控制

PNG 為 lossless format，單純調整 compression level 不一定能顯著降低容量。

若圖片含 alpha 且：

```text
preserve_alpha = true
```

則：

```text
縮小 dimensions
→ encode PNG
→ 檢查檔案大小
→ 必要時再次縮小
```

如果圖片沒有 alpha，則允許在為了符合 file size constraint 時轉成 JPEG。

---

# 23. 最低合理尺寸

為避免極端情況一路縮成幾個 pixel，應定義最低 dimension。

建議：

```text
minimum_dimension = 64
```

如果縮小到此限制仍然無法符合最大檔案大小，則轉換失敗。

---

# 24. `/convert` 指令

除自動監聽指定頻道外，Bot 必須提供：

```text
/convert
```

參數：

```text
image: Attachment
```

行為：

```text
/convert image:<attachment>
```

使用目前 guild 的 config 處理圖片。

此功能允許使用者不必將圖片上傳到 allowlist channel。

輸出格式應與自動模式相同。

---

# 25. Slash Command 設定系統

所有功能性設定必須透過 Discord slash command 管理。

統一使用：

```text
/config
```

command group。

---

# 26. `/config show`

```text
/config show
```

顯示目前 guild config。

範例：

```text
Image Converter Configuration

Channels
#vrchat-images
#screenshots

Maximum dimensions
1500 × 1500 px

Maximum output file size
Disabled

JPEG quality
90

Preserve transparency
Enabled

Strip metadata
Enabled
```

建議以 Discord embed 呈現。

response 應為 ephemeral。

---

# 27. `/config channel add`

```text
/config channel add channel:#channel
```

加入 auto conversion allowlist。

若已存在，應回覆：

```text
Channel is already enabled.
```

response 為 ephemeral。

---

# 28. `/config channel remove`

```text
/config channel remove channel:#channel
```

從 allowlist 移除。

response 為 ephemeral。

---

# 29. `/config channel list`

```text
/config channel list
```

列出所有啟用 auto conversion 的 channel。

response 為 ephemeral。

---

# 30. `/config dimensions`

```text
/config dimensions width:<integer> height:<integer>
```

例如：

```text
/config dimensions width:1500 height:1500
```

必須驗證：

```text
width > 0
height > 0
```

並設定合理上限，例如：

```text
16384
```

---

# 31. `/config max-file-size`

建議設計：

```text
/config max-file-size enabled:true size-mib:4
```

以及：

```text
/config max-file-size enabled:false
```

當 disabled 時，資料庫寫入：

```text
NULL
```

---

# 32. `/config preserve-alpha`

```text
/config preserve-alpha enabled:true
```

或：

```text
/config preserve-alpha enabled:false
```

---

# 33. `/config jpeg-quality`

```text
/config jpeg-quality quality:90
```

允許範圍：

```text
1–100
```

---

# 34. `/config strip-metadata`

```text
/config strip-metadata enabled:true
```

或：

```text
/config strip-metadata enabled:false
```

---

# 35. `/config reset`

```text
/config reset
```

將目前 guild 恢復成預設設定。

建議需要 Discord confirmation button：

```text
Reset configuration?
[Confirm] [Cancel]
```

不得直接執行不可逆 reset。

---

# 36. Slash Command 權限

所有 `/config` commands 預設只允許具有：

```text
Manage Guild
```

權限的成員使用。

`/convert` 不需要 Manage Guild。

若權限不足，應回覆 ephemeral error。

---

# 37. Guild Scope

所有功能設定均為：

```text
per guild
```

不可為 global shared config。

例如：

```text
Guild A
max_width = 1500

Guild B
max_width = 2048
```

兩者互不影響。

---

# 38. Default Config

新 guild 第一次使用時，建立預設 config：

```text
max_width = 1500
max_height = 1500
max_file_size_bytes = NULL
jpeg_quality = 90
preserve_alpha = true
strip_metadata = true
```

allowlist 預設為空。

因此 Bot 加入 guild 後，不會自動監聽任何 channel，必須由管理員明確執行：

```text
/config channel add
```

---

# 39. SQLite Schema

建議 schema：

```sql
CREATE TABLE guild_configs (
    guild_id TEXT PRIMARY KEY,
    max_width INTEGER NOT NULL,
    max_height INTEGER NOT NULL,
    max_file_size_bytes INTEGER NULL,
    jpeg_quality INTEGER NOT NULL,
    preserve_alpha INTEGER NOT NULL,
    strip_metadata INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

Channel allowlist：

```sql
CREATE TABLE allowed_channels (
    guild_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,

    PRIMARY KEY (guild_id, channel_id),

    FOREIGN KEY (guild_id)
        REFERENCES guild_configs(guild_id)
        ON DELETE CASCADE
);
```

Discord snowflake ID 應以 decimal string 儲存，避免跨 DB / driver 時的 unsigned integer 相容性問題。

---

# 40. Database Migration

Bot 必須具有簡單 migration 機制。

至少保存 schema version。

Migration 必須：

* 可重複安全執行。
* 在 Bot startup 時自動執行。
* migration failure 時停止 startup。

不得在 schema 不確定的狀態下繼續啟動 Discord client。

---

# 41. Bot 回覆格式

每張成功處理的圖片建立一則獨立 reply。

建議訊息：

````text
Converted: 3024×4032 → 1125×1500 · JPEG · 1.8 MiB

```
https://cdn.discordapp.com/attachments/...
```

URL expires <t:1790232540:R>
````

並包含：

```text
[Refresh URL]
```

按鈕。

同一則 Discord message 同時包含：

* 處理後圖片 attachment
* URL code block
* expiration timestamp
* Refresh URL button

---

# 42. Discord URL Code Block

URL 必須單獨存在於 code block。

不得加入其他文字，例如：

```text
URL: https://...
```

應使用：

````text
```
https://cdn.discordapp.com/...
```
````

目的是讓 VR 使用者可以方便選取與複製。

---

# 43. CDN URL Expiration

Discord attachment URL 包含 signed query parameters。

Bot 應從：

```text
ex
```

query parameter 解析 expiration timestamp。

`ex` 為 hexadecimal Unix timestamp。

概念：

```go
expiresAt, err := strconv.ParseInt(ex, 16, 64)
```

Discord timestamp：

```text
<t:TIMESTAMP:R>
```

例如：

```text
URL expires <t:1790232540:R>
```

Discord client 會顯示類似：

```text
URL expires in 4 minutes
```

---

# 44. Refresh URL Button

每則成功輸出訊息必須包含：

```text
Refresh URL
```

按鈕。

建議 custom ID：

```text
image_refresh_url
```

不需要將 attachment URL、message ID 或 image ID 編碼進 custom ID。

Interaction 本身即可取得 message context。

---

# 45. Refresh URL Flow

當使用者按下 `Refresh URL`：

```text
Button Interaction
→ Identify current message
→ Fetch current Discord message through API
→ Read attachments[0]
→ Obtain refreshed signed attachment URL
→ Parse new expiration
→ Edit the same Discord message
→ Replace code block
→ Replace expiration timestamp
```

不得建立新的圖片。

不得重新 encode。

不得重新 upload。

不得新增另一則 result message。

必須 edit 原本的 result message。

---

# 46. Refresh URL State

Refresh URL 功能必須是 stateless。

Bot 不應為此功能在 SQLite 保存：

* message ID
* attachment ID
* CDN URL
* expiration timestamp

Bot restart 後，舊的 `Refresh URL` button 仍必須可以正常使用。

---

# 47. Refresh URL Error Handling

可能失敗情況：

* 原 Bot message 已被刪除。
* attachment 已不存在。
* Discord API error。
* message 不包含預期 attachment。
* URL 不包含可解析的 expiration。

若 refresh 無法完成，應透過 ephemeral interaction response 告知使用者。

不得破壞原本訊息。

---

# 48. 初始 Result Message 建立流程

由於 CDN URL 必須在 upload 後才能取得，建議流程：

```text
1. Bot send reply + converted attachment
2. Discord API returns created message
3. Read created message attachment URL
4. Parse expiration
5. Edit created message
6. Add URL code block
7. Add expiration
8. Add Refresh URL button
```

如果第二次 edit 失敗，圖片本身仍已成功上傳。

Bot 應 log error。

可以嘗試有限次 retry。

---

# 49. Reply 關聯

自動模式的 result message 應 reply 到原始使用者 message。

如此可以清楚知道是哪一張來源圖片。

`/convert` 則應透過 interaction response 或 follow-up message 回傳。

---

# 50. 原始圖片

Bot 不應刪除使用者原始 message 或 attachment。

Bot 不需要 Manage Messages permission。

---

# 51. Worker Pool

圖片處理可能消耗大量：

* CPU
* RAM
* native libvips resource

因此不得對所有 attachment 無限制啟動 goroutine。

需要 bounded worker pool。

Runtime configuration：

```text
WORKER_COUNT
```

建議預設：

```text
2
```

可以依部署機器 CPU 調整。

---

# 52. Queue

第一版使用 in-memory queue。

不需要 Redis。

queue 應有有限容量，例如：

```text
WORK_QUEUE_SIZE=32
```

當 queue 已滿，可回覆：

```text
Image processing queue is currently full. Please try again later.
```

---

# 53. Input Resource Limits

為避免 decompression bomb 或超大型圖片耗盡 RAM，必須限制：

```text
MAX_INPUT_FILE_SIZE
MAX_INPUT_PIXELS
```

這是 infrastructure safety configuration，不是 guild user configuration。

建議預設：

```text
MAX_INPUT_FILE_SIZE=50 MiB
MAX_INPUT_PIXELS=100000000
```

即約 100 megapixels。

實際值可以依部署環境調整。

---

# 54. HTTP Download

Discord attachment download 必須：

* 使用 context timeout。
* 限制 response body 大小。
* 檢查 HTTP status。
* 不 follow 任意外部 URL。

只下載 Discord event 中提供的 attachment URL。

第一版不得接受使用者自訂 HTTP URL，以降低 SSRF 風險。

---

# 55. Temporary Files

優先使用 memory buffer 或 libvips streaming 能力。

若圖片太大需要 temporary file：

* 使用 OS temporary directory。
* 使用隨機 filename。
* 處理完成後立即刪除。
* 不信任使用者提供 filename。

---

# 56. Filename

輸出檔名應避免直接信任原始名稱。

建議：

```text
converted-<short-id>.jpg
```

或：

```text
<sanitized-original-name>-converted.jpg
```

若使用原名稱，必須 sanitize。

---

# 57. Discord Upload Size

Bot 必須處理 Discord account/guild upload size limit。

若輸出圖片超過 Discord 可接受大小且沒有設定更嚴格的 max file size，仍可能 upload failure。

此時回覆清楚錯誤：

```text
The converted image is too large to upload to Discord.
```

不要 panic。

---

# 58. Error Message UX

使用者可理解的錯誤應直接回覆 Discord。

例如：

```text
Unsupported image format.
```

```text
Failed to decode the image.
```

```text
The image is too large to process safely.
```

```text
Unable to reduce the image below the configured file-size limit.
```

```text
Failed to upload the converted image.
```

內部 error details 不應直接暴露給使用者。

完整 stack / wrapped error 只寫入 log。

---

# 59. Logging

Log 至少包含：

* startup
* shutdown
* guild ID
* channel ID
* source message ID
* processing duration
* input format
* output format
* input dimensions
* output dimensions
* input file size
* output file size
* errors

不得記錄：

* Discord bot token
* 完整 signed CDN URL

若需要 debug URL，只記錄 host/path 或 attachment ID。

---

# 60. Runtime Configuration

以下為 infrastructure/runtime configuration，不由 Discord slash command 管理：

```text
DISCORD_BOT_TOKEN
DATABASE_PATH
LOG_LEVEL
WORKER_COUNT
WORK_QUEUE_SIZE
MAX_INPUT_FILE_SIZE
MAX_INPUT_PIXELS
```

可以額外提供：

```text
DISCORD_APPLICATION_ID
```

若 SDK 需要。

---

# 61. Environment Variable Example

```env
DISCORD_BOT_TOKEN=...
DATABASE_PATH=/data/bot.db

LOG_LEVEL=info

WORKER_COUNT=2
WORK_QUEUE_SIZE=32

MAX_INPUT_FILE_SIZE=52428800
MAX_INPUT_PIXELS=100000000
```

不得提供真實 token 到 repository。

---

# 62. Startup Capability Check

Bot startup 時必須檢查 libvips 能力。

至少確認：

```text
JPEG decode
JPEG encode
PNG decode
PNG encode
WebP decode
HEIF / HEIC decode
```

若 HEIC decode 不支援，startup 應明確 log warning 或直接 failure。

由於 HEIC 為核心需求，建議採：

```text
fail startup
```

而不是 silent degraded mode。

---

# 63. Docker

必須提供：

```text
Dockerfile
```

建議使用 multi-stage build。

概念：

```text
builder image
├── Go compiler
├── gcc
├── pkg-config
├── libvips-dev
└── required native headers

runtime image
├── libvips runtime
├── libheif
├── HEVC decoder
└── bot binary
```

由於 govips 使用 CGO：

```text
CGO_ENABLED=1
```

---

# 64. HEIC Docker Dependency

Docker image 必須確認存在：

```text
libvips
libheif
libde265 或等效 HEVC decoder
```

具體 package 名稱依 Linux distribution 而異。

Docker build 後應使用 automated test 確認 HEIC sample 可以成功 decode。

不能只確認 package 安裝成功。

---

# 65. Docker Persistent Volume

SQLite 必須存放於 persistent volume，例如：

```yaml
volumes:
  - ./data:/data
```

database path：

```text
/data/bot.db
```

container restart 不得造成 config 遺失。

---

# 66. Graceful Shutdown

收到：

```text
SIGTERM
SIGINT
```

時：

```text
Stop accepting new jobs
→ Close Discord connection
→ Allow active image processing jobs reasonable time to finish
→ Close database
→ Shutdown
```

Docker stop 必須正常運作。

---

# 67. Health Check

第一版 SHOULD 提供簡單 health check。

可以：

* HTTP health endpoint

或

* Docker process health check

若建立 HTTP endpoint，可使用：

```text
GET /healthz
```

回覆：

```json
{"status":"ok"}
```

但這是 SHOULD，不是第一版 MUST。

---

# 68. Discord Command Registration

Slash commands 必須在 startup 時註冊或同步。

開發環境可以支援：

```text
guild scoped commands
```

以取得較快 propagation。

production 可改為 global commands。

應避免每次 startup 無條件建立 duplicate commands。

---

# 69. Concurrency Safety

config cache、worker pool 與 database operation 必須是 concurrency-safe。

如果建立 config in-memory cache：

```text
guild_id → config
```

更新 slash command 後必須立即 invalidation 或 update cache。

不得要求 restart 才套用設定。

---

# 70. Configuration Cache

可以建立簡單 in-memory cache。

但 SQLite 必須是 source of truth。

cache miss：

```text
read SQLite
→ cache
```

slash command update：

```text
write SQLite
→ update/invalidate cache
```

---

# 71. Testing Strategy

需要：

* unit tests
* integration tests
* image fixture tests

不得只依靠 manual Discord testing。

---

# 72. Resize Unit Tests

測試：

```text
3000×2000, max 1500×1500
→ 1500×1000
```

```text
1000×3000
→ 500×1500
```

```text
1200×800
→ 1200×800
```

```text
1500×1500
→ 1500×1500
```

---

# 73. Orientation Tests

準備帶 EXIF orientation 的 JPEG fixture。

確認：

```text
orientation applied
→ dimensions correctly swapped
→ output orientation correct
→ EXIF orientation no longer required
```

---

# 74. Format Tests

至少測試：

```text
JPEG → JPEG
PNG opaque → PNG
PNG alpha + preserve=true → PNG
PNG alpha + preserve=false → JPEG
WebP opaque → JPEG
WebP alpha → PNG
HEIC → JPEG
```

---

# 75. Metadata Test

使用含 EXIF GPS 的 fixture。

在：

```text
strip_metadata=true
```

時確認輸出不包含 GPS metadata。

---

# 76. File Size Tests

測試 JPEG：

```text
max size enabled
→ quality reduced
→ final size <= configured maximum
```

測試超大圖片：

```text
quality reduction insufficient
→ dimensions reduced
→ final size <= limit
```

測試 impossible case：

```text
minimum dimension reached
→ return controlled failure
```

---

# 77. URL Expiry Parser Tests

輸入：

```text
...?ex=6ABCD123&is=...&hm=...
```

確認：

```text
hex parse
→ Unix timestamp
```

缺少 `ex`：

```text
return error
```

invalid hex：

```text
return error
```

---

# 78. Discord Handler Tests

需抽象 Discord API client，使 handler 可以 mock。

測試：

```text
bot message ignored
```

```text
non-allowed channel ignored
```

```text
allowed channel image accepted
```

```text
multiple attachments create multiple jobs
```

```text
non-image attachment ignored
```

---

# 79. Refresh Button Tests

測試：

```text
button pressed
→ message fetched
→ attachment URL read
→ expiry parsed
→ same message edited
```

並確認：

```text
no new attachment upload
```

```text
no new Discord message created
```

---

# 80. Config Permission Tests

確認：

```text
user without Manage Guild
→ cannot modify config
```

```text
user with Manage Guild
→ can modify config
```

---

# 81. Acceptance Criteria

第一版完成必須滿足以下條件。

## 自動轉換

管理員執行：

```text
/config channel add channel:#images
```

之後使用者在 `#images` 上傳：

```text
IMG_1234.HEIC
```

Bot 必須：

1. 成功下載。
2. 成功 decode HEIC。
3. 正確 autorotate。
4. 轉換為 sRGB。
5. 根據尺寸設定縮小。
6. 轉成 JPEG。
7. 上傳至 Discord。
8. reply 原 message。
9. 顯示處理後圖片。
10. 顯示 URL code block。
11. 顯示 URL expiration relative timestamp。
12. 顯示 Refresh URL button。

---

## Refresh URL

使用者等待原 URL 過期或接近過期後：

```text
Press Refresh URL
```

Bot 必須：

1. 不重新處理圖片。
2. 不重新 upload。
3. 重新 fetch Discord message。
4. 取得新的 signed CDN URL。
5. edit 原 message。
6. 更新 code block。
7. 更新 expiration timestamp。

---

## 設定持久化

執行：

```text
/config dimensions width:2048 height:2048
```

restart Docker container 後：

```text
/config show
```

仍必須顯示：

```text
2048 × 2048
```

---

## HEIC

使用實際手機產生的 HEIC 圖片測試。

Docker production image 中必須成功處理。

不得只在 developer workstation 成功。

---

# 82. Security Requirements

MUST：

* 不信任 attachment filename。
* 不信任 MIME type。
* 限制 download body。
* 限制最大 pixel count。
* 不允許 arbitrary URL fetching。
* 不 log bot token。
* 不 log完整 signed CDN URL。
* 不允許一般使用者修改 guild config。
* Bot token 只能透過 secret / environment variable 提供。
* Docker image 不包含開發用 credentials。

---

# 83. Performance Requirements

本 Bot 預期為小型私人或社群 Discord server 使用。

不需要針對大規模 SaaS traffic 設計。

但仍必須：

* bounded concurrency
* bounded queue
* bounded input size
* graceful error handling

不得因單張異常圖片造成整個 process crash。

---

# 84. Observability

每次成功轉換建議輸出 structured log：

```json
{
  "event": "image_converted",
  "guild_id": "...",
  "channel_id": "...",
  "message_id": "...",
  "input_format": "heif",
  "output_format": "jpeg",
  "input_width": 3024,
  "input_height": 4032,
  "output_width": 1125,
  "output_height": 1500,
  "input_bytes": 2849132,
  "output_bytes": 712443,
  "duration_ms": 184
}
```

可以使用：

```text
log/slog
```

不需要額外 logging framework。

---

# 85. README Requirements

README 必須包含：

1. 專案用途。
2. Discord Developer Portal 建立 Bot 流程。
3. 必要 intents。
4. Bot permissions。
5. environment variables。
6. Docker build。
7. docker compose 啟動方式。
8. `/config` 設定範例。
9. `/convert` 使用方式。
10. HEIC dependency 說明。
11. Discord CDN URL 並非永久 URL 的說明。
12. `Refresh URL` 行為說明。

---

# 86. Docker Compose

建議提供：

```yaml
services:
  bot:
    build: .
    restart: unless-stopped
    environment:
      DISCORD_BOT_TOKEN: ${DISCORD_BOT_TOKEN}
      DATABASE_PATH: /data/bot.db
    volumes:
      - ./data:/data
```

不要將 token hard-code 在 compose file。

---

# 87. `.gitignore`

至少加入：

```text
.env
data/
*.db
*.db-shm
*.db-wal
```

---

# 88. SQLite Pragmas

建議啟用：

```sql
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
```

Bot 為單 process 為主，不需要複雜 connection pool。

---

# 89. Context 與 Timeout

所有外部操作都必須有 context timeout。

例如：

```text
Discord attachment download
Discord API request
image processing job
```

不要使用永久不會取消的 blocking operation。

圖片處理可設定較長 timeout，例如：

```text
30 seconds
```

---

# 90. Go Error Handling

應使用 wrapped error：

```go
return fmt.Errorf("decode image: %w", err)
```

不得大量使用：

```go
panic
```

除非 startup 發生無法恢復的初始化錯誤。

---

# 91. 不應過度抽象

AI agent 實作時應避免：

* repository pattern 套每一個 table
* generic service framework
* dependency injection framework
* event bus
* CQRS
* microservices
* unnecessary interfaces

只有需要 mock Discord API 或 image processor 等明確邊界時才建立 interface。

---

# 92. 建議實作順序

AI agent 應依以下順序開發。

## Phase 1

建立：

```text
Go project
Docker
SQLite
configuration model
```

## Phase 2

建立 Discord Bot：

```text
Gateway
Message handler
slash command registration
/config show
```

## Phase 3

實作：

```text
channel allowlist
config commands
```

## Phase 4

實作 govips：

```text
JPEG
PNG
WebP
HEIC
autorotate
resize
sRGB
metadata stripping
```

## Phase 5

串接：

```text
Discord attachment
→ processor
→ Discord upload
```

## Phase 6

實作：

```text
URL code block
expiration parser
Refresh URL button
```

## Phase 7

實作：

```text
max file size
JPEG quality search
dimension reduction
```

## Phase 8

完成：

```text
tests
README
docker-compose
graceful shutdown
logging
```

---

# 93. Definition of Done

專案只有在以下全部完成後才算完成：

* `go test ./...` 通過。
* Docker image build 成功。
* Production Docker image 可 decode HEIC。
* SQLite config restart 後保留。
* allowlist 正常運作。
* 多圖片訊息正常處理。
* 每張圖片獨立 reply。
* JPEG / PNG / WebP / HEIC fixture 測試通過。
* dimensions limit 正常運作。
* max file-size optional setting 正常運作。
* preserve alpha 正常運作。
* strip metadata 正常運作。
* `/convert` 正常運作。
* `/config` commands 正常運作。
* URL expiration 顯示正常。
* Refresh URL button 正常。
* Bot restart 後舊 Refresh URL button 仍可使用。
* Docker SIGTERM graceful shutdown 正常。
* README 足以讓新使用者完成部署。

---

# 94. AI Agent 實作要求

AI coding agent 在實作本規格時：

1. 優先遵守本文件定義的使用者可觀察行為。
2. 不得自行將 Discord attachment 改成外部 object storage。
3. 不得移除 HEIC support。
4. 不得以 pure-Go 圖片 library 取代 govips，除非仍能完整滿足 HEIC 與本規格所有需求，且需明確說明原因。
5. 不得將 guild config 改成單一 global config。
6. 不得將功能設定改為只透過 environment variable 管理。
7. Discord credentials 等 infrastructure secret 不得存入 SQLite。
8. 不得為 Refresh URL 建立不必要的 persistent state。
9. 圖片處理不得無限制建立 goroutine。
10. 不得信任副檔名或 MIME type 作為唯一格式判斷依據。
11. 所有重要 failure path 都必須回傳 error，而非 silent failure。
12. 必須建立測試 fixture，尤其 HEIC、EXIF orientation、transparent PNG 與 metadata。
13. 若實作過程發現 Discord API 或 libvips 的實際行為與本文件假設不同，應保留本文件的使用者需求，並以最小架構調整解決，不應靜默刪除功能。

---

# 95. 預設設定摘要

```text
Maximum width:
1500 px

Maximum height:
1500 px

Maximum output file size:
Disabled

JPEG quality:
90

Preserve transparency:
Enabled

Strip metadata:
Enabled

Auto rotate:
Always enabled

Output color space:
sRGB

Upscaling:
Disabled

Allowed channels:
None

Worker count:
2

Queue size:
32
```

---

# 96. 最終使用體驗

完成後，管理員只需要進行一次設定：

```text
/config channel add channel:#vrchat-images
/config dimensions width:1500 height:1500
```

之後使用者將 iPhone 拍攝的：

```text
IMG_9231.HEIC
```

丟進 `#vrchat-images`。

Bot 自動產生：

````text
Converted: 3024×4032 → 1125×1500 · JPEG · 846 KiB

```
https://cdn.discordapp.com/attachments/.../converted-a8f31c.jpg?ex=...
```

URL expires <t:1790232540:R>

[Refresh URL]
````

使用者只需要在 Virtual Desktop 中複製 code block 內的網址並貼到 VRChat。

如果網址過期，只需要回到 Discord：

```text
Press Refresh URL
```

原本的 Bot 訊息就會更新成新的有效 CDN URL，不需要重新上傳或重新轉換圖片。
