# Renderbin

**English** | [中文](#renderbin-中文)

A self-hosted service that turns **HTML, Markdown, and text files into shareable links** — built for the files your AI coding agent keeps producing.

Your agent finishes an analysis and hands you a self-contained `report.html` with an interactive chart inside. Pasting it into chat strips the styling and scripts; sending the file buries it in someone's `~/Downloads`; standing up S3 + CloudFront for one throwaway chart is absurd. So instead: **upload it, get a URL, send the URL** — it renders in the browser exactly as the agent authored it. With the built-in **MCP server**, the agent does all three itself and hands you the link in the same turn.

The name says the whole idea: a **bin** you throw things into and get a link back, like a pastebin — except the link **renders** the finished artifact instead of showing you its source.

Ships as a **single self-contained binary/container** — a Go API with the built SvelteKit frontend embedded — with all state in one SQLite file.

![Screenshot of the Renderbin dashboard](doc/screenshot.png)

## Features

- **Upload** `.html`, `.md`/`.markdown`, `.txt` (picker or drag-and-drop, 5 MB each) or **write one in the browser** — HTML served verbatim, Markdown rendered as GFM, text escaped and preformatted, all at the same link
- **Public / private** per file — public links are gated by a random access code (`/res/{slug}?code=...`)
- **Link expiry** after a time window (any amount of hours/days/weeks/months/years, up to 10 years) or a number of anonymous views, with a badge saying why a link went offline; **custom slugs**; edit the title or the source in place without breaking existing links (changing the slug or the access code invalidates them, by design)
- **Tags**, search (by name, optionally file contents), filter/sort, day-grouped view, **trash & restore** (or empty it in one click)
- **Per-file analytics** (session views / access-code views / blocked), file sizes and a storage-usage readout, and **one-click SQLite backup / restore**
- **Multi-user** auth (bcrypt, DB-backed sessions surviving restarts) — the first account is the super admin, registration is toggleable
- **Account management** for the super admin: create an account (the password is generated and shown once), suspend/restore, reset a password (the recovery path, since there's no self-service one), set a per-account **storage quota** (100 MB by default), and delete an account together with its files — all of it, plus backup/restore, under **Settings**
- **Bilingual UI** (English / 中文) and an **MCP server** for AI clients

## Quick start

No clone, no toolchain — grab the compose file and pull the prebuilt image:

```bash
curl -O https://raw.githubusercontent.com/shawn-bluce/renderbin/master/docker-compose.yml
docker compose up -d
```

Open http://127.0.0.1:8080 and create the first account on the welcome page — it becomes the **super admin**, and you choose there whether registration and MCP are on. There are no credential env vars; accounts live in the database.

Images are published to `ghcr.io/shawn-bluce/renderbin` for **linux/amd64 and linux/arm64**. Upgrade with `docker compose pull && docker compose up -d`. All state lives in the `db-data` volume (`/data/app.db` plus its WAL sidecars), which survives container recreation; migrations are forward-only and apply themselves on start. `docker compose down` is safe, `down -v` deletes the database.

Prefer no Docker? Every release attaches static linux binaries — no runtime dependencies at all:

```bash
curl -LO https://github.com/shawn-bluce/renderbin/releases/latest/download/renderbin_linux_amd64.tar.gz
tar xzf renderbin_linux_amd64.tar.gz && ./renderbin
```

`GET /api/health` reports the running version.

| Variable      | Default       | Description                      |
| ------------- | ------------- | -------------------------------- |
| `LISTEN_ADDR` | `:8080`       | Address the server binds to      |
| `DB_PATH`     | `data/app.db` | Path to the SQLite database file |

There are only these two. Everything else — registration, MCP, per-account storage quotas — is configured in the running app and stored in the database.

Locked out of the super admin account? It is the one thing the app cannot fix from inside, so the binary can:

```bash
docker compose exec app ./server reset-password --user=admin   # reads the new password from stdin (it is echoed)
```

The container binds to `127.0.0.1:8080` only. Reaching it directly — `http://localhost:8080` or `http://<lan-ip>:8080` — works as-is; put Nginx or Caddy in front when you want TLS. Either way, forward `X-Forwarded-Proto` (and `X-Forwarded-Host` if the proxy rewrites the origin): the session cookie's `Secure` flag and the URLs MCP hands out both follow it.

## MCP

Enable MCP in **Settings → AI capability** to get a per-user API key, then point your client at `/mcp` (stateless streamable HTTP) with that key as a Bearer token:

```bash
claude mcp add --transport http renderbin https://your-host/mcp \
  --header "Authorization: Bearer rb_..."
```

Tools, all scoped to the key owner's own files: `upload_file`, `upload_files` (up to 20), `list_files`, `search_files`, `update_file`, `publish_file` (optionally with a `ttl` or `max_views` limit), `unpublish_file`, `delete_file` (two-step confirm, trash only — permanent deletion isn't exposed over MCP).

## Architecture

One process, one database file, no external services:

```
                    ┌──────────────────────────────────────────────┐
  browser  ────────▶│ /                → embedded SvelteKit SPA    │
  viewer   ────────▶│ /res/{slug}?code → rendered file (public)    │
  AI agent ────────▶│ /mcp             → MCP server (Bearer key)   │
                    │ /api/*           → JSON API (session cookie) │
                    └────────────────────┬─────────────────────────┘
                                         ▼
                                   SQLite (WAL)
```

- **Single binary.** The SvelteKit build is copied into `backend/internal/web/dist` and embedded via `//go:embed`, so one server serves both API and frontend — no Node in production.
- **Files never touch the filesystem.** Source is stored as text in SQLite; a file's `kind` (`html`/`markdown`/`txt`) is fixed at creation and decides only how it's rendered — access control is identical across kinds.
- **`/res/{slug}` gates itself** (no middleware): missing, trashed, or owned by a suspended account → 404 first; expired public files flip private on access (no cron) and record why; then the file's *owner* is served directly, and everyone else — anonymous or signed in as someone else — needs `is_public` plus a constant-time access-code match, else 403.
- **Sessions and migrations live in the DB** — logins survive restarts, and numbered forward-only `.sql` files apply themselves at startup.

```
backend/  cmd/server · internal/{db,auth,handlers,server,backup,web}
web/      src/lib/{api,schemas,components,i18n} · src/routes
```

## Tech stack

Everything here optimises for **one binary, zero dependencies, easy to self-host**:

- **Go** + [chi](https://github.com/go-chi/chi), [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go — `CGO_ENABLED=0` cross-compiles cleanly), [sqlc](https://sqlc.dev/) instead of an ORM, [goldmark](https://github.com/yuin/goldmark) for Markdown, bcrypt for passwords, the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- **[SvelteKit 2](https://svelte.dev/docs/kit) + [Svelte 5](https://svelte.dev/)** as a pure SPA (`adapter-static`, runes only — no state library), [Tailwind v4](https://tailwindcss.com/), [Zod](https://zod.dev/) + [superforms](https://superforms.rocks/), [Vite](https://vite.dev/)/[Vitest](https://vitest.dev/), Lucide icons bundled locally
- Deliberately absent: microservices, message queue, Redis, ORM, and any CGO-based dependency

## Security notes

Uploaded content is served **unsanitized** — that's the point of the tool — but it is sandboxed, and there are a few other things worth knowing before you expose it:

- **`html` and `markdown` reach the viewer with scripts intact** (`txt` is escaped and never executes), inside a `Content-Security-Policy: sandbox`. The sandbox withholds `allow-same-origin`, which puts each document in its own opaque origin: scripts, links, forms and downloads all work, but the page cannot call this app's API as whoever is viewing it, cannot read cookies, and cannot touch storage. Without that, a document uploaded by one account and opened by another — signed in — would run as that viewer. If you want stronger separation still, serve `/res` from a different hostname.
- **Every account is isolated**: files are scoped by owner in SQL, so another user's file is indistinguishable from one that doesn't exist. There is no super-admin exception for file *contents* — id=1 only gains the global settings toggles, the database backup (super-admin only because the snapshot contains every user's data) and account management, which shows how many files an account owns but never what is in them. Note that MCP API keys are stored in plaintext, so anyone who can read the database file can use them.
- **Backup and restore are super-admin only.** A backup is the whole database, so it carries every account's files, password hashes and API keys; a restore overwrites all of them. A restore is atomic — a rejected or failed upload changes nothing — and replaces the sessions table too, so it signs you out.
- **Suspending an account closes every door at once**: sign-in, the sessions it already had, its MCP key, and every `/res/{slug}` link to its files (which start returning 404). The super admin can't be suspended and the role can't be transferred.
- **Uploads are bounded**: 5 MB per file, and each account has a storage quota (100 MB unless the super admin changes it) counted across its files including the trash. Other endpoints cap their request body at 64 KB, except the backup restore (256 MB) and MCP (32 MB).
- **No login rate limiting and no CSRF token** — bcrypt's per-attempt cost and `SameSite=Lax` are the accepted mitigations. Changing your own password does not invalidate existing sessions; a super-admin reset does (and `DELETE FROM sessions` ends all of them). There is no self-service password reset by design — ask the super admin.

Found a vulnerability? There is no private reporting channel yet, so open an issue describing the **impact** and hold back the details that would make it trivially exploitable until there's a fix.

## Development

```bash
make dev-api   # Go server on :8080
make dev-web   # Vite dev server on :5173 (proxies /api, /res and /mcp, so no CORS setup)
```

Also `make build`, `make test`, `make check`, `make sqlc` — see the [`Makefile`](Makefile). Migrations are forward-only: add a new numbered file under `backend/internal/db/migrations/`, never edit an applied one, and run `make sqlc` after changing any query. Keep `make check` and `make test` green in a PR.

### Releasing

Pushing a `vX.Y.Z` tag is the only thing that publishes anything — plain pushes just run CI.

```bash
git tag -a v0.1.0 -m "First release" && git push origin v0.1.0
```

GitHub Actions re-runs the checks, then pushes a multi-arch image to `ghcr.io/shawn-bluce/renderbin` (tagged `0.1.0`, `0.1`, `latest`) and attaches static linux amd64/arm64 binaries to a GitHub Release. Pre-release tags like `v0.2.0-rc.1` never move `latest`. The tag is stamped into the binary and reported by `GET /api/health`.

## License

[MIT](LICENSE).

---

# Renderbin (中文)

[English](#renderbin) | **中文**

一个自托管服务，把 **HTML、Markdown 和文本文件变成可分享的链接**——专为 AI 编程助手不断产出的那些文件而做。

Agent 做完分析，交给你一个自包含的 `report.html`，里面还带着可交互图表。粘进聊天软件，样式和脚本被清洗掉；直接发文件，对方得去 `~/Downloads` 里翻；为一张一次性图表去搭 S3 + CloudFront 又太离谱。于是换个做法：**上传，拿链接，把链接发出去**——浏览器里的呈现和 Agent 写出来的一模一样。有了内置的 **MCP 服务**，这三步 Agent 自己就能完成，在同一轮对话里把链接递给你。

名字就是产品本身：一个像 pastebin 那样、把东西丢进去就能换回一条链接的 **bin**——区别在于链接打开的是**渲染**好的成品，而不是源码。

整个项目以**一个自包含的二进制/容器**发布——内嵌了构建好的 SvelteKit 前端的 Go API——所有状态都在一个 SQLite 文件里。

![Renderbin 管理界面截图](doc/screenshot.png)

## 功能特性

- **上传** `.html`、`.md`/`.markdown`、`.txt`（选择或拖拽，单个 5 MB），也可以**直接在网页里新建**——HTML 原样输出，Markdown 按 GFM 渲染，纯文本转义后预格式化，都在同一个链接上
- **公开 / 私有**逐文件设置——公开链接由随机访问码保护（`/res/{slug}?code=...`）
- **链接过期**：按时间（任意数量的小时/天/周/月/年，最长 10 年）或匿名访问次数，下线后会标明原因；支持**自定义 slug**；就地修改标题或正文不会影响已分享的链接（修改 slug 或访问码会让旧链接失效，这是有意的）
- **标签**、搜索（按名称，可选搜正文）、筛选排序、按天分组视图、**回收站与恢复**（也可一键清空）
- **逐文件访问统计**（会话访问 / 访问码访问 / 被拒绝）、文件体积与存储用量显示，以及**一键 SQLite 备份 / 恢复**
- **多用户**认证（bcrypt，会话存库、重启后仍有效）——首个账号即超级管理员，注册可开关
- **账号管理**（超级管理员）：创建账号（密码由系统生成、只显示一次）、禁用/解禁、重置密码（这就是找回密码的方式，本站没有自助流程）、为每个账号设置**存储配额**（默认 100 MB）、删除账号并连同其文件一起清除——它和备份/恢复都在**设置**页面里
- **双语界面**（English / 中文）与面向 AI 客户端的 **MCP 服务**

## 快速开始

不用 clone，不用装工具链——下载 compose 文件直接拉预构建镜像：

```bash
curl -O https://raw.githubusercontent.com/shawn-bluce/renderbin/master/docker-compose.yml
docker compose up -d
```

打开 http://127.0.0.1:8080，在欢迎页创建第一个账号——它即**超级管理员**，同时在那里选择是否开启注册和 MCP。没有凭据类环境变量，账号存在数据库里。

镜像发布在 `ghcr.io/shawn-bluce/renderbin`，提供 **linux/amd64 和 linux/arm64** 两个架构。升级用 `docker compose pull && docker compose up -d`。所有状态都在 `db-data` 卷里（`/data/app.db` 及其 WAL 附属文件），容器重建不受影响；迁移只向前，启动时自动应用。`docker compose down` 是安全的，`down -v` 会删库。

不想用 Docker？每个 release 都附带静态 Linux 二进制，没有任何运行时依赖：

```bash
curl -LO https://github.com/shawn-bluce/renderbin/releases/latest/download/renderbin_linux_amd64.tar.gz
tar xzf renderbin_linux_amd64.tar.gz && ./renderbin
```

`GET /api/health` 会返回当前运行的版本号。

| 变量          | 默认值        | 说明                  |
| ------------- | ------------- | --------------------- |
| `LISTEN_ADDR` | `:8080`       | 服务监听地址          |
| `DB_PATH`     | `data/app.db` | SQLite 数据库文件路径 |

只有这两个。其余配置——注册开关、MCP、每个账号的存储配额——都在运行中的应用里设置，存在数据库中。

超级管理员密码丢了？这是应用自身唯一无法从内部解决的问题，所以二进制提供了一个子命令：

```bash
docker compose exec app ./server reset-password --user=admin   # 从标准输入读取新密码
```

容器只绑定 `127.0.0.1:8080`。直接访问 `http://localhost:8080` 或 `http://<局域网 IP>:8080` 都能正常使用；需要 TLS 时再用 Nginx 或 Caddy 挡在前面。无论哪种方式，都请转发 `X-Forwarded-Proto`（若代理改写了来源，还需 `X-Forwarded-Host`）：会话 cookie 的 `Secure` 标记和 MCP 返回的 URL 都依据它判断。

## MCP

在**设置 → AI 能力**中启用 MCP 即可获得每用户的 API Key，然后让客户端以该 Key 作为 Bearer Token 连接 `/mcp`（无状态 streamable HTTP）：

```bash
claude mcp add --transport http renderbin https://your-host/mcp \
  --header "Authorization: Bearer rb_..."
```

工具全部只作用于 Key 属主自己的文件：`upload_file`、`upload_files`（最多 20 个）、`list_files`、`search_files`、`update_file`、`publish_file`（可附带 `ttl` 或 `max_views` 限制）、`unpublish_file`、`delete_file`（两段式确认，仅移入回收站——MCP 不提供永久删除）。

## 项目架构

一个进程、一个数据库文件，不依赖任何外部服务：

```
                    ┌──────────────────────────────────────────────┐
  浏览器    ───────▶│ /                → 内嵌的 SvelteKit SPA      │
  访问者    ───────▶│ /res/{slug}?code → 渲染后的文件（公开）      │
  AI Agent ────────▶│ /mcp             → MCP 服务（Bearer Key）    │
                    │ /api/*           → JSON API（会话 Cookie）   │
                    └────────────────────┬─────────────────────────┘
                                         ▼
                                   SQLite（WAL）
```

- **单二进制。** SvelteKit 构建产物被拷进 `backend/internal/web/dist` 并通过 `//go:embed` 嵌入，一个服务同时提供 API 和前端——生产环境没有 Node 进程。
- **文件从不落盘。** 源码以文本存在 SQLite 中；`kind`（`html`/`markdown`/`txt`）创建时确定，只决定如何渲染——三种格式的访问控制完全一致。
- **`/res/{slug}` 自己做鉴权**（不走中间件）：不存在、已删除，或属主账号已被禁用 → 先 404；过期的公开文件在访问时被置为私有并记录下线原因（无定时任务）；随后文件的**属主**直接放行，其他人——匿名访客或登录着的其他用户——都需要 `is_public` 且访问码常量时间比对通过，否则 403。
- **会话与迁移都在数据库里**——登录重启后仍有效，编号的只向前 `.sql` 迁移在启动时自动执行。

```
backend/  cmd/server · internal/{db,auth,handlers,server,backup,web}
web/      src/lib/{api,schemas,components,i18n} · src/routes
```

## 技术栈

所有选型都围绕**一个二进制、零依赖、易于自托管**：

- **Go** + [chi](https://github.com/go-chi/chi)、[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)（纯 Go，`CGO_ENABLED=0` 可干净交叉编译）、用 [sqlc](https://sqlc.dev/) 而非 ORM、[goldmark](https://github.com/yuin/goldmark) 渲染 Markdown、bcrypt 处理密码、官方 [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- **[SvelteKit 2](https://svelte.dev/docs/kit) + [Svelte 5](https://svelte.dev/)** 纯 SPA（`adapter-static`，只用 runes，无状态库）、[Tailwind v4](https://tailwindcss.com/)、[Zod](https://zod.dev/) + [superforms](https://superforms.rocks/)、[Vite](https://vite.dev/)/[Vitest](https://vitest.dev/)、本地打包的 Lucide 图标
- 有意不引入：微服务、消息队列、Redis、ORM，以及任何基于 CGO 的依赖

## 安全须知

上传的内容**不做任何净化**——这正是这个工具的意义所在——但它运行在沙箱里。在对外暴露之前，还有几点需要知道：

- **`html` 和 `markdown` 会带着脚本原样送达访问者**（`txt` 会被转义，永不执行），并且带有 `Content-Security-Policy: sandbox`。该沙箱不授予 `allow-same-origin`，因此每个文档都处在自己的不透明源中：脚本、链接、表单和下载都照常工作，但页面无法以访问者的身份调用本应用的 API，读不到 Cookie，也碰不到 storage。没有这层隔离，一个账号上传的文档被另一个已登录账号打开时，就会以那个访问者的身份运行。若还想要更强的隔离，可以把 `/res` 放到另一个域名下。
- **账号之间完全隔离**：文件在 SQL 层按属主过滤，别人的文件与不存在的文件不可区分。超级管理员在文件**内容**上没有例外——id=1 只是多了全局设置开关、数据库备份（限超管是因为快照包含所有用户的数据）和账号管理；账号管理只显示每个账号有多少文件，不显示文件内容。另外 MCP API Key 是明文存储的，能读到数据库文件的人就能使用它们。
- **备份与恢复仅限超级管理员。** 备份就是整个数据库，包含所有账号的文件、密码哈希和 API Key；恢复会覆盖它们全部。恢复是原子的——被拒绝或失败的上传不会改动任何数据——并且会一并替换 sessions 表，所以恢复后你会被登出。
- **禁用一个账号会同时关掉所有入口**：登录、已签发的会话、MCP Key，以及该账号名下文件的每一个 `/res/{slug}` 链接（开始返回 404）。超级管理员不可被禁用，身份也不可转移。
- **上传是有上限的**：单个文件 5 MB，每个账号还有存储配额（默认 100 MB，超级管理员可调），按该账号名下的文件累计，回收站里的也算在内。其余接口的请求体上限为 64 KB，备份恢复（256 MB）和 MCP（32 MB）除外。
- **没有登录限流，也没有 CSRF Token**——bcrypt 自身的单次开销和 `SameSite=Lax` 是已接受的缓解手段。自己修改密码不会让已有会话失效；超级管理员重置密码会（`DELETE FROM sessions` 则让全部会话失效）。没有自助找回密码流程，这是有意的——请找超级管理员重置。

发现漏洞？本仓库暂时没有私密上报渠道，请开一个 issue 说明**影响面**，并在修复发布之前先不要贴出可以直接利用的细节。

## 本地开发

```bash
make dev-api   # Go 服务，:8080
make dev-web   # Vite 开发服务器，:5173（代理 /api、/res 和 /mcp，无需配置 CORS）
```

另有 `make build`、`make test`、`make check`、`make sqlc`——详见 [`Makefile`](Makefile)。迁移只向前：在 `backend/internal/db/migrations/` 下新增编号文件，绝不修改已应用的迁移；改动任何查询后运行 `make sqlc`。提 PR 前请保证 `make check` 和 `make test` 通过。

### 发布

推送 `vX.Y.Z` 形式的 tag 是唯一会产出发布物的操作——普通推送只跑 CI。

```bash
git tag -a v0.1.0 -m "First release" && git push origin v0.1.0
```

GitHub Actions 会重跑一遍检查，然后把多架构镜像推到 `ghcr.io/shawn-bluce/renderbin`（打上 `0.1.0`、`0.1`、`latest`），并把 linux amd64/arm64 的静态二进制附到 GitHub Release。`v0.2.0-rc.1` 这类预发布 tag 不会移动 `latest`。tag 会被写进二进制，可通过 `GET /api/health` 查看。

## 许可证

[MIT](LICENSE)。
