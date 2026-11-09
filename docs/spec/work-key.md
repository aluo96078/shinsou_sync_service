# 規格：作品鍵與章節鍵（v1）

作品鍵讓不同裝置、不同使用者指向「同一部作品」，是同步、分享與評論共同的基礎。**這份規格一旦有使用者資料就不能再改**，否則同一部作品會在伺服器上分裂成兩筆。任何變更都必須提高 `keyVersion`。

測試向量：[`api/vectors/work-key.json`](../../api/vectors/work-key.json)。伺服器與 App 的實作都必須通過。

## 1. 組成

| 欄位 | App 端來源 | 說明 |
|---|---|---|
| `keyVersion` | `RemoteEntityKey.keyVersion` | 目前只有 `1` |
| `packageId` | `SourceKey.packageId` | 例如 `zh.wenku8` |
| `sourceId` | `SourceKey.sourceId` | 套件內的來源 ID，原樣使用 |
| `remoteId` | `RemoteEntityKey.canonicalId` | 來源回報的作品 ID，原樣使用 |

章節鍵另外加上 `unitRemoteId`（章節的 `RemoteEntityKey.canonicalId`），且只在其作品範圍內有效。

`SourceKey.contractVersion` **不屬於**作品鍵：套件契約版本升級時，同一個來源上的同一部作品仍應是同一個作品。`SourceKey.legacyLongId` 也不屬於作品鍵。

## 2. 不做任何正規化

所有字串**逐位元組原樣**使用：不去頭尾空白、不轉小寫、不做 Unicode 正規化（NFC 等）、不解析或改寫 URL。

理由：App 的 `canonicalRemoteId` 本身就是恆等函式，來源 ID 對大小寫與參數順序可能有意義；兩種語言各自實作正規化，是最容易產生不一致的地方。

## 3. 驗證

不符合下列任一條件的輸入必須被拒絕（伺服器回 `422 invalid_work_key`）：

1. 字串必須是合法 UTF-8（Kotlin 端：不可含孤立代理字元）。
2. 不可為空，且不可全部由 Unicode `White_Space` 字元組成。
3. 不可含 Unicode 一般類別 `Cc` 的控制字元（U+0000–U+001F、U+007F–U+009F）。
4. UTF-8 位元組長度上限：`packageId` 128、`sourceId` 128、`remoteId` 1024、`unitRemoteId` 1024。
5. `keyVersion` 必須是 `1`。

## 4. 正規字串

以長度前綴串接，長度是 **UTF-8 位元組數**（十進位、無前導零）：

```
lp(s)       = len_utf8(s) ":" s
work(v1)    = "w1|" lp(packageId) lp(sourceId) lp(remoteId)
unit(v1)    = "u1|" lp(packageId) lp(sourceId) lp(remoteId) lp(unitRemoteId)
```

例：`packageId=zh.wenku8`、`sourceId=zh.wenku8`、`remoteId=1973`

```
w1|9:zh.wenku89:zh.wenku84:1973
```

## 5. 雜湊

`workHash = lowercase_hex(sha256(utf8(work)))`，`unitHash` 同理。64 個十六進位字元，可直接用在 URL 與資料表索引。

伺服器收到作品描述時，自行計算雜湊並與用戶端提供的值比對；不一致回 `422 work_hash_mismatch`。

## 6. 本機匯入的書

沒有來源的書（TXT、EPUB、圖片包）不使用作品鍵，改用記錄鍵 `local:<uuid>`（小寫 UUID v4）。這類記錄只屬於該使用者，不能分享、不能評論。
