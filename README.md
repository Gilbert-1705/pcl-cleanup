# pcl-cleanup – storage cleanup for Prusa Connect Local

*Deutsche Fassung: [README.de.md](README.de.md)*

An extension for [Prusa Connect Local](https://github.com/prusa3d/prusa-connect), the self-hosted Prusa Connect server. Prusa Connect Local never deletes anything on its own: the camera keeps writing snapshots, a timelapse extension writes one folder per print, and other extensions may store pictures too. This extension deletes such files by rules per folder. Before anything is deleted, it shows what would go, how the storage is used, and what was deleted last.

Status: 0.2.0, built and tested against Prusa Connect Local **v0.1.0-beta.1**. The user interface is German (the page is called „Aufräumen“).

Up to 0.1.1 the extension lived in a shared repository with a timelapse extension, under a different module path. Since 0.2.0 it has its own repository. If you run 0.1.x, see [Upgrading from 0.1.x](#upgrading-from-01x).

## What it does

- Adds a page „Aufräumen“ under Extensions in the sidebar, with the rules, a preview per rule, a „Jetzt aufräumen“ (clean up now) button, storage usage and a history.
- Each **rule** names a storage, a folder, a mode and how long to keep things:
  - **Files:** every file on its own, by its modification time, optionally filtered by a pattern such as `*.jpg`.
  - **Folders:** whole folders at a given depth, aged by the newest file inside. A timelapse run always goes as a whole.
  - `keep_newest` keeps the newest N units regardless of their age.
  - A rule on the top level of a storage is refused.
- Limits per scan: 500,000 entries, 3,000 paths per plan, 200 entries in the usage view. A plan that hit a limit says so. In folder mode, folders that were not read completely are left out of such a plan, so they are neither kept nor deleted by it.

**Default rules:**

| Rule | Where | Keep | State |
|---|---|---|---|
| Kamera-Snapshots | `local:camera`, `*.jpg` | 14 days | on |
| Zeitraffer | `local:zeitraffer`, folders at depth 2 | 180 days, always the newest 10 | on |
| PrintWatch-Snapshots | `local:printwatch`, `*.jpg` | 7 days | off |

## How files are deleted

The module protocol of Prusa Connect Local can read and write storages, but not delete. So there are two ways:

- **By hand:** „Jetzt aufräumen“ shows the count and size first, then deletes from the browser with the rights of the signed-in person.
- **Automatically:** the Go process calls the server's own HTTP API (`POST /api/v1/storages/{storage}/deletions`). For that it needs the server address (`core_url`) and an API credential with the scope `operate` (`api_token`). **Without a token nothing is ever deleted automatically.**

Deleting is permanent. Check the preview of every rule before the first run.

## Settings

Under Settings → Extensions → Aufräumen:

| Setting | Default | Meaning |
|---|---|---|
| `core_url` | `http://127.0.0.1:8080` | The server's address as the module sees it. Change it if the server listens on another port. |
| `api_token` | empty | API credential with the scope `operate` (Settings → API credentials). Stored as a secret. |
| `auto_interval` | `0s` | How often the active rules run automatically, e.g. `6h`. `0s` turns it off. |

Permissions the module asks for: `read_storage` and a data folder of its own (`regeln.json`, `verlauf.json`).

### Permissions and the API token

Prusa Connect Local guards the module's routes: reading (`GET`) needs `view_resource` on the extension, everything else needs `operate_resource`.

Be aware what that means once an `api_token` is set: **anyone who may operate the extension can set rules for any storage and start an automatic run, and that run deletes with the rights of the person the token belongs to.** On a server with a single administrator this changes nothing. On a server with several people:

- create the token for an account with as few rights as possible, ideally only on the storages you want cleaned,
- give `operate` on the extension only to people who may delete in those storages anyway,
- or leave `api_token` empty and clean up by hand only.

The page also lists storages with the module's own `read_storage` right, so people with `view_resource` on the extension can see folder names and sizes of every storage.

## Installation

```sh
make core
make release
prusa-connect module add github.com/gilbert-1705/pcl-cleanup@0.2.0 -from release/cleanup -approve
```

Prusa Connect Local recognises an update only by its version, so every change raises it in `module.yaml`, `main.go` (`release`), `openapi.json` and `ui/package.json`. Restart the server once after an update, because it loads the browser part only at start.

### Upgrading from 0.1.x

Since 0.2.0 the extension has a new module path and the module name `cleanup` instead of `aufraeumen`. For Prusa Connect Local it is therefore a new module, not an update. The old installation keeps running until you remove it.

1. Install 0.2.0 as above.
2. To keep your rules, copy `module-data/aufraeumen/regeln.json` to `module-data/cleanup/regeln.json` and restart the server.
3. Enter `core_url`, `api_token` and `auto_interval` for the new extension.
4. Remove the old installation: `prusa-connect module remove <old module path>`. Its data folder `module-data/aufraeumen` stays and can be deleted afterwards.

Do not let both run automatically before step 4. If you use `core.modules_local`, change the key there to the new module path.

## Building

```sh
make core      # fetch Prusa Connect Local v0.1.0-beta.1 into .core and build its libraries
make test      # Go tests and svelte-check
make release   # release/cleanup/ and release/cleanup-<version>.tar.gz
```

Needs Go 1.26, Node 22, pnpm 11 and git. `ui/pnpm-workspace.yaml` accepts new package versions only after seven days and transitive dependencies only from the registry.

## Layout

| File | Contents |
|---|---|
| `main.go` | start, settings, the module's routes |
| `rules.go` | rules, defaults, validation |
| `plan.go` | what a rule would delete now, storage usage |
| `cleaner.go` | automatic run, deleting through the HTTP API |
| `ui/` | the page „Aufräumen“ (Svelte 5, `@prusa/connect-ui`) |

Routes under `/api/v1/modules/com.github.aux.gilbert-1705.pcl-cleanup/`:

| Route | Purpose |
|---|---|
| `GET /overview` | rules, settings, history |
| `PUT /rules` | save the rules |
| `GET /plan?rule=…` | what a rule would delete now |
| `GET /usage?storage=…` | usage per top-level folder |
| `POST /runs` | record a run made by hand |
| `POST /auto/run` | start an automatic run now |

`/state`, `/openapi.json` and `/ui/` are taken by the server, hence `/overview`.

## Security

Please do not report security issues in public issues. See [SECURITY.md](SECURITY.md).

Every push, every pull request and once a week, Gitleaks (secrets, including the Git history), Trivy (known vulnerabilities, misconfiguration, secrets) and Semgrep (static analysis) run in CI. Locally the same runs with `./scripts/security-review.sh`, and `pre-commit install` adds a Gitleaks check before every commit.

## License

MIT, see [LICENSE](LICENSE).

Built on Prusa Connect Local by Prusa Research a.s. Prusa Connect Local itself is licensed under OCL v1.1 with add-ons, see its repository. This repository contains none of its code; `make core` fetches it into `.core`, which is not committed. A built release does contain parts of it (the Go SDK in the binary, `@prusa/connect-ui` in the page bundle).
