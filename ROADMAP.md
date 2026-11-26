# Roadmap

What is planned for this extension, and what it waits for. Nothing here is a
promise of a date.

## Storage rules that run on their own

**Waits for:** a call to delete files in a storage in the module protocol of
Prusa Connect Local, proposed in
[prusa3d/prusa-connect#96](https://github.com/prusa3d/prusa-connect/issues/96)
(`storage.delete`, allowed by `write_storage`).

**Why it waits.** Up to 0.4.1, storage rules could run on a schedule. The
module then deleted through the server's HTTP API with an API credential the
operator pasted into its settings. That credential carried every right of its
owner within its scope, more than the extension asks for when it is
installed, and the limits around it were held only by the extension's own
code. 0.5.0 removed that path. Since then storage rules only run from the
page, with the rights of the person who starts them.

**What changes once the call exists:**

- The extension asks for `write_storage` in `module.yaml` and states the
  `core-storage.delete` capability under `require`, so it only installs on a
  server that has the call.
- Active storage rules run every `auto_interval` again, deleting through the
  module protocol, within the permissions the operator approved at install.
- Before each run the extension still asks whether the person who saved the
  rule may delete there (`checks: write_storage`), as it does for printer
  rules today, and skips the rule otherwise.
- A dry run, on by default, like the automatic printer run.
- Folder rules delete the files of a folder first and then the empty folder,
  if the call deletes only empty folders.

## Smaller items

- More translations of the page. See [Translations](README.md#translations).
