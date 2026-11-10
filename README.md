# Shinsou 同步服務

Shinsou X 的開源同步伺服器，任何人都可以自行架設。使用者可以註冊帳號，把書庫、章節狀態、閱讀進度與分類同步到多台裝置；之後也會支援同一伺服器內的作品分享與書籍評論。

> 狀態：P3（網頁前台與帳號管理）。帳號、裝置、同步（推送、拉取、即時通知）與背景清理之外，已加入網頁登入（cookie 工作階段）、電子郵件驗證與忘記密碼、唯讀書庫檢視、帳號匯出與刪除、管理後台（使用者、邀請碼、實例設定、稽核紀錄），以及編進執行檔的網頁前台；分享與評論尚未實作。

## 設計重點

- **伺服器可讀**：資料以 TLS 傳輸，伺服器能理解內容，才能做分享、評論與搜尋。請選擇你信任的伺服器。
- **只同步中繼資料**：章節內文與圖片不會上傳，各裝置從來源重新下載。
- **狀態式同步**：每筆記錄的每個欄位以 HLC 時間戳做 LWW 合併，拉取等冪，新裝置從序號 0 拉完即是完整狀態。

## 目錄

| 路徑 | 內容 |
|---|---|
| `api/openapi.yaml` | HTTP API 規格（協定的單一事實來源） |
| `api/vectors/` | 跨語言測試向量，伺服器（Go）與 App（Kotlin）都必須通過 |
| `docs/spec/` | 作品鍵、同步語意、記錄內容、網頁前台與帳號管理（`web.md`）、追蹤、封鎖與分享（`sharing.md`）的規格 |
| `migrations/` | PostgreSQL 資料表遷移 |
| `cmd/shinsou-server/` | 伺服器進入點（`serve`、`migrate`、`admin`） |
| `internal/` | 伺服器實作：`config`、`db`、`auth`、`syncsvc`、`notify`、`jobs`、`httpapi`、`instance`、`mail`、`settings`、`library`、`account`、`admin`、`social` |
| `web/` | 網頁前台（Vue 3 + Vite + TypeScript）；`npm run build` 輸出到 `web/dist`，由 `web/embed.go` 編進伺服器 |
| `deploy/` | `docker-compose.yml`（伺服器＋PostgreSQL 16） |
| `docs/postman/` | Postman 測試集合 |
| `internal/workkey` | 作品鍵參考實作 |
| `internal/hlc`、`internal/lww` | HLC 與欄位級 LWW 合併的參考實作 |

## 本機開發

需要 Go 1.25 以上與 PostgreSQL 16。

```bash
# 1. 準備資料庫
createdb shinsou_sync

# 2. 啟動（預設會自動執行資料庫遷移，可加 --no-migrate 關閉）
export SHINSOU_DATABASE_URL='postgres://user:password@127.0.0.1:5432/shinsou_sync?sslmode=disable'
export SHINSOU_LISTEN=127.0.0.1:8080
export SHINSOU_REGISTRATION=open        # open | invite | closed
go run ./cmd/shinsou-server serve
```

首次啟動且沒有管理員時，日誌會印出**一次性設定碼**（`setup_code`）。註冊時帶上 `setupCode`，該帳號就會成為管理員；之後不會再顯示。忘記時可重新產生（已有管理員時不可用）：

```bash
go run ./cmd/shinsou-server admin reset-setup-code
go run ./cmd/shinsou-server admin create-invite --max-uses 5 --expires 168h
go run ./cmd/shinsou-server admin set-role <username> <user|moderator|admin>
go run ./cmd/shinsou-server migrate     # 只執行遷移
```

### 環境變數

