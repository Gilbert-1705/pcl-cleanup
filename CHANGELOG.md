# Changelog

What an operator sees change when they upgrade this module, newest release
first. `version:` in `module.yaml` is the newest heading below. The names
below are the setting keys and routes as they appear in `module.yaml` and
`openapi.json`.

## 0.5.0 - 2026-11-26

### Removed

- The settings `api_token` and `core_url`, and with them every network connection of the module: `module.yaml` declares no `egress` any more. Storage rules no longer run on a schedule, because the module protocol cannot delete in a storage and the API credential gave the module more rights than it asks for at install. Running them on their own again waits for a delete call in the module protocol, see `ROADMAP.md`.
- Before updating from 0.4.x, remove `api_token` and `core_url` from the extension's settings: the server refuses settings a module no longer declares. Then delete the API credential created for this extension.

### Added

- „Run due rules“ on the page: every active storage rule that has something to delete, planned again just before it deletes, with the rights of the signed-in person.
- `ROADMAP.md`.

### Security

- The overview shows what comes from a printer only to a person who may observe that printer: printer rules of other printers come with their name only, marked hidden, and keep as they are when the list is saved; the files an automatic run deleted and why a rule was skipped come only with the printer. Who saved a rule, and who cleaned up by hand, come only to a person who may delete where the rule points (and the latter also to that person). The extension asks the server `observe` for this (`checks` in `module.yaml`).
- Removing a rule takes the same right as changing it. `PUT /rules` needs the `rules_version` of the overview in `If-Match` (428 without, 412 when the rules changed meanwhile), and saves run one at a time. The version is keyed anew at each start, so it cannot be checked against a guess of rules a person may not see; it still changes when any rule changes. Two rules may not share a name (422).
- A printer rule deletes only a path the printer reports directly in the rule's folder with a print file ending (also the FAT short forms `.bgc` and `.gco`), not only by the name it shows.
- The copy of a print file may be at most 24 hours newer than the file on the printer. The README says what the copy check cannot do.
- `PUT /rules`, `POST /runs` and `POST /auto/run` take `application/json` only (415 otherwise).
- A folder rule with a pattern is refused (422); one stored by an earlier version loads without it.
- The history is written through a file of its own and a rename.
- Built against Prusa Connect Local v0.1.0-beta.2; `make core`, the CI and the Marketplace builder check the commit of the tag.
- `minimatch` 11.0.9 for the lint tools (GHSA-g3hv-75wg-m249); it is not in the page bundle.

### Changed

- The page is English with a German translation (`ui/src/locales/`), shown in the reader's language. Sizes and dates follow the reader's language. The texts in `module.yaml` are English.
- An active storage rule now means that it is part of „Run due rules“.
- `auto_interval` only drives the automatic printer run; `POST /auto/run` only runs printer rules and is refused unless `printer_auto` and `auto_interval` are set.
- The default rules on a new install are called „Camera snapshots“, „Timelapses“ and „PrintWatch snapshots“. Existing rules keep their names.
- The history writes who ran a cleanup as a code (`schedule`, `request`, `hand:<person>`), which the page words in the reader's language. Entries from older versions are shown as they were written.

## 0.4.1 - 2026-11-22

Submitted to the Prusa Plugin Marketplace; not published there (see 0.5.0).

### Changed

- `core_url` has to point at this machine: `127.0.0.1`, `::1` or `localhost`, on any port. Any other host, a user, a query or a scheme other than `http`/`https` is refused when the module is configured, and the module does not start. Before, `core_url` could name any host.
- The client for `core_url` does not follow redirects, ignores `HTTP_PROXY`/`HTTPS_PROXY` and only connects to loopback addresses. A redirect counts as a failed deletion.
- `module.yaml` names the Prusa Connect versions it runs on: `require.core` is `>=0.1.0-beta.1`.
- Title and summary are English (`Cleanup`); the page itself is still called „Aufräumen“.
- The `egress` entry for `core_url` explains in English that the connection goes back to this server's own HTTP API on this machine, on whatever port it listens.

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
