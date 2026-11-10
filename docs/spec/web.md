# 規格：網頁前台、帳號管理與管理後台（P3）

HTTP 介面以 [`api/openapi.yaml`](../../api/openapi.yaml) 為準；本文件說明 OpenAPI 無法表達的行為。

## 1. 網頁工作階段

網頁前台沿用 App 的權杖系統（短效 access token＋會輪替的 refresh token），差別只在 refresh token 的存放位置。

| 項目 | 規則 |
|---|---|
| access token | 只存在頁面記憶體，以 `Authorization: Bearer` 送出；不放 cookie、不放 localStorage |
| `shinsou_refresh` cookie | refresh token。`HttpOnly`、`SameSite=Lax`、`Path=/api/v1/web`、`Max-Age` = refresh token 有效期 |
| `shinsou_csrf` cookie | CSRF 權杖，頁面可讀。`SameSite=Lax`、`Path=/`、同樣的 `Max-Age` |
| `Secure` | `SHINSOU_PUBLIC_URL` 是 `https://` 或該請求本身是 TLS 時加上 |

- `shinsou_csrf` 的值是 `base64url(HMAC-SHA256(csrfKey, refreshToken))`。`csrfKey` 是 32 位元組亂數，首次啟動時產生並存於 `instance_settings`（鍵 `web_csrf_key`）。
- `POST /web/refresh` 與（帶有 `shinsou_refresh` 時的）`POST /web/logout` 必須帶 `X-CSRF-Token`，伺服器以固定時間比較它與由 cookie 重新計算的值；不符回 `403 csrf_failed`。
- 四個 `/api/v1/web/*` 端點都檢查 `Origin`：有 `Origin` 標頭，而且它既不等於請求的 `Host`（同協定同主機）也不等於 `SHINSOU_PUBLIC_URL` 的來源時，回 `403 csrf_failed`。沒有 `Origin` 標頭時放行。
- `/web/register`、`/web/login` 建立的裝置 `platform` 固定為 `web`；`deviceName` 省略時，由 `User-Agent` 推導（例如 `Chrome on macOS`），推導不出來時用 `Web browser`。
- `/web/refresh` 失敗（401）時，回應同時清除兩個 cookie。`/web/logout` 一律清除兩個 cookie。
- `/web/register`、`/web/login`、`/web/refresh` 與對應的 `/auth/*` 端點共用每個 IP 的限流。
- refresh token 會輪替，已輪替的權杖再次出現會登出該瀏覽器。前端必須用 Web Locks API（`navigator.locks`，鎖名 `shinsou-refresh`）讓各分頁依序呼叫 `/web/refresh`；瀏覽器不支援時直接呼叫。

## 2. 電子郵件

### 2.1 設定

| 變數 | 預設 | 說明 |
|---|---|---|
| `SHINSOU_SMTP_HOST` | 空 | 設定後才會寄信 |
| `SHINSOU_SMTP_PORT` | `587` | |
| `SHINSOU_SMTP_USERNAME`、`SHINSOU_SMTP_PASSWORD` | 空 | 都留空時不做 SMTP 驗證 |
| `SHINSOU_SMTP_FROM` | 空 | 寄件者，例如 `Shinsou <no-reply@example.com>`；設定 SMTP 時必填 |
| `SHINSOU_SMTP_TLS` | `starttls` | `starttls`、`implicit`（465）或 `none` |

- 「可寄信」＝ `SHINSOU_SMTP_HOST` 與 `SHINSOU_SMTP_FROM` 都有值。此時 `SHINSOU_PUBLIC_URL` 必填（信中的連結需要它），否則啟動失敗。
- `SHINSOU_EMAIL_VERIFICATION` 未設定時的預設值：可寄信且 `SHINSOU_REGISTRATION=open` 時為 `required`，其餘為 `disabled`。
- 政策為 `required` 但不可寄信：啟動失敗（環境變數）或 `409 email_unavailable`（管理後台）。
- 實例資訊的 `features.email` 反映「可寄信」。

### 2.2 權杖

- 32 位元組亂數，以 base64url 表示；資料庫只存 SHA-256。
- 驗證信有效 48 小時，重設密碼信有效 1 小時，都只能使用一次。
- 連結把權杖放在 **URL fragment**，不會送到伺服器，也不會進日誌：
  - `<PUBLIC_URL>/verify-email#token=<token>`
  - `<PUBLIC_URL>/reset-password#token=<token>`
- 每個帳號、每種用途 60 秒內只寄一封。`POST /me/email/verification` 在冷卻中回 `429 rate_limited`（附 `Retry-After`）；`POST /auth/password/forgot` 在冷卻中靜默略過。
- 信件是純文字，同一封信含繁體中文與英文。
- 寄信一律在背景進行（有上限的佇列，滿了就丟棄並記錄日誌），請求不等待 SMTP：回應時間不會透露信箱是否存在，寄信失敗也只記錄日誌、不影響請求結果。
- 過期超過 7 天的權杖由背景工作清除。

