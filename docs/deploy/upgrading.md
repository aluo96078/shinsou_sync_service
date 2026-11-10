# 升級指南

以下指令都在 `deploy/` 目錄執行。

## 升級步驟

1. **先備份**（見 [backup-restore.md](backup-restore.md)）：

```bash
./backup.sh
```

2. 取得新版程式：

```bash
git pull
```

3. 重新建置並啟動：

```bash
docker compose up -d --build
```

4. 確認：

```bash
docker compose ps
```

```bash
docker compose logs --tail 50 server
```

升級期間伺服器會中斷幾秒到幾十秒。App 在這段時間的變更會留在裝置上，伺服器回來後自動補送，不需要通知使用者。

## 資料庫結構

伺服器每次啟動都會自動套用還沒執行過的資料庫遷移（`migrations/`），不需要手動操作。遷移在交易內執行：失敗時資料庫維持原狀，伺服器不會啟動，錯誤會出現在 `docker compose logs server`。

想把「套用遷移」與「啟動服務」分開（例如先確認遷移成功再切換）：

```bash
docker compose run --rm server migrate
```

## 退回舊版本

**不支援直接降版。** 新版的遷移可能改變資料表，舊版程式不認得。要退回時：

1. `git checkout <舊版本>`；
2. 照 [backup-restore.md](backup-restore.md) 還原**升級前**的備份；
3. `docker compose up -d --build`。

所以升級前的那一份備份一定要留著，直到確認新版運作正常。

## 看目前是哪個版本

```bash
docker compose exec server shinsou-server version
```

或打開 `https://你的網域/.well-known/shinsou` 看 `version` 欄位。版本字串在建置時決定；想讓它顯示 git 的版本，建置時帶上：

```bash
SHINSOU_VERSION=$(git describe --tags --always) docker compose up -d --build
```

## App 與伺服器的相容性

`/.well-known/shinsou` 的 `apiVersions` 標示伺服器支援的協定範圍。App 連線時會檢查；協定不相容時，App 會明確提示要更新哪一邊，不會默默同步出錯。一般來說先升級伺服器、再升級 App 最安全：伺服器會維持對舊版 App 的相容。

## 升級 PostgreSQL 大版本

`docker-compose.yml` 固定使用 `postgres:16`，小版本更新（16.x）只要 `docker compose pull db && docker compose up -d`。大版本（例如 16 → 17）的資料目錄不相容，要用傾印再還原的方式：

1. `./backup.sh`；
2. `docker compose down`；
3. 刪除資料 volume：`docker volume rm deploy_db-data`（**確認上一步的備份檔存在而且大小合理之後才做**）；
4. 把 `docker-compose.yml` 的映像改成新版本，`docker compose up -d db`；
5. 照 [backup-restore.md](backup-restore.md) 還原，再 `docker compose up -d`。
