# Shinsou Sync 網頁前台

Shinsou Sync 的單頁應用：檢視同步後的書庫（唯讀）、管理帳號與裝置，以及實例管理後台。
技術：Vue 3（Composition API、`<script setup lang="ts">`）＋ Vite ＋ TypeScript ＋ vue-router，
沒有 UI 框架或 CSS 框架。建置後的檔案由 Go 伺服器以 `go:embed` 內嵌，與 API 同源提供。

規格見 [`../docs/spec/web.md`](../docs/spec/web.md)，HTTP 介面以 [`../api/openapi.yaml`](../api/openapi.yaml) 為準。

## 需求

- Node 24、npm 11

## 開發

```sh
npm ci            # 安裝依賴（依 package-lock.json）
npm run dev       # 啟動 Vite 開發伺服器 http://localhost:5173
```

開發伺服器會把 `/api` 與 `/.well-known` 代理到 `http://127.0.0.1:8787`。要指向其他後端：

```sh
SHINSOU_DEV_BACKEND=http://127.0.0.1:9000 npm run dev
```

## 產生 API 型別

所有回應與請求型別都由 OpenAPI 檔案產生，不手寫。修改 `../api/openapi.yaml` 之後執行：

```sh
npm run gen:api   # 輸出 src/api/schema.d.ts（需提交）
```

`src/api/types.ts` 只是對產生型別的別名。新增錯誤碼而沒有補上翻譯時，`vue-tsc` 會報錯（見 `src/i18n/index.ts`）。

## 檢查、測試、建置

```sh
npm run typecheck # vue-tsc --noEmit
npm test          # vitest（API 用戶端、refresh 去重與重試、轉址清理、#token= 解析等）
npm run build     # vue-tsc --noEmit && vite build，輸出到 dist/
```

建置會清空 `dist/` 再由 `public/.gitkeep` 補回 `dist/.gitkeep`（Go 端追蹤這個檔案，讓沒有建置前台時仍可編譯）。
**不要刪除 `public/.gitkeep`。**

## 結構

| 路徑 | 說明 |
|---|---|
| `src/api/` | `client.ts`（fetch 包裝：Bearer、401 重新整理並重試一次）、`session.ts`（記憶體內的 access token、Web Locks、refresh 去重）、`auth.ts`、`endpoints.ts`、`schema.d.ts`（產生） |
| `src/i18n/` | 繁體中文（預設與後備）與英文字典；鍵由 `zh-Hant.ts` 決定，`en.ts` 缺鍵會編譯失敗 |
| `src/stores/` | 實例資訊、主題 |
| `src/views/` | 各頁面；`views/admin/` 為管理後台 |
| `src/components/` | 共用元件（封面、對話框、表單欄位、分頁） |
| `src/styles/main.css` | 設計變數（含深色模式）與全部樣式 |

## 工作階段重點

- access token 只存在記憶體，不寫入 localStorage 或 cookie。
- 啟動時若有 `shinsou_csrf` cookie，先呼叫 `POST /api/v1/web/refresh`（標頭 `X-CSRF-Token`）再取得 `/me`，路由會等待這個步驟。
- 所有 `/web/refresh` 都在 `navigator.locks.request('shinsou-refresh', …)` 內執行，並在鎖內才讀取 CSRF cookie；同一分頁內的並行呼叫共用同一個 Promise。
- 重新整理失敗時清除工作階段並導向 `/login?redirect=…`；轉址目標只接受同源路徑。
- `/reset-password`、`/verify-email` 從 `location.hash` 讀取 `#token=…` 後立即用 `history.replaceState` 移除。

## CSP

伺服器以下列政策提供前台，前端不使用內嵌腳本、`eval`、外部字型或任何外部請求（封面圖片除外，僅限 `https:`）：

```
default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'
```

不要使用 `v-html` 顯示來自 API 的資料。
