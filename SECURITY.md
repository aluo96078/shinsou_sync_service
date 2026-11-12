# 安全政策｜Security Policy

## 回報漏洞｜Reporting a Vulnerability

**請勿在公開的 GitHub Issue、Discussion 或 Discord 頻道揭露漏洞。**
**Do not disclose vulnerabilities in public GitHub issues, discussions, or Discord channels.**

請寄信至 / Email: **aszxc96078@gmail.com**，主旨以 `[Security] shinsou_sync_service` 開頭。

信中請盡量提供 / Please include:

- 伺服器版本或 commit，以及部署方式（Docker／二進位、反向代理設定） / Server version or commit, and deployment method
- 漏洞類型與影響範圍（例如跨帳號存取、權限提升） / Type and impact (e.g. cross-account access, privilege escalation)
- 重現步驟或 PoC，請使用你自己架設的測試伺服器 / Reproduction steps or PoC against your own test instance
- 你是否已在其他地方揭露 / Whether it has been disclosed elsewhere

**請勿對他人架設的伺服器進行測試。** 寄出前請移除登入權杖、密碼、電子郵件與其他私人資料。
**Do not test against servers operated by other people.** Remove login tokens, passwords, email addresses, and other private data before sending.

## 處理流程｜Process

1. 收到後會在 7 天內回覆確認。/ We aim to acknowledge reports within 7 days.
2. 確認問題後會評估嚴重程度並開發修正；修正發佈前請勿公開細節。/ Please keep details private until a fix is released.
3. 修正發佈後，若你同意，會在 Release notes 中致謝。/ With your permission, you will be credited in the release notes.

本專案由個人維護，沒有漏洞獎金計畫。/ This project is maintained by an individual and has no bug bounty program.

## 支援版本｜Supported Versions

只有 `main` 分支與最新發佈的版本會收到安全修正。自架者請依 [升級文件](docs/deploy/upgrading.md) 保持更新。
Only `main` and the latest release receive security fixes. Self-hosters should keep up to date by following the upgrade guide.

## 範圍｜Scope

適用於 / In scope:

- 驗證、裝置登入與權杖處理 / Authentication, device sign-in, and token handling
- 跨帳號讀寫同步資料、分享、評論或收件匣 / Cross-account access to sync data, shares, reviews, or inbox
- 管理後台權限繞過 / Admin console authorization bypasses
- 網頁前台的 XSS、CSRF 等注入問題 / Injection issues in the web front end (XSS, CSRF, etc.)

不在範圍內 / Out of scope:

- 個別伺服器管理員的部署設定錯誤（例如未啟用 HTTPS） / Misconfiguration by individual server operators (e.g. no HTTPS)
- 伺服器管理員能讀取同步資料：這是[既定設計](README.md#設計重點)，不是漏洞 / Operators being able to read synced data is by design
- 自架伺服器上的內容與處置由該伺服器管理員負責，請直接聯絡對方 / Content and moderation on a self-hosted server are that operator's responsibility
- App 端的漏洞請回報至 [shinsoux](https://github.com/aluo96078/shinsoux/blob/master/SECURITY.md)（同一個信箱即可） / Report app issues under shinsoux (same email address)
