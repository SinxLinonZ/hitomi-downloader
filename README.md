# Hitomi Local

**English** · [简体中文](README.zh-CN.md) · [日本語](README.ja.md)

A Go CLI that starts a local HTTP GUI (React, TypeScript and Mantine). The frontend is embedded in the Go binary, so you ship and run a single executable.

Prebuilt Linux / Windows / macOS binaries are available on the [Releases](https://github.com/SinxLinonZ/hitomi-downloader/releases) page.

> Note: the GUI text is currently in Simplified Chinese. Button and menu names in this document are translated to describe them.

## Build and run

Requires Go 1.23+ and Node.js 20.19+ or 22.12+.

```sh
cd web
npm ci
npm run build
cd ..
go build -o hitomi-downloader ./cmd/hitomi-downloader
./hitomi-downloader
```

Your default browser opens automatically; you can also open the link printed in the terminal. The frontend is embedded in the binary, so distributing the binary is enough and Node.js is not needed at runtime.

```sh
./hitomi-downloader --port 8080 --download-dir /absolute/path/to/downloads
./hitomi-downloader --proxy http://127.0.0.1:7890
./hitomi-downloader --lan   # let a phone on the same LAN open the reader page
./hitomi-downloader --proxy direct --no-open --data-dir /absolute/path/to/state
```

By default it listens on a random port of `127.0.0.1`. The default download directory is `downloads` under the working directory; configuration and task records live in `data`. A download path saved in the GUI is restored on the next start, and the CLI `--download-dir` overrides it. The proxy follows the `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment variables by default. The session link printed by the CLI establishes a local HTTP-only cookie; use the new link after every restart.

The GUI uses a dark theme and keeps the Mantine AppShell with a fixed header and sidebar (on mobile the sidebar opens from the button at the top left). The content area is dense: the page title and description share one line, each page's search/filter toolbar is pinned to the top, search results are a cover grid, the download queue is a compact table (filters: All / Active / Resumable / Completed, 100 rows per page), and "Downloaded" is a two-column list with 50 items per page. Press `/` to focus the search box of the current page. The "Stop service" button in the header and Ctrl+C / SIGTERM in the terminal use the same shutdown flow: connected pages are told to stop reconnecting SSE, in-flight HTTP requests are cancelled, downloads are paused with their progress saved, then connections are drained and the process exits. After stopping, the page disables its actions; run the CLI again and open the new link to continue.

Task state is pushed over SSE only when the task list changes, with a keep-alive every 15 seconds while idle.

## Mobile reader page

`/reader` is a phone-oriented reader page. It does four things: browse downloaded works (two-column covers, infinite scroll), filter them (keywords, plus a bottom drawer with multi-tag filtering where all selected tags must match), sort them (dropdown at the top right of the list: Newest download / Oldest download / Recently updated; default is newest download and the choice is remembered on that phone), and read them.

Back navigation: a work and the filter drawer each take one entry in the browser history (URL hash `#/w/ID`, `#/filter`), so the phone's back gesture/key and the in-page "back" button only close them and return to the list. After a reload, or when a work is opened directly from a link, the "back" button replaces the current entry with the list instead of leaving the page.

- **Reading**: vertical scroll by default; only the images around the current page are loaded (so works with many pages stay light on memory), and a bottom slider jumps between pages. You can switch to "paged" mode, where tapping the left/right side of the screen or swiping turns pages and right-to-left reading can be toggled. Tap the middle of the screen to show or hide the toolbar. The reading position and display settings are stored only in that phone's browser, and the list shows "read n/total".
- **Image source**: local downloaded images only; Hitomi is not contacted. Formats the browser cannot show (e.g. JXL in some environments) appear as missing pages. Works in the legacy format (not yet migrated) can also be read.
- **Covers**: the thumbnail cached by the search page is used first, otherwise the first page of the work, so covers can be large the first time the list opens.

### Letting a phone connect

By default only the local machine can connect. Start with `--lan` when needed:

```sh
./hitomi-downloader --lan
```

The terminal prints a link for the phone, and the GUI "Settings" dialog shows the link and a QR code. Scan it or open the link (the phone must be on the same LAN as the computer). Security design:

- The link carries a **reader-only token**, different from the admin start-up token. It is stored in `data/reader-token` (the link stays the same across restarts; delete the file and restart to reset it). It can only reach the reader page and its API; it cannot download, verify, migrate or change settings.
- Requests from other devices never get admin rights: even with the admin token or cookie they can only use the reader. The admin home page is redirected to `/reader`.
- LAN access uses plain HTTP, so the token is not encrypted in transit. Use it only on a trusted home network. The reader API returns only work information and images and does not expose file paths.
- Without `--lan` the behaviour is unchanged: it listens on `127.0.0.1` only.

## Search and download

- Plain keywords are intersected. `artist:name`, `group:name`, `series:name`, `character:name`, `language:japanese`, `type:manga`, `tag:name`, `female:name` and `male:name` search by tag.
- Use underscores for spaces in tag values: `artist:some_artist`. Prefix a term with `-` to exclude it, e.g. `language:japanese -tag:anthology`.
- A gallery ID or a work link opens the detail view. At the bottom of the detail drawer are Hitomi's "related works" (the `related` field of the gallery info, up to 40); you can open one and go back to the previous work. When a page of results partly fails, the successful cards are kept and the page can be retried.
- By default 2 works are processed at once, each with up to 4 pages in parallel. Both are configurable in the GUI "Settings" (concurrent tasks 1–8, files per task 1–16) or with the `--task-concurrency` and `--page-concurrency` flags, and are saved to `data/config.json`. The task limit takes effect immediately for queued tasks; the per-task file limit applies to tasks that are newly started or resumed. HTTP requests are still spaced at least 200 ms apart globally so the upstream is not hit in bursts, hence a very high concurrency does not speed things up proportionally. Migration tasks are pure local I/O and use their own concurrency (4 tasks, 8 pages each), unaffected by these settings.
- Network errors, 408, 429 and 5xx are tried up to 4 times with exponential backoff and jitter, honouring `Retry-After` (a demand longer than 60 seconds returns a retryable error).
- The original format of the source file in the metadata is tried first. On 403/404 for the original format, `gg.js` is force-refreshed and retried; only if it is still unavailable are the available JXL, AVIF and WebP tried. If the host name used for the original format (or JXL) cannot be resolved (no DNS record), that format is likewise treated as unavailable and the fallback happens immediately without retry waits. On the machine used for testing, the `gold-usergeneratedcontent.net` hosts starting with `1.`, `2.` and `j1.` do not exist, while `w1.` (WebP) and `a1.` (AVIF) resolve, so in practice the download currently falls back to AVIF or WebP and the reason is recorded in the metadata. The order of derived formats is a fixed fallback order and does not imply higher quality; images are not re-encoded on the client.
- Network timeouts and 5xx do not trigger a format downgrade. The page format, the fallback reason and the source hash are all stored in the metadata.
- Pausing or cancelling stops requests and keeps completed pages. On resume, page SHA-256 values are checked and only incomplete or damaged pages are downloaded again; there is no byte-level Range resume of a single image. Pause, cancel, exit and crash never delete completed pages; a half-written page exists only as a temporary `.page-*.part` file, is never treated as complete, and is cleaned up the next time the task starts.

## File layout and recovery

```text
downloads/
  12345 - Title/
    0001.png
    0002.jpg
    metadata.json
  .downloading-67890/
    metadata.json
    0001.png
data/
  config.json
  tasks/12345.json
  covers/12345.webp
```

`metadata.json` contains the raw gallery info (all upstream fields kept), the file name of each page, the source hash, the actual format, the byte size, the local SHA-256 and the update time. Task progress is stored in separate JSON files; the "Downloaded" list is built by scanning the metadata in the current download directory, with no database. JSON and images are written to a temporary file and then renamed, and existing completed directories are never deleted.

Change the download directory in the GUI settings, then open "Downloaded". A background scan reveals works as they are found and shows the current directory, the number of directories checked and read warnings; it can be cancelled. Once a work's metadata is found, the images and sub-directories inside it are not enumerated. The in-memory index is reused while the app runs, so switching pages does not rescan; "Rescan" refreshes the index manually, and files are rediscovered after a restart. OS I/O on network mounts cannot be interrupted by Go, so a cancel takes effect after the current I/O returns.

The "Downloaded" page is a list of 50 items per page (two columns on wide screens), with a lazily loaded cover from the first local image, title, artists, language, page count and format. You can filter by title, artist or ID, filter by metadata format, and select across pages. Sorting is by "Recently updated" or by "Download time" (newest→oldest / oldest→newest). Download time is the creation time of the work directory (birthtime on macOS and Windows; elsewhere the directory modification time is used instead), so it is not affected by later verification rewriting `metadata.json`. Covers do not need Hitomi to be online; a placeholder is shown when the browser cannot display JXL. The list shows full-namespace tags (`female`, `male`, plain `tag`); clicking a tag adds it to the filter, and long tag lists can be expanded. The search box is labelled "Search downloaded" and searches titles, artists, IDs and tags in the local index as you type; multiple terms are intersected, and conditions such as `female:glasses`, `female:long_hair` and `-tag:digital` are supported. A separate searchable multi-select of tags shows work counts; tags combine with AND and can be used together with text and the metadata-format filter. Both new downloads and migrated old data keep the original `gallery.tags` field in `metadata.json` intact; search uses a normalized index and never rewrites or trims the original tags.

For the new format, "Verify and complete" works per work, in batches for selected works, and as "Verify all". Verification has two layers:

1. **Local integrity (offline)**: checks the expected page count, file sizes and SHA-256 recorded in the metadata. Pages that were just migrated have no SHA-256 baseline yet, so they first get a signature and full-decode check, and then the baseline is set.
2. **Comparison with Hitomi (online)**: one gallery-info request per work; the source hash of each page is compared (it identifies the original image itself, regardless of whether the local file is webp, avif or the original).

The two layers are combined to tell "local damage" from "upstream update":

| Local page | Upstream hash | Result |
| --- | --- | --- |
| OK | Same | OK |
| Missing or damaged | Same | Local damage; downloaded again automatically |
| Any | Different | Upstream replaced the page: reported only; the local file is not overwritten and the old version is not downloaded |
| — | Upstream has extra pages | Upstream added pages: reported only |
| Present | Upstream lacks it | Upstream removed the page: reported only; local files are not deleted |
| — | Gallery returns 404 | Work deleted upstream: only marked; local content is untouched, and missing pages cannot be completed |
| — | Network or protocol error | Unknown: only the local check is done and the existing upstream state is not changed |

The upstream state is stored in the `upstream` field of `metadata.json`. The "Downloaded" list shows "Upstream updated / Upstream deleted" and can be filtered by them. Changes that only affect metadata such as title or tags are not counted as differences and never rewrite the local metadata.

Only "Apply upstream update" (per work, or in batch after selecting) changes local content: replaced or removed old pages are moved to `superseded/<time>/` (never deleted), the replacement and new pages are downloaded, and the gallery info is updated to the upstream version.

The online comparison is tried at most 2 times; once a connection fails, online comparison is skipped for the following minute so a batch verification does not wait on every work. Without network access, complete works can still pass the local check, but missing pages need network access to be completed. The task detail shows exactly which pages failed; click "Resume / Retry" when the network is back.

After the program exits or crashes, unfinished tasks come back as paused. After changing the download directory, unfinished tasks of the old directory must be resumed after switching back to it; complete works that were moved can be scanned and verified directly in the new directory. Only one task runs per gallery ID at a time.

The download queue is grouped as "Processing → Queued → Paused/Failed/Cancelled → Completed"; processing and queued tasks are ordered by when they were queued, so rows do not jump around as progress changes. A work that is already in the download directory cannot be "downloaded" again: the download button in search results, related works and the detail view is greyed out (hover shows "Downloaded" or "Already in the download queue"), and the server also rejects it with 409 `ALREADY_DOWNLOADED`; use "Verify" if you need to complete it. The decision is based on the scanned local index (while the scan is still running only works already discovered are blocked), and it confirms that the directory still exists.

Every batch operation (Resume all / Pause all / Remove completed tasks, and on the Downloaded page Migrate all, Verify all, Migrate selected, Verify selected and Apply upstream update) first shows a confirmation dialog that states the count and the effect. The default focus is on "Cancel", and cancelling sends nothing to the server. Operations on a single work need no confirmation.

The download queue supports "Pause all", "Resume all", "Hide completed" and "Remove completed tasks". Pause all affects queued and running download, migration and verification tasks; Resume all restarts paused, failed and cancelled unfinished tasks and skips completed or already running ones. Pressing Resume all right after Pause all first waits for the old workers to end, so the same task is never processed twice; failures such as a mismatched download directory are reported per task.

"Hide completed" only affects what the queue displays. "Remove completed tasks" only deletes the `data/tasks/{id}.json` task records and the matching in-memory tasks; it does not touch the download directory, does not delete images or work metadata, and does not remove works from the "Downloaded" list. Unfinished tasks are never cleaned up; a completed record can only be removed after its worker has finished its last save, and removed records do not come back after a restart.

The flat `metadata.json` written by [lanyeeee/hitomi-downloader](https://github.com/lanyeeee/hitomi-downloader) (a Tauri app) is recognised only as "to migrate"; scanning and verification never rewrite it automatically. After clicking "Migrate" on one work, "Migrate selected", "Migrate legacy on this page" or "Migrate all" at the top, the task only checks the file size, format signature and image header of the existing images (it does not read the whole content, so it is fast even on network drives; migration tasks run with higher concurrency than downloads); it does not download again or modify images. The SHA-256 baseline is deferred until that work's first "Verify and complete". Before finishing, it checks that the metadata was not changed by another program, saves the original file as `metadata.legacy.json`, and writes the new metadata atomically; if a different backup already exists, the migration stops with an error. Old fields (for example camelCase titles and string author lists) are converted to the new structure while keeping the original field information. "Migrate all" waits for the scan to finish and covers all legacy metadata in the current download directory regardless of filters and paging; it fixes a snapshot of targets, skips works that already have an active task, then enqueues them in batches of 100, and the progress is still seen in the download queue.

When an old image is missing, damaged, or a page exists in several formats, the migration records the specific page and a warning and keeps the original image; verify and complete it later from "Downloaded". Pausing or cancelling a migration keeps the old metadata, and resuming re-reads the local images; the task state can also be recovered if the program exits after the metadata was committed. Migration does not improve the encoding quality of old images, and the old downloader is no longer guaranteed to read the new metadata. A gallery JSON exported by a Node script is not a complete download directory and is not imported directly.

The local SHA-256 is for detecting changes to a file after download; it is not proof of the remote authenticity of the upstream image. The source hash and the local file SHA-256 are stored separately. JPEG, PNG and GIF are fully decoded when downloaded; WebP, AVIF and JXL are checked for file signature and response length, and later all of them are checked for changes with SHA-256. If the metadata itself is damaged, the page list cannot be inferred reliably, so the metadata must be restored first; directories that cannot be recognised are never overwritten.

## Verification

```sh
go test -race ./...
go vet ./...
cd web
npm ci
npm run build
npm test
```

The Playwright tests start the real Go binary and cover directory scanning, offline verification, partial search failures and settings persistence, so run the build above first (the binary embeds the frontend, so rebuild the binary after changing the frontend). Google Chrome on macOS is used by default; set `CHROME_PATH` to point at a Chromium executable on other systems. The tests use temporary fixtures and mocked search responses and do not depend on Hitomi being online. In restricted environments that forbid the default `/tmp` path, set `TMPDIR=/private/tmp`; if binding a localhost port is forbidden, the browser tests cannot start, but the Go HTTP handler tests still run.

When developing the frontend, first start Go with `--port 8080` and open the CLI link in the browser to initialise the session, then run `npm run dev` and visit the Vite page on the same hostname. Vite proxies `/api` to `http://127.0.0.1:8080` by default; change it with `HITOMI_BACKEND`. Always use `127.0.0.1` in the browser so that the cookie hostname stays the same.

The dynamic host names and the `gg.js` routing rules follow the protocol implementation of [lanyeeee/hitomi-downloader](https://github.com/lanyeeee/hitomi-downloader); when the protocol changes, a clear parse error is reported and the `internal/hitomi` adapter needs updating.

## Acknowledgements and references

This project builds on the protocol knowledge of [lanyeeee/hitomi-downloader](https://github.com/lanyeeee/hitomi-downloader): how Hitomi's gallery info, `nozomi` indexes, `gg.js` routing and image host names work were learned from its implementation. The migration feature exists to take over the download directories and `metadata.json` format written by that project. Many thanks to its author.