### 2.3 流程

- **註冊**：政策為 `required` 時 `email` 必填（`422 email_required`）。政策不是 `disabled`、帳號有信箱且可寄信時，註冊後寄出驗證信。以設定碼建立的管理員也一樣寄信，但不受第 3 節的限制。
- **驗證**（`POST /auth/email/verify`）：權杖有效，且帳號目前的信箱仍等於寄信時的信箱 → 標記已驗證。權杖無效、過期或信箱已變更 → `400 invalid_token`。同一權杖重複送出、而該信箱已是驗證狀態時回 `204`（使用者重複點連結）。
- **重寄**（`POST /me/email/verification`）：帳號沒有信箱 → `422 email_required`；已驗證 → `409 conflict`；不可寄信 → `409 email_unavailable`。
- **變更信箱**（`PUT /me/email`）：需要目前的密碼（錯誤回 `401 invalid_credentials`）。新信箱一律變回未驗證，該帳號尚未使用的驗證與重設密碼權杖全部作廢，並依註冊的同樣條件寄驗證信；會寄信時同樣受 60 秒冷卻限制，冷卻中回 `429 rate_limited`（附 `Retry-After`），信箱不變。信箱沒有變化時不做任何事。`null` 表示移除信箱；政策為 `required` 時拒絕（`422 email_required`）。信箱已被使用 → `409 email_taken`。
- **忘記密碼**（`POST /auth/password/forgot`）：不可寄信 → `409 email_unavailable`；其餘一律 `204`，不透露信箱是否存在。只寄給已驗證的信箱；政策為 `disabled` 時也寄給未驗證的信箱。停用中的帳號不寄。
- **重設密碼**（`POST /auth/password/reset`）：權杖有效 → 設定新密碼、登出所有裝置、作廢該帳號其餘的重設權杖；信箱仍等於寄信時的信箱時，順便標記為已驗證。否則 `400 invalid_token`。

## 3. 信箱驗證限制

實例政策為 `required` 時，**信箱未驗證且不是管理員**的帳號呼叫下列端點會得到 `403 email_unverified`：

- `/api/v1/sync/*`（推送、拉取、串流）
- `/api/v1/library`、`/api/v1/library/{key}`、`/api/v1/categories`
- `/api/v1/me/export`

帳號管理端點（`/me`、`/me/usage`、`/me/password`、`/me/email`、`/me/email/verification`、`/me/delete`、`/devices`、登出）不受限制，使用者才能完成驗證或離開。政策由 `required` 改為其他值後，限制立即解除。

## 4. 實例設定

`name`、`description`、`registration`、`emailVerification`、`termsUrl`、`privacyUrl`、`contact` 可由管理員在執行期間覆寫。

- 分享（`sharing`）、評論（`reviews`）與訪客瀏覽（`visitorBrowsing`）三個開關同樣可在管理後台覆寫，值為 `enabled` 或 `disabled`；規則見 [sharing.md](sharing.md) 與 [reviews.md](reviews.md)。後台在評論關閉時停用訪客瀏覽的選項。
- 環境變數提供預設值；覆寫值存於 `instance_settings`（鍵 `name`、`description`、`registration`、`email_verification`、`terms_url`、`privacy_url`、`contact`），存在時優先。`PATCH /admin/settings` 對某個鍵送 `null` 即刪除覆寫、回到預設。
- 伺服器在記憶體中保存目前的有效值，啟動時載入、每次 `PATCH` 後更新；註冊、驗證政策與實例資訊都讀這份有效值，變更立即生效，不需重啟。（伺服器是單一行程，與即時通知相同的前提。）
- 驗證：`name` 1–80 字元；`description` 最多 500；`termsUrl`、`privacyUrl` 必須是 `http(s)://` 絕對網址、最長 2048，空字串視為「無」；`contact` 最長 200。
- 每次變更寫入 `audit_log`（`settings.update`，`detail` 記錄變更的鍵與新值）。
- 實例資訊的 `setupPending` 在尚無管理員時為 `true`，前台據此顯示設定碼欄位。

## 5. 書庫檢視

唯讀；資料全部來自同步記錄（[records.md](records.md)）。伺服器**不驗證欄位語意**，所以這些端點必須容忍任何形狀的欄位值：型別不符的欄位視為不存在，絕不可因此回 500（SQL 中不可直接把 JSON 轉型，先以 `jsonb_typeof` 判斷）。

