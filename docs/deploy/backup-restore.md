# 備份與還原

伺服器的所有狀態都在 PostgreSQL 裡：帳號、裝置、同步記錄、分享、評論、實例設定與稽核紀錄。**備份資料庫就是備份全部。** 伺服器本身不寫入任何檔案。

另外請把 `deploy/.env` 另存一份（裡面有資料庫密碼與寄信設定）。Caddy 的憑證不需要備份，遺失時會自動重新申請。

以下指令都在 `deploy/` 目錄執行。

## 備份

`deploy/backup.sh` 會把資料庫傾印成一個帶日期的檔案，並刪掉超過 14 天的舊檔：

```bash
./backup.sh
```

檔案預設放在 `deploy/backups/`，可以指定別的目錄，保留天數用 `KEEP_DAYS` 調整：

```bash
KEEP_DAYS=30 ./backup.sh /var/backups/shinsou
```

備份時不需要停機，傾印出來的是同一個時間點的一致狀態。

### 每天自動備份

用 `crontab -e` 加一行（每天 03:30，路徑換成你的）：

```
30 3 * * * /opt/shinsou_sync_service/deploy/backup.sh /var/backups/shinsou >> /var/log/shinsou-backup.log 2>&1
```

**只放在同一台主機上的備份不算備份。** 請再用 rclone、restic 或主機商的快照，把備份目錄同步到別的地方。

### 手動傾印

不用腳本時，等效的指令是：

```bash
docker compose exec -T db pg_dump -U shinsou -d shinsou -Fc > shinsou.dump
```

## 還原

還原會**覆蓋**目前資料庫的內容。步驟：

1. 停掉伺服器（資料庫繼續執行）：

```bash
docker compose stop server caddy
```

2. 把傾印檔灌回去：

```bash
docker compose exec -T db pg_restore -U shinsou -d shinsou --clean --if-exists --no-owner < backups/shinsou-20261110-033000.dump
```

3. 重新啟動：

```bash
docker compose up -d
```

伺服器啟動時會自動把資料庫結構升到目前版本，所以**舊版本的備份可以還原到新版本的伺服器**。反過來不行：新版本的備份不能給舊版本的伺服器用。

### 還原之後裝置要做什麼

每台裝置都保有自己的完整資料，所以閱讀進度本身不會因為伺服器還原而消失。但裝置只會上傳「上次同步之後改過」的內容，而且記得自己拉取到哪裡；伺服器被還原到較早的時間點後，裝置不會自動發現，備份之後同步過的變更不會自己補回伺服器，裝置之間也可能暫時漏掉彼此的新變更。

**還原到較早的時間點之後，請每位使用者在每台裝置上登出同步帳號、再登入一次。** 重新登入時 App 會把本機的完整資料與伺服器重新合併並上傳，之後一切恢復正常。

不會回來的是只存在伺服器上的東西：備份之後才註冊的帳號、分享、評論與後台設定。

如果還原的是剛做好的備份（例如搬家，中間沒有人同步過），不需要重新登入。

### 搬到另一台主機

1. 在舊主機執行 `./backup.sh`，把傾印檔與 `.env` 複製到新主機；
2. 在新主機照[架設指南](self-hosting.md)做到「啟動」；
3. 照上面的步驟還原；
4. 把 DNS 指到新主機。

## 驗證備份

沒有還原過的備份不能算數。偶爾在另一台機器（或本機，`SHINSOU_DOMAIN=localhost`）照「搬到另一台主機」做一次，確認登入得進去、書庫看得到。

快速檢查傾印檔是否完整（列出內容，不寫入任何東西）：

```bash
docker compose exec -T db pg_restore --list < backups/shinsou-20261110-033000.dump | head
```
