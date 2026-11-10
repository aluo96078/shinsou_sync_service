# 規格：作品評論與管理（P5）

同一個伺服器內的帳號可以對作品寫評論、評分、按「有幫助」、回覆與檢舉；管理員與版主可以隱藏、刪除內容並停權帳號。API 契約在 [`api/openapi.yaml`](../../api/openapi.yaml)（標籤 `reviews`、`moderation`），資料表在 `migrations/00004_reviews.sql`。本文件說明契約沒寫出來的規則。

## 1. 功能開關與訪客瀏覽

| 設定 | 環境變數 | 預設 | 作用 |
|---|---|---|---|
| `reviews` | `SHINSOU_REVIEWS` | `enabled` | 評論功能總開關 |
| `visitorBrowsing` | `SHINSOU_VISITOR_BROWSING` | `disabled` | 未登入者能否唯讀瀏覽作品頁與評論 |

- 兩者都可由管理員在後台即時切換（`PATCH /api/v1/admin/settings`）。
- `GET /.well-known/shinsou` 的 `features.reviews` 反映總開關；`features.visitorBrowsing` 只有在兩個設定都開啟時才是 `true`。
- 評論關閉時，本文件第 2～7 節的端點全部回 `403 feature_disabled`；已存在的資料保留。第 8 節的管理端點不受開關影響，關閉後仍可清理內容。

三個讀取端點接受「不帶權杖」的請求：

- `GET /api/v1/works/{hash}`
- `GET /api/v1/works/{hash}/reviews`
- `GET /api/v1/reviews/{reviewId}/replies`

判斷順序：

1. 帶了 `Authorization`：照一般登入規則驗證（權杖無效回 `401`，不會降級成訪客），並受信箱驗證限制（見 [web.md](web.md) 第 3 節）。
2. 沒帶：評論關閉回 `403 feature_disabled`；訪客瀏覽關閉回 `401 unauthorized`；否則以訪客身分讀取。

訪客看到的內容與「沒有封鎖任何人的登入者」相同，`mine`、`votedHelpful`、`inLibrary` 一律為 `false`，`myReview` 為 `null`。訪客不能寫入任何東西。

其餘端點都需要登入並受信箱驗證限制。

## 2. 作品頁

作品以作品鍵雜湊（64 個十六進位字元，見 [work-key.md](work-key.md)）定位。只有伺服器已經知道的作品（曾有人把它同步上來）才有作品頁；雜湊格式不對或不存在都回 `404`。

`GET /api/v1/works/{hash}` 回傳：

- `work`：作品的共用資料；
- `rating.average`：可見評論中有評分者的平均，四捨五入到小數兩位；沒有任何評分時為 `null`。`rating.count` 是有評分的評論數；
- `reviewCount`：可見評論數（含沒有評分、只有文字的）；
- `myReview`：呼叫者自己的評論（即使已被隱藏），沒有則 `null`；
- `inLibrary`：作品是否在呼叫者的書庫中。

統計只計「可見」的評論：狀態為 `visible` 且作者未被停權。統計不因呼叫者的封鎖名單而不同。

## 3. 評論

每個帳號對每部作品最多一則評論。

`PUT /api/v1/works/{hash}/review`，內容 `{rating?, body?, spoiler?}`：

- `rating`：1～5 的整數，可省略或 `null`（只寫文字）；
- `body`：最多 5000 個字元（以 Unicode 碼位計），前後空白會去掉；可為空（只評分）；
- `rating` 與 `body` 至少要有一個，否則 `422`；
- `spoiler`：作者自行標記是否含劇透，預設 `false`。用戶端應把標記的內文預設摺疊。

第一次寫回 `201`，之後同一端點是編輯，回 `200`。

- **第一次**寫評論時，作品必須在呼叫者的書庫中，否則 `422 unknown_work`。之後即使把作品移出書庫，仍可編輯或刪除自己的評論。
- 內容確實有變動時才更新 `updatedAt`；`edited` 為 `updatedAt` 晚於 `createdAt`。存入完全相同的內容不算編輯。
- 編輯被隱藏的評論不會讓它恢復可見。