| 變數 | 預設 | 說明 |
|---|---|---|
| `SHINSOU_DATABASE_URL` | （必填） | PostgreSQL 連線字串 |
| `SHINSOU_LISTEN` | `127.0.0.1:8080` | 監聽位址 |
| `SHINSOU_PUBLIC_URL` | 空 | 對外網址，用於 `@user@host`、信件中的連結，以及網頁前台的來源檢查。放在終結 TLS 的反向代理後面時必填（例如 `https://sync.example.com`），否則網頁登入、註冊會回 `403 csrf_failed` |
| `SHINSOU_REGISTRATION` | `invite` | `open`、`invite`、`closed` |
| `SHINSOU_EMAIL_VERIFICATION` | 見說明 | `disabled`、`optional`、`required`。未設定時：可寄信且 `SHINSOU_REGISTRATION=open` 為 `required`，否則為 `disabled`。`required` 需要 SMTP，否則啟動失敗 |
| `SHINSOU_SMTP_HOST` | 空 | SMTP 主機；與 `SHINSOU_SMTP_FROM` 都設定後才會寄信（驗證信、重設密碼信），此時 `SHINSOU_PUBLIC_URL` 必填 |
| `SHINSOU_SMTP_PORT` | `587` | SMTP 連接埠 |
| `SHINSOU_SMTP_USERNAME` / `SHINSOU_SMTP_PASSWORD` | 空 | 兩者都留空時不做 SMTP 驗證；要設就一起設 |
| `SHINSOU_SMTP_FROM` | 空 | 寄件者，例如 `Shinsou <no-reply@example.com>` |
| `SHINSOU_SMTP_TLS` | `starttls` | `starttls`、`implicit`（465）或 `none` |
| `SHINSOU_TRUSTED_PROXIES` | 空 | 可信任 `X-Forwarded-For` 的反向代理（IP 或 CIDR，逗號分隔） |
| `SHINSOU_RECORD_QUOTA` | `200000` | 每位使用者的記錄數上限（`0` = 不限） |
| `SHINSOU_INSTANCE_NAME` | `Shinsou Sync` | 實例名稱；另有 `_DESCRIPTION`、`SHINSOU_TERMS_URL`、`SHINSOU_PRIVACY_URL`、`SHINSOU_CONTACT` |
| `SHINSOU_AUTH_RATE_PER_MINUTE` / `_BURST` | `30` / `10` | 每個 IP 的登入類端點限流 |
| `SHINSOU_PUSH_RATE_PER_MINUTE` / `_BURST` | `240` / `60` | 每位使用者的推送限流 |
| `SHINSOU_SHARING` | `enabled` | 使用者之間分享作品：`enabled` 或 `disabled`。關閉時 `/api/v1/shares` 回 `403 feature_disabled`；追蹤、封鎖與搜尋不受影響 |
| `SHINSOU_SHARES_PER_DAY` | `200` | 每位使用者 24 小時內可建立的分享數（`0` = 不限） |
| `SHINSOU_SOCIAL_RATE_PER_MINUTE` / `_BURST` | `120` / `40` | 每位使用者的使用者搜尋與建立分享限流 |
| `SHINSOU_JOB_INTERVAL` | `1h` | 背景工作間隔 |
| `SHINSOU_TOMBSTONE_RETENTION` | `2160h`（90 天） | 墓碑保留時間 |
| `SHINSOU_BATCH_RETENTION` | `24h` | 推送批次（冪等）保留時間 |
| `SHINSOU_ACCESS_TOKEN_TTL` / `SHINSOU_REFRESH_TOKEN_TTL` | `15m` / `2160h` | 權杖有效期 |
| `SHINSOU_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` |

名稱、描述、註冊政策、信箱驗證政策、分享開關、服務條款與隱私權政策網址、聯絡方式這幾項，管理員可在網頁後台於執行期間覆寫（立即生效、不需重啟）；環境變數是預設值，後台把某項重設即回到預設值。

### 網頁前台

前台是 `web/` 下的單頁應用，建置後以 `go:embed` 編進伺服器執行檔：

```bash
cd web && npm ci && npm run build && cd ..   # 輸出到 web/dist
go build -o bin/shinsou-server ./cmd/shinsou-server
SHINSOU_DATABASE_URL=... ./bin/shinsou-server serve
```

啟動後以瀏覽器開啟 `SHINSOU_PUBLIC_URL`（本機開發為 `http://127.0.0.1:8080`）即可。首次啟動、尚無管理員時，在註冊頁填入日誌中的設定碼，該帳號就是管理員；管理後台在登入後的 `/admin`。沒有先建置前台也能編譯與執行，只是網頁路徑會回 404，API 不受影響。Docker 映像會自動完成前台建置。

網頁登入把 refresh token 放在 `HttpOnly` cookie，access token 只存在頁面記憶體；請務必以 HTTPS 對外提供服務，並讓反向代理轉送 `Host` 標頭。

### 測試

整合測試需要一個空的 PostgreSQL 資料庫，每個測試會在其中建立獨立的 schema 並於結束後刪除；未設定 `SHINSOU_TEST_DATABASE_URL` 時會自動略過。

```bash
export SHINSOU_TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/shinsou_sync_test?sslmode=disable'
gofmt -l .            # 應無輸出
go vet ./...
go test -race ./...
```

### 以 Docker Compose 執行

```bash
cd deploy
cp .env.example .env     # 修改 POSTGRES_PASSWORD 與 SHINSOU_PUBLIC_URL
docker compose up -d
docker compose logs server | grep setup_code
```

`docs/postman/shinsou-sync.postman_collection.json` 涵蓋所有端點；登入或註冊後，測試腳本會自動保存權杖。

## 授權

[MIT](LICENSE)
