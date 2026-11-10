# 架設指南

這份文件帶你用 Docker Compose 架好一台 Shinsou 同步伺服器：伺服器本體與 PostgreSQL 16。伺服器只在主機的 `127.0.0.1:8080` 提供 HTTP；**網域、HTTPS 與憑證由你自己的反向代理處理**（nginx、Caddy、Traefik 都可以），這個範本不會申請憑證，也不會占用 80 / 443 埠。

## 你需要準備

- 一台 Linux 主機（1 vCPU、1 GB 記憶體即可起步）；
- 一個網域（例如 `sync.example.com`），以及能替它提供 HTTPS 的反向代理；
- 主機上裝好 [Docker Engine](https://docs.docker.com/engine/install/) 與 Compose 外掛（`docker compose version` 有輸出），以及 `git`。

只是想在自己的電腦上試用？不需要網域與代理，見[本機試用](#本機試用)。

## 1. 取得程式

```bash
git clone https://github.com/aluo96078/shinsou_sync_service.git
```

```bash
cd shinsou_sync_service/deploy
```

## 2. 填寫設定

```bash
cp .env.example .env
```

用編輯器打開 `.env`，**一定要改**的只有兩項：

| 變數 | 填什麼 |
|---|---|
| `SHINSOU_PUBLIC_URL` | 使用者在瀏覽器看到的完整網址，例如 `https://sync.example.com` |
| `POSTGRES_PASSWORD` | 資料庫密碼。用 `openssl rand -hex 24` 產生一個，貼進去 |

其他項目先照預設即可，之後大多能在網頁後台調整。預設的註冊方式是**邀請制**（`SHINSOU_REGISTRATION=invite`）：只有拿到邀請碼的人能註冊。想讓任何人註冊就改成 `open`。

## 3. 啟動

```bash
docker compose up -d --build
```

第一次會建置映像，需要幾分鐘。完成後確認兩個服務都在執行：

```bash
docker compose ps
```

在主機上確認伺服器有回應（應回傳一段 JSON）：

```bash
curl http://127.0.0.1:8080/.well-known/shinsou
```

## 3.1 設定反向代理

讓你的代理把網域轉到 `http://127.0.0.1:8080`，並且：

- 轉送原本的 `Host` 標頭；
- 帶上 `X-Forwarded-For`；
- 支援 WebSocket（`/api/v1/sync/stream` 用來即時通知其他裝置）。

nginx 的範例（憑證設定照你原本的做法）：

```nginx
server {
    listen 443 ssl;
    server_name sync.example.com;
    # ssl_certificate / ssl_certificate_key ...

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 1h;
    }
}
```

Caddy 的範例（Caddy 會自己處理憑證）：

```
sync.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

完成後用瀏覽器打開 `https://你的網域`，應該看到登入頁。

## 4. 建立管理員帳號

第一次啟動、還沒有管理員時，伺服器會在日誌印出**一次性設定碼**：

```bash
docker compose logs server | grep setup_code
```

到 `https://你的網域/register` 註冊，並在「設定碼」欄位貼上它，這個帳號就是管理員。設定碼用過即失效；如果弄丟了（而且還沒有任何管理員），可以重新產生：

```bash
docker compose exec server shinsou-server admin reset-setup-code
```

管理後台在登入後的 `/admin`：可以管理使用者、發邀請碼、調整實例設定、查看稽核紀錄。

## 5. 讓 App 連上來

在 Shinsou X 的「更多 → 同步」輸入伺服器網址 `https://你的網域`，再登入或註冊即可。要給其他人用，在後台「邀請碼」產生一組給對方。

## 6. 接下來

- **備份**：馬上設定每日備份，見 [backup-restore.md](backup-restore.md)。
- **升級**：見 [upgrading.md](upgrading.md)。
- **寄信**：要有「忘記密碼」與信箱驗證，在 `.env` 填好 `SHINSOU_SMTP_*` 後執行 `docker compose up -d`。
- **版主**：`docker compose exec server shinsou-server admin set-role <使用者名稱> moderator`，或在後台的使用者頁調整。

## 設定參考

`.env` 裡常用的項目：

| 變數 | 預設 | 說明 |
|---|---|---|
| `SHINSOU_REGISTRATION` | `invite` | `open`（任何人）、`invite`（需邀請碼）、`closed`（關閉註冊） |
| `SHINSOU_INSTANCE_NAME` | `Shinsou Sync` | 顯示在登入頁與 App 的名稱 |
| `SHINSOU_EMAIL_VERIFICATION` | 自動 | `disabled`、`optional`、`required`；留空時，有設定寄信且開放註冊就是 `required` |
| `SHINSOU_RECORD_QUOTA` | `200000` | 每個帳號可存的記錄數（0 = 不限） |
| `SHINSOU_SHARING` | `enabled` | 帳號之間分享作品 |
| `SHINSOU_REVIEWS` | `enabled` | 作品評論 |
| `SHINSOU_VISITOR_BROWSING` | `disabled` | 未登入者能否瀏覽作品頁與評論 |
| `SHINSOU_SMTP_*` | 空 | 寄信設定；`SHINSOU_SMTP_HOST` 留空就是不寄信 |

名稱、註冊方式、信箱驗證、分享、評論與訪客瀏覽，管理員都能在後台即時覆寫；`.env` 的值是預設值。修改 `.env` 後執行 `docker compose up -d` 套用。

完整的環境變數清單在專案根目錄的 [README](../../README.md#環境變數)。沒有列在 `docker-compose.yml` 裡的變數，要自己加到 `server` 服務的 `environment` 區塊才會生效。

## 本機試用

把 `.env` 的 `SHINSOU_PUBLIC_URL` 設為 `http://localhost:8080`，其他步驟相同，不需要反向代理。啟動後打開 `http://localhost:8080`。

8080 埠被占用時，在 `.env` 設定 `SHINSOU_PORT`（例如 `11080`），`SHINSOU_PUBLIC_URL` 也要跟著改成 `http://localhost:11080`。

## 連接埠與代理的位置

| 變數 | 預設 | 說明 |
|---|---|---|
| `SHINSOU_PORT` | `8080` | 伺服器在主機上的埠（HTTP） |
| `SHINSOU_BIND` | `127.0.0.1` | 綁定的位址。代理在同一台主機時維持預設；代理在另一台機器時才改成 `0.0.0.0`，並用防火牆限制只有代理連得到 |
| `SHINSOU_TRUSTED_PROXIES` | `172.30.42.1/32` | 哪些來源的 `X-Forwarded-For` 可以相信（以逗號分隔的 CIDR）。預設是 Compose 網路的閘道，也就是主機上的代理連進容器時的來源位址 |

`SHINSOU_TRUSTED_PROXIES` 不對的話，所有人會被當成同一個 IP，登入的速率限制會互相影響。

`SHINSOU_PUBLIC_URL` 必須與使用者在瀏覽器看到的網址一致（包含 `https://` 與埠號），否則網頁登入會回 `403 csrf_failed`。

## 從內建 Caddy 的舊範本升級

舊版範本內建 Caddy 並自動申請憑證。更新程式後：

1. 在 `.env` 把 `SHINSOU_DOMAIN=sync.example.com` 換成 `SHINSOU_PUBLIC_URL=https://sync.example.com`，並移除 `SHINSOU_HTTP_PORT`、`SHINSOU_HTTPS_PORT`；
2. 執行 `docker compose up -d --build --remove-orphans`（會移除舊的 Caddy 容器）；
3. 把你的反向代理指到 `http://127.0.0.1:8080`（或你設定的 `SHINSOU_PORT`）。

舊的憑證 volume 不再使用，可以用 `docker volume rm deploy_caddy-data deploy_caddy-config` 刪除。

## 疑難排解

| 現象 | 檢查 |
|---|---|
| 瀏覽器連不上、憑證錯誤 | 先在主機上 `curl http://127.0.0.1:8080/healthz`。有回應就是反向代理或憑證的問題，沒有回應看 `docker compose logs server` |
| 不斷重新導向 | 代理必須以 HTTP 連到 `127.0.0.1:8080`，不要再經過另一層會把 HTTP 轉成 HTTPS 的代理 |
| 網頁登入回 `403 csrf_failed` | 瀏覽器網址與 `SHINSOU_PUBLIC_URL` 不同（例如用 IP 或另一個網域開啟），或代理沒有轉送原本的 `Host` 標頭 |
| `server` 不斷重啟 | `docker compose logs server`。設定錯誤會在啟動時逐項列出 |
| 找不到設定碼 | 已經有管理員時不會再印。忘記管理員密碼可用另一個管理員重設，或設定寄信後用「忘記密碼」 |
| App 顯示無法連線 | 用瀏覽器打開 `https://你的網域/.well-known/shinsou`，應回傳一段 JSON |
| 健康檢查 | `https://你的網域/healthz` |