`DELETE /api/v1/works/{hash}/review` 刪除自己的評論，連同它的回覆、「有幫助」與檢舉一起刪除。

`GET /api/v1/me/reviews` 列出自己的所有評論（附 `work`），依 `updatedAt` 新到舊。

### 列表與排序

`GET /api/v1/works/{hash}/reviews?sort=`：

| `sort` | 順序 |
|---|---|
| `newest`（預設） | 建立時間新到舊 |
| `helpful` | 「有幫助」數多到少，同數時建立時間新到舊 |

其他值回 `400`。分頁參數 `offset`（預設 0）、`limit`（預設 50，1～100）。

### 誰看得到什麼

呼叫者看得到一則評論（或回覆）的條件是下列之一：

- 它是自己寫的（包含已被隱藏的，此時會附 `hiddenReason`）；
- 它的狀態是 `visible`、作者未被停權，而且呼叫者沒有封鎖作者。

`replyCount` 依同樣規則計算，所以不同人看到的數字可能不同。看不到的評論，對它的任何操作都回 `404`。

`hiddenReason` 只會給作者本人與管理端點；其他人拿到的評論物件沒有這個欄位。

## 4. 內文格式

評論與回覆的內文是一個很小的 Markdown 子集。伺服器保存原文（`body`），並回傳已轉好的 HTML（`bodyHtml`）。

區塊（以空行分隔；種類改變時不需要空行）：

| 寫法 | 結果 |
|---|---|
| 連續的一般文字行 | 一個段落，行與行之間是換行 |
| 以 `>` 開頭的連續行 | 引用 |
| 以 `- ` 或 `* ` 開頭的連續行 | 項目清單 |

行內：`` `程式碼` ``、`**粗體**`、`*斜體*`。

- 粗體裡可以有斜體；程式碼裡的內容一律照原樣顯示。
- 強調記號必須緊貼文字（開頭記號之後、結尾記號之前不能是空白），而且不能是空的；找不到配對的記號就是普通文字。
- 每行前後的空白會去掉；`\r\n` 與 `\r` 視為換行。
- **沒有**連結、圖片、標題、巢狀清單、原始 HTML。網址只會以純文字顯示。

安全性：輸入的每個字元都經過 HTML 跳脫，輸出中只會出現伺服器自己寫的標籤（`p`、`br`、`blockquote`、`ul`、`li`、`code`、`strong`、`em`），而且都不帶屬性。網頁前端可以直接插入 `bodyHtml`。

共用測試向量在 [`api/vectors/markdown.json`](../../api/vectors/markdown.json)。App 以原生元件從 `body` 自行排版，必須對這些向量產生相同的結構。

## 5. 「有幫助」

`PUT` / `DELETE /api/v1/reviews/{reviewId}/helpful`，兩者都是冪等的，回傳目前的 `{helpful, votedHelpful}`。

- 不能對自己的評論按（`422`）。
- 「有幫助」數計入所有帳號的票，不因封鎖名單而不同。

## 6. 回覆

回覆只有一層：只能回覆評論，不能回覆回覆。

- `POST /api/v1/reviews/{reviewId}/replies`，`body` 1～2000 個字元，格式同第 4 節。
- 評論作者封鎖了呼叫者時回 `403 forbidden`。
- `GET /api/v1/reviews/{reviewId}/replies` 依建立時間舊到新。
- `DELETE /api/v1/replies/{replyId}` 只有回覆的作者可以用；其他人回 `404`。
- 回覆不能編輯。

## 7. 檢舉與發文上限

`POST /api/v1/reviews/{reviewId}/report` 與 `POST /api/v1/replies/{replyId}/report`，內容 `{reason, note?}`：

- `reason`：`spam`、`harassment`、`spoiler`、`illegal`、`other`；
- `note`：最多 500 個字元；
- 不能檢舉自己的內容（`422`）；
- 同一個人對同一個目標已有未處理的檢舉時，回 `200` 與原本那一筆（不會更新原因），不另外建立，也不計入上限。新建立時回 `201`。

