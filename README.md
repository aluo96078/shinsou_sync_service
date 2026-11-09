# Shinsou 同步服務

Shinsou X 的開源同步伺服器，任何人都可以自行架設。使用者可以註冊帳號，把書庫、章節狀態、閱讀進度與分類同步到多台裝置；之後也會支援同一伺服器內的作品分享與書籍評論。

> 狀態：P0（規格階段）。目前只有規格、測試向量與參考實作，還不能實際使用。

## 設計重點

- **伺服器可讀**：資料以 TLS 傳輸，伺服器能理解內容，才能做分享、評論與搜尋。請選擇你信任的伺服器。
- **只同步中繼資料**：章節內文與圖片不會上傳，各裝置從來源重新下載。
- **狀態式同步**：每筆記錄的每個欄位以 HLC 時間戳做 LWW 合併，拉取等冪，新裝置從序號 0 拉完即是完整狀態。

## 目錄

| 路徑 | 內容 |
|---|---|
| `api/openapi.yaml` | HTTP API 規格（協定的單一事實來源） |
| `api/vectors/` | 跨語言測試向量，伺服器（Go）與 App（Kotlin）都必須通過 |
| `docs/spec/` | 作品鍵、同步語意、記錄內容的規格 |
| `migrations/` | PostgreSQL 資料表遷移 |
| `cmd/shinsou-server/` | 伺服器進入點 |
| `internal/workkey` | 作品鍵參考實作 |
| `internal/hlc`、`internal/lww` | HLC 與欄位級 LWW 合併的參考實作 |

## 開發

需要 Go 1.25 以上。

```bash
go test ./...
```

```bash
go run ./cmd/shinsou-server --listen 127.0.0.1:8080
```

## 授權

[MIT](LICENSE)
