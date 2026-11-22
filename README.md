# pcl-cleanup – storage cleanup for Prusa Connect Local

*Deutsche Fassung: [README.de.md](README.de.md)*

An extension for [Prusa Connect Local](https://github.com/prusa3d/prusa-connect), the self-hosted Prusa Connect server. Prusa Connect Local never deletes anything on its own: the camera keeps writing snapshots, a timelapse extension writes one folder per print, and other extensions may store pictures too. This extension deletes such files by rules per folder. Before anything is deleted, it shows what would go, how the storage is used, and what was deleted last.

Status: 0.4.1, built and tested against Prusa Connect Local **v0.1.0-beta.1**. The user interface is German (the page is called „Aufräumen“).

Up to 0.1.1 the extension lived in a shared repository with a timelapse extension, under a different module path. Since 0.2.0 it has its own repository. If you run 0.1.x, see [Upgrading from 0.1.x](#upgrading-from-01x).

## What it does

- Adds a page „Aufräumen“ under Extensions in the sidebar, with the rules, a preview per rule, a „Jetzt aufräumen“ (clean up now) button, storage usage and a history.
- Each **rule** names a storage, a folder, a mode and how long to keep things:
  - **Files:** every file on its own, by its modification time, optionally filtered by a pattern such as `*.jpg`.
  - **Folders:** whole folders at a given depth, aged by the newest file inside. A timelapse run always goes as a whole.
  - `keep_newest` keeps the newest N units regardless of their age.
  - A rule on the top level of a storage is refused.
- **Printer rules** (since 0.3.0) clean old print files off a printer's own storage, such as its USB drive. Since 0.4.0 they can keep just the newest N files and delete only files that also lie in Connect Local's storage. See [Printer storage](#printer-storage).
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
- **Automatically, on a storage:** the Go process calls the server's own HTTP API (`POST /api/v1/storages/{storage}/deletions`). For that it needs the server address (`core_url`) and an API credential with the scope `operate` (`api_token`). **Without a token no storage rule ever deletes automatically.**
- **Automatically, on a printer** (since 0.4.0, off by default): through the module protocol, as the module itself, only with `printer_auto` on and the operator role the operator granted it on that printer. See [Automatic printer cleanup](#automatic-printer-cleanup).

Deleting is permanent. Check the preview of every rule before the first run.

## Printer storage

A printer rule names a printer, a folder on its storage (for example `usb`) and a pattern for print files (`*.bgcode` or `*.gcode`). It is deliberately narrow:

- **By hand by default.** The page first reads the printer („Drucker lesen“), then shows what would go, and deletes after you confirm. An automatic run is an option the operator has to turn on, see [Automatic printer cleanup](#automatic-printer-cleanup).
- **With your rights.** By hand, the module reads and deletes on the printer for the signed-in person, who needs `operate` on that printer.
- **Print files only.** The pattern has to end in `.bgcode` or `.gcode`. Folders, read-only files and files the printer reports without a date are never offered.
- **Never while printing.** If a print is running, nothing is read or deleted. The file of the current and of the last print is never offered.
- **One folder.** Only the named folder is read, not the folders below. A printer on Buddy firmware answers one command at a time and needs 12 to 15 seconds for about 300 files, so reading takes a moment and only happens when you ask for it.
- **Only listed printers.** A printer has to be in `allowed_devices` (empty by default, so printer rules are off until the operator lists a printer).

Changes made on the printer's own screen show up the next time the printer is read.

### Keep the newest N, delete only what Connect Local still has

Since 0.4.0 a printer rule can keep just the newest N print files (`keep_newest`) with no age limit (`keep_days: 0`, allowed only on printer rules and only with `keep_newest` of at least 1). New printer rules start with the newest 10.

By default a printer rule also **deletes a file only if the same file lies in one of the server's storages**: same long file name (upper and lower case aside) and same size in bytes, in the storage `copy_storage` under the folder `copy_path` and its subfolders (empty: the whole storage). That is normally the case for files you sent to the printer through Connect Local. On the page you pick the folder from a list.

- A file **without** such a copy (for example copied onto the drive straight from the slicer) still counts among the newest N, but is never deleted. Otherwise older files with a copy would move up and go instead. The preview lists these files separately („keine Kopie in Connect Local“).
- If the copies cannot be read completely (storage not readable, folder missing, scan cut short at its limit), **nothing** is deleted.
- The copies are read with the rights of the person on the page, and only if the rule would delete anything at all.
- The copy check can be turned off for a rule run by hand (`skip_copy_check`). An active rule always checks.

The copy storage has to be in `allowed_storages`. A printer rule saved by 0.3.0 has no copy storage yet: it still loads, but deletes nothing until you edit and save it.

### Automatic printer cleanup

Off by default. When the operator turns it on, active printer rules also run every `auto_interval`. Because nobody is signed in at that moment, the module reads and deletes **as itself**, through the module protocol (`device.files`, `device.command` with `delete_file`); it needs no API token for this. Every one of these has to hold, or the rule is skipped and the page says why:

1. `printer_auto` is on and `auto_interval` is set.
2. The printer is in `allowed_devices`, the copy storage in `allowed_storages`, and the rule is active. An active printer rule always checks for copies.
3. The person who last saved the rule may still operate that printer (asked before every run).
4. **The operator made the module an operator of that printer.** The module's instance holds no right on any printer until then; `-approve` does not grant this. With the default instance name:

   ```
   POST /api/v1/grants/device/{device_id}
   {"relation": "operator", "subject": "sink:cleanup"}
   ```

   Take it back with `DELETE /api/v1/grants/device/{device_id}?relation=operator&subject=sink:cleanup`.
5. The printer is connected and not printing, its folder was read fresh, and the copies were read completely.

Each run deletes at most `printer_auto_max` files per rule (default 50), the oldest first. Right before each file it asks the printer's jobs again and stops if a print started or the file became the one of the current or last print. Every deleted file is written into the history.

**Dry run first.** `printer_auto_dry_run` is on by default: the automatic run then deletes nothing and only writes into the history which files it would have deleted. Turn it off once the list looks right.

**The risk, stated openly.** The operator role on a printer is more than deleting files: with it, the module could also start, pause or stop prints and send other commands. This module only lists files, reads the jobs and sends `delete_file`, but the grant itself does not limit it to that. Turn this on only if you accept that, and grant it only on the printers you need. Without the grant and `printer_auto`, printer rules stay by hand.

## Settings

Under Settings → Extensions → Aufräumen:

| Setting | Default | Meaning |
|---|---|---|
| `core_url` | `http://127.0.0.1:8080` | The server's address as the module sees it. Change it if the server listens on another port. |
| `api_token` | empty | API credential with the scope `operate` (Settings → API credentials). Stored as a secret. |
| `auto_interval` | `0s` | How often the active rules run automatically, e.g. `6h`. `0s` turns it off. Storage rules also need `api_token`, printer rules `printer_auto`. |
| `allowed_storages` | `["local"]` | The storages rules may point at. `"*"` allows all. Anything else is neither saved nor cleaned, and the usage view only shows these. |
| `allowed_devices` | `[]` | The printers (ids) whose own storage printer rules may clean. `"*"` allows all, empty none. |
| `printer_auto` | `false` | Lets printer rules be active and run every `auto_interval`. See [Automatic printer cleanup](#automatic-printer-cleanup). |
| `printer_auto_max` | `50` | At most this many files per printer rule and automatic run, 1 to 500. |
| `printer_auto_dry_run` | `true` | The automatic printer run only writes down what it would delete. |

Permissions the module asks for: `read_storage` and a data folder of its own (`regeln.json`, `verlauf.json`). On printers it holds nothing unless the operator grants it the operator role for the automatic printer run. It also asks the server two questions it never acts on itself (`checks: write_storage, operate`), also for people who are not signed in at that moment (`check_absent_people`). See below.

### Permissions and the API token

Prusa Connect Local guards the module's routes: reading (`GET`) needs `view_resource` on the extension, everything else needs `operate_resource`. The automatic run deletes with the API token, which may hold more rights than the people who write the rules. Two layers keep a rule from deleting more than its author could delete by hand:

1. **The operator's lists.** Rules may only point at the storages in `allowed_storages` and the printers in `allowed_devices`. Only someone who may configure the extension changes them.
2. **The author's own rights.** When someone saves a new or changed rule, the module asks the server whether that person may delete there (`write_storage` for storages, `operate` on the printer) and refuses the rule otherwise. The rule remembers who saved it. Before every automatic run the module asks again for that person, and skips the rule if they may no longer delete there or the server cannot answer. The page shows why a rule was skipped.

A rule that nobody has saved since 0.3.0, such as the default rules or rules from an older version, has no author yet. It still works by hand, but it only runs automatically after someone with the right to delete has saved it once.

Limits to be aware of:

- In Prusa Connect Local v0.1.0-beta.1, `write_storage` is granted on all storages together, not per storage. `allowed_storages` is what narrows it to single storages.
- Reading on the page (preview, usage, folders) happens with the signed-in person's rights. The automatic run reads storages with the module's own `read_storage`, and printers only with the operator role the operator granted it (see above).
- Create the token for an account with as few rights as possible, or leave `api_token` empty and clean up by hand only.

## Installation

```sh
make core
make release
prusa-connect module add github.com/gilbert-1705/pcl-cleanup@0.4.1 -from release/cleanup -approve
```

Prusa Connect Local recognises an update only by its version, so every change raises it in `module.yaml`, `main.go` (`release`), `openapi.json` and `ui/package.json`. Restart the server once after an update, because it loads the browser part only at start.

### Upgrading from 0.3.x

0.4.0 is an update of the same module. It also asks for `device.command` (only used by the optional automatic printer run) and has three new settings, all with safe defaults: `printer_auto` off, `printer_auto_max` 50, `printer_auto_dry_run` on. If the update does not ask for them, those defaults apply.

1. Edit every printer rule once: choose the storage and folder that hold the copies, or turn the copy check off for that rule. Until then the rule deletes nothing and says so.
2. Only if you want the automatic printer run: turn on `printer_auto`, set `auto_interval`, grant the module the operator role on the printer (see above), make the rule active, and check the dry runs in the history before you turn `printer_auto_dry_run` off.

### Upgrading from 0.2.x

0.3.0 is an update of the same module, but it asks for more than 0.2.x: the two checks (`write_storage`, `operate`) and `check_absent_people`. Make sure they are granted when you update (with the command line: `-approve`). Without them, saving a rule fails with „the server could not say whether … may delete there“, and automatic runs skip every rule. After that:

1. Check `allowed_storages`: the default is `["local"]`. Add every storage your rules use, or `"*"`.
2. Open the page and save every rule that should run automatically once, as a person who may delete there. Until then the automatic run skips it and says so.
3. For printer rules, put the printer's id into `allowed_devices`.

### Upgrading from 0.1.x

Since 0.2.0 the extension has a new module path and the module name `cleanup` instead of `aufraeumen`. For Prusa Connect Local it is therefore a new module, not an update. The old installation keeps running until you remove it.

1. Install the current version as above.
2. To keep your rules, copy `module-data/aufraeumen/regeln.json` to `module-data/cleanup/regeln.json` and restart the server.
3. Enter the settings for the new extension and save the rules once (see above).
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
| `policy.go` | the operator's lists and the author's rights |
| `plan.go` | what a rule would delete now, storage usage |
| `printer.go` | printer rules: reading a printer's storage |
| `copies.go` | printer rules: the copies on the server, the folder list |
| `auto_printer.go` | the optional automatic run for printer rules |
| `cleaner.go` | settings, history, deleting through the HTTP API |
| `ui/` | the page „Aufräumen“ (Svelte 5, `@prusa/connect-ui`) |

Routes under `/api/v1/modules/com.github.aux.gilbert-1705.pcl-cleanup/`:

| Route | Purpose |
|---|---|
| `GET /overview` | rules, settings, history |
| `PUT /rules` | save the rules (checked against the person's rights) |
| `GET /plan?rule=…` | what a rule would delete now (a printer rule only when asked for alone) |
| `GET /usage?storage=…` | usage per top-level folder |
| `GET /folders?storage=…&path=…` | the folders one level below a path, for picking the copy folder |
| `POST /runs` | record a run made by hand |
| `POST /auto/run` | start an automatic run now |

`/state`, `/openapi.json` and `/ui/` are taken by the server, hence `/overview`.

## Changes

- **0.4.1:** Release through the Prusa Plugin Marketplace: `require.core`, English title and summary, `CHANGELOG.md`, release workflow. No change in behaviour.
- **0.4.0:** Printer rules keep the newest N (`keep_days: 0`) and by default delete only files with a copy on the server (same name and size). Optional automatic run for printer rules (`printer_auto`, off), with a per-run limit and a dry run that is on by default.
- **0.3.0:** Printer rules for a printer's own storage (by hand). Rules remember who saved them; saving and every automatic run check that person's own rights. New settings `allowed_storages` and `allowed_devices`. The page reads storages with the signed-in person's rights. The history takes only runs of existing rules.
- **0.2.0:** Own repository, new module path. In folder mode, a scan cut short at its limit no longer counts unread folders as old. The page deletes in the storage of the plan it read.

## Security

Please do not report security issues in public issues. See [SECURITY.md](SECURITY.md).

Every push, every pull request and once a week, Gitleaks (secrets, including the Git history), Trivy (known vulnerabilities, misconfiguration, secrets) and Semgrep (static analysis) run in CI. Locally the same runs with `./scripts/security-review.sh`, and `pre-commit install` adds a Gitleaks check before every commit.

## License

MIT, see [LICENSE](LICENSE).

Built on Prusa Connect Local by Prusa Research a.s. Prusa Connect Local itself is licensed under OCL v1.1 with add-ons, see its repository. This repository contains none of its code; `make core` fetches it into `.core`, which is not committed. A built release does contain parts of it (the Go SDK in the binary, `@prusa/connect-ui` in the page bundle).
