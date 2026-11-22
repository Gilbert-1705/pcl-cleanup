# Changelog

What an operator sees change when they upgrade this module, newest release
first. `version:` in `module.yaml` is the newest heading below. The page and
the settings of the module are German; the names below are the setting keys
and routes as they appear in `module.yaml` and `openapi.json`.

## 0.4.1 - 2026-11-22

First release through the Prusa Plugin Marketplace. No change in behaviour.

### Changed

- `module.yaml` names the Prusa Connect versions it runs on: `require.core` is `>=0.1.0-beta.1`.
- Title and summary are English (`Cleanup`); the page itself is still called „Aufräumen“.
- The `egress` entry for `core_url` explains in English that the connection goes back to this server's own HTTP API, and why its port can differ from 8080.

## 0.4.0 - 2026-11-22

### Added

- Printer rules can keep only the newest files: `keep_days` may be `0` on a printer rule when `keep_newest` is at least 1. New printer rules keep the newest 10.
- Printer rules delete a file only if a file with the same long name and size lies in the server's storage `copy_storage` under `copy_path`. Files without such a copy count among the newest and are never deleted. If the copies cannot be read completely, nothing is deleted. `skip_copy_check` turns this off for a rule run by hand.
- `GET /folders` lists the folders of an allowed storage, for picking the copy folder on the page.
- Optional automatic run for printer rules, off by default: `printer_auto`, `printer_auto_max` (default 50 files per rule and run) and `printer_auto_dry_run` (default on, deletes nothing and writes what it would delete into the history). It needs the operator role on the printer for the module's instance, granted by the operator.

### Changed

- A printer rule saved by 0.3.x deletes nothing until it is saved again with a copy storage.
- Storage rules still need `api_token` to run automatically.

## 0.3.0 - 2026-11-22

### Added

- Printer rules (`target: printer`) for a printer's own storage, such as its USB drive, by hand only: print files only, never while printing, never the file of the current or last print.
- Settings `allowed_storages` (default `["local"]`) and `allowed_devices` (default empty) limit where rules may point.

### Changed

- Rules remember who saved them. Saving and every automatic run check that person's own rights (`write_storage`, `operate`).
- The page reads storages with the signed-in person's rights.
- `POST /runs` takes only runs of existing rules.

## 0.2.0 - 2026-11-21

### Changed

- Own repository and new module path `github.com/gilbert-1705/pcl-cleanup` (module name `cleanup`). An installation of an older version under another path is a different module.

### Fixed

- In folder mode, a scan cut short at its limit no longer counts unread folders as old.
- The page deletes in the storage of the plan it read.
