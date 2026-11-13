# 規格：同步記錄內容（v1）

伺服器只驗證記錄的**種類、鍵的格式、欄位名稱與大小**（見 [sync-semantics.md](sync-semantics.md)），不驗證欄位的語意。下表是 App 與網頁前台共同遵守的欄位約定。

所有時間都是 Unix 毫秒（整數）。UUID 一律小寫。未列出的欄位，用戶端必須原樣保留、不可刪除，以便未來版本新增欄位時舊版 App 不會破壞資料。

## 1. 記錄鍵

| kind | key 格式 | 說明 |
|---|---|---|
| `library` | `<workHash>` 或 `local:<uuid>` | 書庫條目 |
| `unit` | `<unitHash>` | 章節狀態；推送時附 `work` 與 `unitRemoteId` |
| `progress` | `<workHash>` 或 `local:<uuid>` | 每部作品一筆，目前讀到哪裡 |
| `history` | `<workHash>` 或 `local:<uuid>` | 最近閱讀 |
| `category` | `<uuid>` 或 `default` | 書庫分類 |
| `repository` | 不透明字串，最長 2048 位元組 | 擴充套件儲存庫；App 以正規化後的 HTTPS 網址當鍵 |
| `setting` | `^[a-z][a-zA-Z0-9.]{0,127}$` | 可攜設定 |
| `package` | `^[A-Za-z0-9._~-]{1,128}$` | 已安裝套件；鍵是套件 id |

`workHash`、`unitHash` 見 [work-key.md](work-key.md)。用 `workHash` 當鍵的記錄，推送時必須在同一批的 `works` 中附上作品描述，或該作品已存在於伺服器，否則回 `unknown_work`。

## 2. 欄位

### `library`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `addedAt` | integer | 加入書庫的時間 |
| `categoryIds` | string[] | 分類鍵，空陣列 = 預設分類 |
| `pinned` | boolean | |
| `archived` | boolean | |
| `customTitle` | string \| null | 使用者自訂標題 |
| `updatePolicy` | string | `auto` / `manual` / `wifi` |
| `title`、`author`、`contentKind` | string | **只用於 `local:` 條目**；有來源的作品改用作品描述 |

移出書庫：推送 `deleted = { value: true }`。

### `unit`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `read` | boolean | |
| `bookmarked` | boolean | |
| `lastPageIndex` | integer \| null | 漫畫最後看到的頁碼（0 起算） |

### `progress`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `unitRemoteId` | string | 目前所在章節；`local:` 條目改用 `unitId`（UUID） |
| `locator` | object | 可攜定位器，見第 3 節 |
| `progression` | number | 整本作品的閱讀比例 0–1，供列表顯示 |

### `history`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `lastReadAt` | integer | |
| `unitRemoteId` | string | |

### `category`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `name` | string | 1–64 字元 |
| `order` | integer | |
| `flags` | integer | 排序與篩選旗標，沿用 App 現有位元定義 |

### `repository`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `url` | string | 儲存庫網址（與鍵相同） |
| `name` | string | 顯示名稱，最長 200 字元；可省略 |
| `trustedPublishers` | string[] | 加入儲存庫時，它用來簽章的金鑰指紋；沒有簽章時為空陣列 |

這類記錄是「帳號知道的儲存庫」，用來給其他裝置當建議，而不是要同步的清單：

- **live** 代表帳號建議這個儲存庫；另一台裝置收到後只會把它列為「來自其他裝置」的建議，由使用者決定要加入或忽略。加入時照一般流程進行，包括確認發佈者。App **不會**因為遠端記錄而新增或刪除本機的儲存庫清單，每台裝置實際使用的清單完全由本機決定。
- **墓碑**代表已從帳號移除，其他裝置不再收到這個建議；已經加入的裝置會保留，不受影響。只有使用者在刪除時勾選「也從帳號移除」才會產生墓碑，單純在本機刪除不會動到伺服器上的記錄。
- 重新加入同一個儲存庫會送出 `~deleted = false`，讓帳號上的墓碑復活。
- 對本機已有的儲存庫，`name` 與 `trustedPublishers` 的變更仍會同步過去。`trustedPublishers` 只是資訊，是否信任由每台裝置的使用者自己決定。

