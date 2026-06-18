<p align="center">
  <img src="docs/assets/appicon.png" width="128" alt="Omnigate" />
</p>

<h1 align="center">Omnigate · 萬象之門</h1>

<p align="center">
  一站式整合多款二次元手遊的桌面啟動器<br/>
  <strong>原神 · 星穹鐵道 · 絕區零 · 鳴潮 · 明日方舟：終末地</strong>
</p>

<p align="center">
  <img alt="版本" src="https://img.shields.io/badge/版本-v0.3.0-2ea043?style=flat-square" />
  <img alt="平台" src="https://img.shields.io/badge/平台-Windows-0a7bbd?style=flat-square" />
  <img alt="技術" src="https://img.shields.io/badge/Wails-Go%20＋%20Vue%203-00b8a9?style=flat-square" />
  <img alt="授權" src="https://img.shields.io/badge/授權-AGPL--3.0-4c8eda?style=flat-square" />
  <img alt="語言" src="https://img.shields.io/badge/介面-繁中／簡中／EN-9a6adc?style=flat-square" />
</p>

<p align="center"><a href="README.en.md">English README →</a></p>

---

> **一個介面，管理所有啟動器。**
> 不必在五個官方啟動器之間切換——遊戲安裝偵測、更新與預下載、最近遊玩、最新情報，外加每帳號的**抽卡分析**儀表板，全都收進同一扇門。

## ✨ 特色

- 🎮 **多遊戲統一管理** — 自動偵測各遊戲安裝路徑（HoYoPlay / KRLauncher / GRYPHLINK），側欄依發行商分組一覽。
- ⬇️ **更新與預下載** — HoYoverse Sophon 分塊更新、鳴潮 / 終末地增量更新，斷點可續、原子套用。
- 📰 **最新情報** — 各遊戲官方公告 / 活動 / 最新消息直接內嵌首頁。
- 🎰 **抽卡分析** — 總抽數、消耗資源（精確石頭數）、保底進度、歐非幸運評比、出貨分佈、最近高星時間軸；資料本機 SQLite 保存。
- 🖼️ **沉浸式首頁** — 各遊戲 key-art / 動態背景大圖，一鍵開始遊戲。
- 🌐 **多語介面** — 繁體中文 / 簡體中文 / English。

## 📸 介面預覽

#### 首頁總覽 — 遊戲庫、主視覺、最新情報
![總覽](docs/assets/overview.png)

#### 抽卡分析 — 每帳號儀表板
![抽卡分析](docs/assets/gacha.png)

## 🕹️ 支援遊戲

| 遊戲 | 安裝偵測 | 更新 | 情報 | 抽卡分析 |
|---|:---:|:---:|:---:|:---:|
| 原神 | ✅ | ✅ | ✅ | ✅ |
| 崩壞：星穹鐵道 | ✅ | ✅ | ✅ | ✅ |
| 絕區零 | ✅ | ✅ | ✅ | ✅ |
| 鳴潮 | ✅ | ✅ | ✅ | ✅ |
| 明日方舟：終末地 | ✅ | ✅ | ✅ | ⚠️＊ |
| 異環 (Neverness to Everness) | 🚧 規劃中 | — | — | — |

<sub>＊終末地新版客戶端改了抽卡授權機制，token 取得待更新。</sub>

## 🛠️ 技術棧

[Wails v2](https://wails.io)（Go 後端 + Windows WebView2）· Vue 3 + Pinia 前端 · SQLite 抽卡紀錄 · 抽卡統計引擎純 Go 實作。

## 🔨 建置

```bash
wails dev      # 開發（熱重載）
wails build    # 產出 build/bin/omnigate.exe
```

## 📄 授權與致謝

- **授權：** [AGPL-3.0](LICENSE)
- 協定行為參考 [Collapse Launcher](https://github.com/CollapseLauncher/Collapse)（AGPL-3.0）
- HDiff 補丁採用內附 [`hpatchz`](https://github.com/sisong/HDiffPatch)

> 開發相關（Sophon protobuf 重新產生等）詳見 [README.en.md](README.en.md)。
> 本專案原名 `launcher-collection`，後更名為 Omnigate；首個公開版本 **v0.1.0**。