- 只列出未刪除的 `library` 記錄。
- `title`：`customTitle`（非空字串）→ 作品描述的標題 → 記錄的 `title`（`local:` 條目）→ 空字串。`author`、`contentKind` 同理取作品描述或記錄欄位。
- `progression`、`currentUnitRemoteId` 來自同鍵、未刪除的 `progress` 記錄；`lastReadAt` 來自同鍵、未刪除的 `history` 記錄。
- `trackedUnits`、`readUnits`：同一作品、未刪除的 `unit` 記錄數，以及其中 `read` 為 `true` 的數量。`local:` 條目為 0。
- `q`：對上述 `title` 與 `author` 做不分大小寫的子字串比對（`%`、`_` 視為一般字元）。
- `category`：`categoryIds` 含該鍵；`default` 表示 `categoryIds` 不存在或為空陣列。
- `archived`：`exclude`（預設）、`include`、`only`。
- `sort`：`recent`＝`lastReadAt`、`addedAt`、記錄更新時間三者取第一個存在的值，新的在前；`title`＝標題不分大小寫遞增；`added`＝`addedAt` 新的在前（沒有的排最後）。相同時以 `key` 排序，分頁才穩定。
- `GET /library/{key}` 的 `units` 依更新時間新的在前，最多 5000 筆。
- `GET /categories` 列出未刪除的 `category` 記錄，依 `order`、`name` 排序；`name` 不是字串時用空字串，`order` 不是數字時用 0。

## 6. 帳號

- `GET /me/usage`：`total` 是全部記錄數（含墓碑，與配額計算一致）；`quota` 是該帳號的上限（個人設定優先，否則實例預設；不限時為 `null`）；`byKind` 一律列出七種 kind，值為未刪除的筆數。
- `GET /me/export`：在同一個唯讀交易中串流輸出，包含墓碑；`works` 只含這些記錄參照到的作品。
- `POST /me/delete`：需要目前的密碼。刪除帳號會連帶刪除裝置、權杖與同步記錄；共用的作品描述保留。唯一的啟用中管理員不可刪除自己（`409 last_admin`）。寫入 `audit_log`（`account.delete`，`detail.username`）。

## 7. 管理後台

所有 `/api/v1/admin/*` 端點要求角色為 `admin`，否則 `403 forbidden`。角色以資料庫目前的值為準。

- **最後一位管理員**：把最後一位「啟用中」的管理員降級、停權或刪除一律回 `409 last_admin`；檢查與變更在同一個交易中進行，並鎖定管理員列，避免兩個請求同時通過。
- **停權**（`status = suspended`）會同時登出該使用者的所有裝置。
- `recordQuota`：`null` 清除個人設定；`0` 表示不限。
- `emailVerified = true` 但帳號沒有信箱 → `422 validation_failed`。
- 邀請碼：明碼只在建立時回傳一次，資料庫只存雜湊。`expiresInHours` 省略或 `null` 表示不過期。列表依建立時間新的在前，含已用完與已過期的。
- 稽核紀錄的 `action`：`user.update`、`user.delete`、`user.sign_out`、`invite.create`、`invite.delete`、`settings.update`、`account.delete`、`user.admin_bootstrap`、`user.set_role`（命令列）。`detail` 不可包含密碼、權杖或邀請碼明碼。

## 8. 內嵌前台

- 前台是 `web/` 下的 Vue 3 + Vite + TypeScript 單頁應用，`npm run build` 輸出到 `web/dist`，以 `go:embed` 編進伺服器執行檔。`web/dist/.gitkeep` 進版控，讓沒有建置前台時 Go 仍可編譯。
- 伺服器對非 `/api/`、非 `/.well-known/`、非 `/healthz` 的 `GET`／`HEAD`：檔案存在就回檔案，否則回 `index.html`（由前端路由處理）；`/assets/` 下不存在的檔案例外，回 `404 not_found`，避免過期的腳本網址拿到 HTML。`/api/` 下不存在的路徑仍回 JSON 的 `404 not_found`。
- 快取：`/assets/` 下檔名含雜湊的檔案 `Cache-Control: public, max-age=31536000, immutable`；`index.html` 與其他檔案 `no-cache`。
- 沒有建置前台（`dist` 內沒有 `index.html`）時，這些路徑回 `404 not_found`，訊息說明此版本未包含網頁前台。
- 前台回應附加：
  - `Content-Security-Policy: default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: no-referrer`
- 前端路由（評論相關）：`/works/<作品雜湊>` 作品頁與評論；`/reviews` 我的評論（`features.reviews` 為 `true` 時才出現在導覽列）；`/mod` 評論管理（角色 `moderator`、`admin` 才有導覽項目，其他人看到找不到頁面）。作品頁是唯一不強制登入的路由：未登入時，只有 `features.visitorBrowsing` 為 `true` 才能進入（唯讀，並提示登入），否則導向登入頁。書庫條目頁在 `features.reviews` 為 `true` 時顯示「評論」連結。
- 封面圖片直接由瀏覽器向來源載入（`referrerpolicy="no-referrer"`），伺服器不代抓；載入失敗時顯示預留圖。
