# 規格：同步語意（v1）

伺服器把每位使用者的資料存成一組**記錄**，每筆記錄由 `(kind, key)` 識別，內容是一組欄位。合併採**狀態式、欄位級 LWW**：每個欄位各自帶一個 HLC 時間戳，較新的勝出。

測試向量：[`api/vectors/lww.json`](../../api/vectors/lww.json)。伺服器與 App 的合併實作都必須通過。

## 1. HLC 時間戳

字串格式：

```
<millis>:<counter>:<deviceId>
```

| 部分 | 規則 |
|---|---|
| `millis` | 十進位，無前導零，`0` 到 `2^53 - 1` |
| `counter` | 十進位，無前導零，`0` 到 `2^31 - 1` |
| `deviceId` | `[A-Za-z0-9_-]{1,64}` |

格式必須完全符合（不可有前導零、空白或正負號），所以**兩個時間戳相等若且唯若字串相等**。

比較順序：`millis`（數值）→ `counter`（數值）→ `deviceId`（位元組順序）。由於 `deviceId` 限定 ASCII，Kotlin 的 `String.compareTo` 與 Go 的字串比較結果相同。這與 App 現有的 `HlcTimestamp` 排序一致。

`deviceId` 由伺服器在裝置登入時配發（見 OpenAPI 的 `Device.syncDeviceId`），同一帳號內不重複。

## 2. 記錄狀態

```json
{
  "fields":  { "<欄位>": <任意 JSON 值> },
  "clocks":  { "<欄位>": "<HLC>" },
  "deleted": { "value": true, "clock": "<HLC>" },
  "cleared": "<HLC>"
}
```

- `fields` 與 `clocks` 的鍵必須完全相同。
- `deleted` 是一個 LWW 暫存器，表示記錄目前是否被刪除；從未刪除過時省略。
- `cleared` 是**所有曾經出現過的刪除（`deleted.value = true`）中最大的時鐘**，從未刪除過時省略。它不會因為之後的「取消刪除」而變小。
- 欄位名稱：`^[a-z][A-Za-z0-9]{0,63}$`；一筆記錄最多 64 個欄位；`fields` 編碼後最多 16 KiB。

## 3. 推送內容

用戶端推送的一筆記錄只包含它要寫入的部分：

```json
{
  "fields":  { "read": true },
  "clocks":  { "read": "1731200000000:0:dev-a" },
  "deleted": { "value": false, "clock": "1731200000000:0:dev-a" }
}
```

`fields`、`deleted` 都可以省略，但至少要有一項。

## 4. 合併規則

給定目前狀態 `S`（記錄不存在時為空）與推送 `P`：

1. **驗證**（任一失敗則整筆拒絕，`S` 不變）：
   - `P.clocks` 必須剛好涵蓋 `P.fields` 的每個欄位 → 否則 `missing_clock` / `invalid_record`。
   - 每個時鐘必須符合第 1 節格式 → 否則 `invalid_clock`。
   - 欄位名稱與大小限制 → 否則 `invalid_record`。
   - **時鐘防護**：任何時鐘的 `millis` 大於 `伺服器現在時間 + 300000`（5 分鐘）→ `clock_skew`。
2. **刪除暫存器**：若 `P.deleted` 存在，且 `S.deleted` 不存在或 `P.deleted.clock > S.deleted.clock`，則取 `P.deleted`。
3. **清除時鐘**：若 `P.deleted.value = true`，則 `S.cleared = max(S.cleared, P.deleted.clock)`。（不論第 2 步是否採用了它。）
4. **欄位**：對 `P.fields` 中每個欄位 `f`，若 `S` 沒有 `f` 或 `P.clocks[f] > S.clocks[f]`，則取 `P` 的值與時鐘。**時鐘相等時保留 `S` 的值。**
5. **清除舊欄位**：若 `S.cleared` 存在，移除所有 `clocks[f] <= S.cleared` 的欄位。
6. 若結果與原本的 `S` 不同（任何欄位、時鐘、`deleted`、`cleared` 有變化），結果為 `applied`，伺服器配發新的 `seq`；否則為 `unchanged`，`seq` 不變。

### 性質

- **交換律、結合律、等冪**：只要所有時鐘彼此不同，任何推送順序、重送次數、分批方式都得到相同狀態。測試向量中標記 `"anyOrder": true` 的案例會以所有排列驗證。
- **時鐘相等但值不同**只會發生在用戶端錯誤（同一裝置、同一時間戳寫了兩個值）。此時伺服器保留先到的值；用戶端拉取時，以伺服器的值為準（見第 6 節），所以各裝置仍會收斂到伺服器的狀態。
- **刪除會清掉較舊的欄位**：刪除後再加入（例如移出書庫後又加回），舊的分類、釘選等欄位不會復活；刪除之後才寫入的欄位會保留。

## 5. 序號與拉取

- 每位使用者有一個遞增序號 `seq`。每次 `applied` 的記錄得到新的 `seq`，取代記錄原本的 `seq`。
- 伺服器在同一個交易中以 `update users set sync_seq = sync_seq + 1 ... returning` 取得序號，該列鎖讓同一使用者的推送依序提交，因此**提交順序與序號順序一致**，拉取不會漏掉序號較小但較晚提交的記錄。
- `GET /sync/changes?since=N` 回傳 `seq > N` 的記錄**完整狀態**（含 `seq`），依 `seq` 遞增排序。

## 6. 用戶端套用

用戶端拉到伺服器記錄 `R` 時，對本機尚未推送的欄位使用相同規則合併，但**時鐘相等時以 `R` 為準**。本機已推送且未再修改的欄位，直接以 `R` 覆蓋。

推送成功後，伺服器回傳合併後的完整狀態，用戶端以同樣方式套用。

## 7. 墓碑清理與 410

- `deleted.value = true` 且最後一次變更超過 **90 天** 的記錄，由背景工作永久刪除；同時把 `users.sync_floor_seq` 提高到被刪記錄中最大的 `seq`。
- `since != 0` 且 `since < sync_floor_seq` 的拉取請求回 `410 resync_required`。推送時也帶上 `since`，同樣檢查。`since = 0`（完整拉取）永遠不回 410，否則清理後的新裝置將無法開始同步。
- 用戶端收到 410 時：
  1. 從 `since=0` 重新完整拉取。
  2. 本機有、伺服器沒有、且**上次成功同步後未曾修改**的記錄，視為已在其他裝置刪除，於本機刪除。
  3. 其餘本機修改照常推送。
- 已知限制：離線超過 90 天、且在離線期間修改過某筆已被他人刪除的記錄的裝置，會讓那筆記錄重新出現。這是可接受的取捨。