上限（以最近 24 小時計，超過回 `429 rate_limited`，附 `retryAfterSeconds`）：

| 環境變數 | 預設 | 計什麼 |
|---|---|---|
| `SHINSOU_REVIEWS_PER_DAY` | 30 | 新的評論與回覆合計 |
| `SHINSOU_NEW_ACCOUNT_POSTS_PER_DAY` | 5 | 同上，但只套用在新帳號 |
| `SHINSOU_NEW_ACCOUNT_AGE` | `72h` | 註冊後多久以內算新帳號 |
| `SHINSOU_REPORTS_PER_DAY` | 20 | 新的檢舉 |

- 0 表示不限制。
- 編輯既有評論、按「有幫助」不計入。
- 寫入類端點另外受社群速率限制（[sharing.md](sharing.md) 第 2 節）。

## 8. 管理

`/api/v1/mod/*` 需要角色 `moderator` 或 `admin`，否則 `403 forbidden`。評論功能關閉時仍可使用。

### 檢舉佇列

`GET /api/v1/mod/reports?status=open|resolved`（預設 `open`）。`open` 依建立時間舊到新，`resolved` 依處理時間新到舊。每筆附檢舉人、目標種類（`review` / `reply`）、所屬評論（含作品）與被檢舉的回覆。回傳的 `open` 欄位是目前未處理的總數。

`POST /api/v1/mod/reports/{reportId}/resolve`，`action`：

| `action` | 對目標做什麼 | `resolution` |
|---|---|---|
| `dismiss` | 不動 | `dismissed` |
| `hide` | 隱藏，原因記為 `reported: <檢舉原因>` | `hidden` |
| `delete` | 刪除 | `deleted` |

- 處理一筆檢舉會同時關閉**同一個目標**上所有未處理的檢舉，結果相同。
- 檢舉已處理過回 `409 already_resolved`。
- 目標被刪除後，它的檢舉紀錄隨之刪除（之後再查那筆檢舉是 `404`）；處置紀錄保留在稽核紀錄中。

### 直接處置

| 端點 | 說明 |
|---|---|
| `GET /mod/reviews?status=hidden\|visible`、`GET /mod/replies?status=` | 列表，預設 `hidden` |
| `POST /mod/reviews/{id}/hide`、`POST /mod/replies/{id}/hide` | 需要 `reason`（1～500 字元） |
| `POST /mod/reviews/{id}/unhide`、`POST /mod/replies/{id}/unhide` | 恢復可見 |
| `DELETE /mod/reviews/{id}`、`DELETE /mod/replies/{id}` | 刪除，`reason` 可省略 |

隱藏或刪除也會關閉目標上未處理的檢舉。被隱藏的內容只有作者本人還看得到，並會看到原因；它不再計入評分與評論數。

### 停權

`POST /api/v1/mod/users/{userId}/suspend`（需要 `reason`）與 `/unsuspend`：

| 操作者 | 可以處置的對象 |
|---|---|
| 版主 | 一般使用者 |
| 管理員 | 一般使用者與版主 |

管理員帳號不能從這裡停權（`403`），要由管理後台處理。停權會登出該帳號的所有裝置；它寫的評論與回覆在停權期間對其他人不可見，也不計入統計，恢復後照常出現。

### 稽核紀錄

每個處置都寫入稽核紀錄（`GET /api/v1/admin/audit`）：

`review.hide`、`review.unhide`、`review.delete`、`reply.hide`、`reply.unhide`、`reply.delete`、`report.dismiss`、`user.suspend`、`user.unsuspend`。

細節欄位包含內容作者、原因、一併關閉的檢舉數；刪除時另外保留內文開頭的摘錄。

## 9. 匯出與刪除帳號

- `GET /api/v1/me/export` 多一個 `reviews` 區塊：自己寫的評論（附作品）、回覆、按過「有幫助」的評論 id、自己提出的檢舉。
- 刪除帳號會一併刪除該帳號的評論（連同其下所有人的回覆與票）、回覆、票與檢舉。

## 10. 之後才做

章節留言、評論通知（有人回覆或按有幫助時通知作者）不在這個階段。