App 只接受 HTTP(S) 網址，其餘的記錄會被忽略。

### `setting`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `value` | any | 設定值 |

只有 App 標記為「可攜」的設定才會同步（白名單在 App 端維護）。伺服器不檢查設定名稱。

- 鍵是設定的路徑 `<區塊>.<欄位>`，例如 `reader.novelFontSizeSp`；`value` 就是該欄位的 JSON 值，`null` 也是一個值（例如「跟隨系統」）。
- 裝置第一次登入時只上傳**與預設值不同**的設定，新裝置的預設值不會蓋掉帳號裡已有的設定。
- 設定不會被刪除；用戶端忽略設定的墓碑，也忽略自己不認得的鍵或型別不符的值。

### `package`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `repositoryUrl` | string | 套件來源儲存庫的正規化網址（與 `repository` 記錄的鍵相同寫法） |
| `name` | string | 顯示名稱 |
| `version` | string | 安裝時的版本字串 |
| `versionCode` | integer | 安裝時的版本號，用來比較新舊 |
| `signingKeyFingerprint` | string | 安裝時套件的簽章金鑰指紋；**僅供參考** |

這類記錄是「帳號在某台裝置上安裝過的套件」，讓其他裝置可以預先準備，而不是要同步的授權狀態：

- 裝置安裝套件（並完成授權）時送出 **live** 記錄；更新套件時以新的 `version`、`versionCode` 覆寫。
- **解除安裝只影響該裝置**，不送墓碑，帳號上的記錄保留，其他裝置不受影響。用戶端忽略 `package` 的墓碑。
- 另一台裝置收到記錄後，只會從**本機已信任的儲存庫**（`repositoryUrl` 對應的儲存庫已在本機加入並信任其發佈者）預安裝：下載並驗證簽章後停在**待授權**狀態，使用者第一次開啟時才進行權限審查。儲存庫未加入或未信任時不做任何事。
- 信任與授權都**不會**被同步：`signingKeyFingerprint` 只是資訊，驗證一律以本機信任的發佈者為準；權限授與由每台裝置的使用者各自決定。

## 3. 可攜定位器

App 的 `ReadingLocator` 帶有 `scope`（本機 publication / acquisition / unit UUID 與 `contentRevision`），這些都是**本機身分**，不能直接同步。

同步時：

1. 序列化 `ReadingLocator`，移除 `scope`。
2. 章節改以 `progress.unitRemoteId` 表示。
3. 套用時，App 以作品鍵與 `unitRemoteId` 查本機對照表，重建 `scope`；`contentRevision` 使用本機目前的值。若本機內容版本不同，由定位器內的引文（`TextQuote`）、`progression` 等資訊重新定位。

找不到對應章節時（例如另一台裝置的來源較新），保留記錄但不套用，等章節清單更新後再試。

## 4. 作品描述

推送批次中的 `works` 陣列：

| 欄位 | 型別 | 說明 |
|---|---|---|
| `keyVersion`、`packageId`、`sourceId`、`remoteId` | | 見 work-key.md |
| `hash` | string | 用戶端計算的 `workHash`，伺服器會驗證 |
| `title` | string | 必填，1–512 字元 |
| `author` | string \| null | |
| `coverUrl` | string \| null | 只接受 `https://`，伺服器不會抓取 |
| `contentKind` | string | `manga` / `novel` |
| `language` | string \| null | BCP 47 |
| `sourceName` | string \| null | 來源顯示名稱 |
| `repositoryUrl` | string \| null | 提供該來源的儲存庫，分享時用來引導安裝 |

伺服器保留最新一次提交的描述，並記錄提交者。例外：`language`、`sourceName`、`repositoryUrl` 描述的是來源而不是作品，提交時為 `null`（裝置沒有安裝該套件）不會清掉伺服器已知的值。

`works` 可以不搭配任何紀錄單獨提交，用來補上先前沒帶到的描述。
