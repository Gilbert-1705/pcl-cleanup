# pcl-cleanup – Aufräumen für Prusa Connect Local

*English version: [README.md](README.md)*

Eine Erweiterung für [Prusa Connect Local](https://github.com/prusa3d/prusa-connect), den selbst betriebenen Prusa-Connect-Server. Prusa Connect Local löscht von sich aus nichts: Die Kamera schreibt laufend Snapshots, eine Zeitraffer-Erweiterung legt je Druck einen Ordner an, und andere Erweiterungen speichern ebenfalls Bilder. Diese Erweiterung löscht solche Dateien nach Regeln je Ordner. Bevor etwas gelöscht wird, zeigt sie, was weg würde, wie der Speicher belegt ist und was zuletzt gelöscht wurde.

Stand: 0.5.0, gebaut und getestet gegen Prusa Connect Local **v0.1.0-beta.1**. Die Seite ist englisch mit deutscher Übersetzung; Prusa Connect Local zeigt die Sprache der lesenden Person. Pläne für spätere Versionen stehen in [ROADMAP.md](ROADMAP.md).

## Was sie macht

- Eine Seite „Aufräumen“ (englisch „Cleanup“) unter Erweiterungen in der Seitenleiste, mit den Regeln, einer Vorschau je Regel, den jetzt fälligen Regeln, der Speicherbelegung und einem Verlauf.
- Jede **Regel** nennt einen Speicher, einen Ordner, eine Art und wie lange etwas bleibt:
  - **Dateien:** jede Datei für sich, nach ihrer Änderungszeit, wahlweise gefiltert nach einem Muster wie `*.jpg`.
  - **Ordner:** ganze Ordner auf einer Ebene, gealtert nach der neuesten Datei darin. Ein Zeitraffer-Lauf geht immer als Ganzes.
  - `keep_newest` behält die neuesten N Einheiten unabhängig vom Alter.
  - Eine Regel auf die oberste Ebene eines Speichers wird abgelehnt.
- **Drucker-Regeln** räumen alte Druckdateien vom eigenen Speicher eines Druckers, etwa dem USB-Stick. Sie können nur die neuesten N Dateien behalten und nur Dateien löschen, die auch im Speicher von Connect Local liegen. Siehe [Speicher eines Druckers](#speicher-eines-druckers).
- Grenzen je Durchlauf: 500.000 Einträge, 3.000 Pfade je Plan, 200 Einträge in der Belegung. Ein Plan, der an eine Grenze stieß, sagt das. Im Ordner-Modus bleiben Ordner, die nicht vollständig gelesen wurden, aus einem solchen Plan heraus; sie werden durch ihn weder behalten noch gelöscht.

**Voreingestellte Regeln:**

| Regel | Wo | Behalten | Zustand |
|---|---|---|---|
| Camera snapshots | `local:camera`, `*.jpg` | 14 Tage | aktiv |
| Timelapses | `local:zeitraffer`, Ordner auf Ebene 2 | 180 Tage, die neuesten 10 immer | aktiv |
| PrintWatch snapshots | `local:printwatch`, `*.jpg` | 7 Tage | aus |

`zeitraffer/` ist der Ordner, in den [pcl-timelapse](https://github.com/Gilbert-1705/pcl-timelapse) standardmäßig schreibt. Die Namen der Regeln lassen sich auf der Seite ändern.

## Wie gelöscht wird

Das Modulprotokoll von Prusa Connect Local kann Speicher lesen und schreiben, aber darin nicht löschen. Diese Erweiterung löscht deshalb selbst nie in einem Speicher. Sie hat keinen API-Zugang und baut keine Netzwerkverbindung auf.

- **Speicher-Regeln, von Hand:** „Jetzt aufräumen“ an einer Regel oder **„Fällige Regeln ausführen“** für jede aktive Regel, die etwas zu löschen hat. Die Seite zeigt zuerst Anzahl und Größe, plant jede Regel kurz vor dem Löschen noch einmal und löscht aus dem Browser mit den Rechten der angemeldeten Person. Der Server prüft diese Rechte bei jedem Löschen.
- **Speicher-Regeln, von selbst:** noch nicht. Dafür braucht es einen Löschaufruf im Modulprotokoll; siehe [ROADMAP.md](ROADMAP.md). Bis dahin heißt „aktiv“ bei einer Speicher-Regel: Sie gehört zu „Fällige Regeln ausführen“.
- **Drucker-Regeln, von selbst** (ab Werk aus): über das Modulprotokoll, als Modul selbst, nur mit `printer_auto` und der Operator-Rolle, die die Administration ihm auf diesem Drucker gegeben hat. Siehe [Automatisches Aufräumen am Drucker](#automatisches-aufräumen-am-drucker).

Löschen ist endgültig. Vor dem ersten Lauf die Vorschau jeder Regel prüfen.

## Speicher eines Druckers

Eine Drucker-Regel nennt einen Drucker, einen Ordner auf seinem Speicher (zum Beispiel `usb`) und ein Muster für Druckdateien (`*.bgcode` oder `*.gcode`). Sie ist bewusst eng gefasst:

- **Ab Werk von Hand.** Die Seite liest zuerst den Drucker („Drucker lesen“), zeigt dann, was weg würde, und löscht nach Bestätigung. Ein automatischer Lauf ist eine Option, die die Administration einschalten muss, siehe [Automatisches Aufräumen am Drucker](#automatisches-aufräumen-am-drucker).
- **Mit deinen Rechten.** Von Hand liest und löscht das Modul am Drucker für die angemeldete Person; sie braucht `operate` auf diesem Drucker.
- **Nur Druckdateien.** Das Muster muss auf `.bgcode` oder `.gcode` enden. Ordner, schreibgeschützte Dateien und Dateien, die der Drucker ohne Datum meldet, werden nie angeboten.
- **Nie während eines Drucks.** Läuft ein Druck, wird nichts gelesen oder gelöscht. Die Datei des laufenden und des letzten Drucks wird nie angeboten.
- **Ein Ordner.** Nur der genannte Ordner wird gelesen, nicht die Ordner darunter. Ein Drucker mit Buddy-Firmware beantwortet einen Befehl nach dem anderen und braucht für etwa 300 Dateien 12 bis 15 Sekunden; deshalb dauert das Lesen etwas und geschieht nur auf Knopfdruck.
- **Nur freigegebene Drucker.** Ein Drucker muss in `allowed_devices` stehen (ab Werk leer, also sind Drucker-Regeln aus, bis die Administration einen Drucker einträgt).

Änderungen am Bildschirm des Druckers erscheinen beim nächsten Lesen.

### Die neuesten N behalten, nur löschen, was Connect Local noch hat

Eine Drucker-Regel kann nur die neuesten N Druckdateien behalten (`keep_newest`), ohne Altersgrenze (`keep_days: 0`, nur bei Drucker-Regeln und nur mit `keep_newest` von mindestens 1). Neue Drucker-Regeln beginnen mit den neuesten 10.

Ab Werk **löscht eine Drucker-Regel eine Datei nur, wenn dieselbe Datei in einem Speicher des Servers liegt**: gleicher langer Dateiname (ohne Rücksicht auf Groß- und Kleinschreibung) und gleiche Größe in Byte, im Speicher `copy_storage` unter dem Ordner `copy_path` und seinen Unterordnern (leer: der ganze Speicher). Das ist normalerweise bei Dateien der Fall, die über Connect Local an den Drucker gingen. Den Ordner wählst du auf der Seite aus einer Liste.

- Eine Datei **ohne** solche Kopie (etwa direkt aus dem Slicer auf den Stick kopiert) zählt trotzdem zu den neuesten N, wird aber nie gelöscht. Sonst rückten ältere Dateien mit Kopie nach und gingen stattdessen. Die Vorschau listet diese Dateien gesondert.
- Lassen sich die Kopien nicht vollständig lesen (Speicher nicht lesbar, Ordner fehlt, Durchlauf an seiner Grenze abgeschnitten), wird **nichts** gelöscht.
- Die Kopien werden mit den Rechten der Person auf der Seite gelesen, und nur, wenn die Regel überhaupt etwas löschen würde.
- Für eine Regel, die von Hand läuft, lässt sich der Kopie-Abgleich abschalten (`skip_copy_check`). Eine aktive Regel prüft immer.

Der Kopie-Speicher muss in `allowed_storages` stehen.

### Automatisches Aufräumen am Drucker

Ab Werk aus. Schaltet die Administration es ein, laufen aktive Drucker-Regeln zusätzlich alle `auto_interval`. Weil in dem Moment niemand angemeldet ist, liest und löscht das Modul **als es selbst**, über das Modulprotokoll (`device.files`, `device.command` mit `delete_file`). Jede dieser Bedingungen muss gelten, sonst wird die Regel ausgelassen und die Seite sagt, warum:

1. `printer_auto` ist an und `auto_interval` gesetzt.
2. Der Drucker steht in `allowed_devices`, der Kopie-Speicher in `allowed_storages`, und die Regel ist aktiv. Eine aktive Drucker-Regel prüft immer auf Kopien.
3. Die Person, die die Regel zuletzt gespeichert hat, darf den Drucker noch bedienen (vor jedem Lauf gefragt).
4. **Die Administration hat das Modul zum Operator dieses Druckers gemacht.** Bis dahin hat die Instanz des Moduls auf keinem Drucker ein Recht; `-approve` gibt es nicht. Mit dem Standard-Instanznamen:

   ```
   POST /api/v1/grants/device/{device_id}
   {"relation": "operator", "subject": "sink:cleanup"}
   ```

   Zurücknehmen mit `DELETE /api/v1/grants/device/{device_id}?relation=operator&subject=sink:cleanup`.
5. Der Drucker ist verbunden und druckt nicht, sein Ordner wurde frisch gelesen, und die Kopien wurden vollständig gelesen.

Jeder Lauf löscht höchstens `printer_auto_max` Dateien je Regel (Standard 50), die ältesten zuerst. Direkt vor jeder Datei fragt er die Aufträge des Druckers erneut und hört auf, wenn ein Druck begonnen hat oder die Datei zu der des laufenden oder letzten Drucks wurde. Jede gelöschte Datei steht im Verlauf.

**Erst Probelauf.** `printer_auto_dry_run` ist ab Werk an: Der automatische Lauf löscht dann nichts und schreibt nur in den Verlauf, welche Dateien er gelöscht hätte. Ausschalten, wenn die Liste stimmt.

**Das Risiko, offen gesagt.** Die Operator-Rolle auf einem Drucker ist mehr als Dateien löschen: Damit könnte das Modul auch Drucke starten, pausieren oder abbrechen und andere Befehle senden. Dieses Modul listet nur Dateien, liest die Aufträge und sendet `delete_file`, aber die Freigabe selbst begrenzt es nicht darauf. Nur einschalten, wenn du das akzeptierst, und nur auf den Druckern freigeben, die du brauchst. Ohne Freigabe und `printer_auto` bleiben Drucker-Regeln von Hand.

## Einstellungen

Unter Einstellungen → Erweiterungen → Cleanup. Die Beschreibungen dort sind englisch, weil Prusa Connect Local sie als reinen Text aus `module.yaml` liest.

| Einstellung | Standard | Bedeutung |
|---|---|---|
| `auto_interval` | `0s` | Wie oft die aktiven Drucker-Regeln von selbst laufen, z. B. `6h`. `0s` schaltet das aus. Braucht `printer_auto`. Speicher-Regeln laufen nur von der Seite aus. |
| `allowed_storages` | `["local"]` | Die Speicher, auf die Regeln zeigen dürfen. `"*"` erlaubt alle. Anderes wird weder gespeichert noch ausgeführt, und die Belegung zeigt nur diese. |
| `allowed_devices` | `[]` | Die Drucker (IDs), deren eigenen Speicher Drucker-Regeln aufräumen dürfen. `"*"` erlaubt alle, leer keinen. |
| `printer_auto` | `false` | Drucker-Regeln dürfen aktiv sein und laufen alle `auto_interval`. Siehe [Automatisches Aufräumen am Drucker](#automatisches-aufräumen-am-drucker). |
| `printer_auto_max` | `50` | Höchstens so viele Dateien je Drucker-Regel und automatischem Lauf, 1 bis 500. |
| `printer_auto_dry_run` | `true` | Der automatische Drucker-Lauf schreibt nur auf, was er löschen würde. |

Rechte, um die das Modul bittet: `read_storage` und ein eigener Datenordner (`regeln.json`, `verlauf.json`). Es meldet keine Netzwerkverbindung an (`egress`). Auf Druckern hat es nichts, solange die Administration ihm für den automatischen Drucker-Lauf nicht die Operator-Rolle gibt. Außerdem stellt es dem Server zwei Fragen, nach denen es selbst nie handelt (`checks: write_storage, operate`), auch für Personen, die gerade nicht angemeldet sind (`check_absent_people`). Siehe unten.

### Berechtigungen

Prusa Connect Local schützt die Routen des Moduls: Lesen (`GET`) braucht `view_resource` auf die Erweiterung, alles andere `operate_resource`. Zwei Ebenen verhindern, dass eine Regel mehr löscht, als ihre Autorin oder ihr Autor von Hand löschen könnte:

1. **Die Listen der Administration.** Regeln dürfen nur auf die Speicher in `allowed_storages` und die Drucker in `allowed_devices` zeigen. Ändern kann sie nur, wer die Erweiterung konfigurieren darf.
2. **Die eigenen Rechte der Person.** Speichert jemand eine neue oder geänderte Regel, fragt das Modul den Server, ob diese Person dort löschen darf (`write_storage` bei Speichern, `operate` am Drucker), und lehnt die Regel sonst ab. Die Regel merkt sich, wer sie gespeichert hat. Vor jedem automatischen Drucker-Lauf fragt das Modul für diese Person erneut und lässt die Regel aus, wenn sie den Drucker nicht mehr bedienen darf oder der Server nicht antwortet. Die Seite zeigt, warum eine Regel ausgelassen wurde.

Eine Regel, die noch niemand gespeichert hat, etwa eine voreingestellte, hat keine Autorin und keinen Autor. Sie funktioniert von Hand, eine Drucker-Regel läuft aber erst von selbst, nachdem jemand mit dem Recht, diesen Drucker zu bedienen, sie einmal gespeichert hat.

Grenzen, die man kennen sollte:

- In Prusa Connect Local v0.1.0-beta.1 gilt `write_storage` für alle Speicher zusammen, nicht je Speicher. Erst `allowed_storages` grenzt auf einzelne Speicher ein.
- Gelesen wird auf der Seite (Vorschau, Belegung, Ordner) mit den Rechten der angemeldeten Person. Der automatische Drucker-Lauf liest Speicher mit dem eigenen `read_storage` des Moduls und Drucker nur mit der Operator-Rolle, die die Administration ihm gegeben hat (siehe oben).

## Installation

Über den Prusa Plugin Marketplace, sobald sie dort gelistet ist, oder aus einem Release:

```sh
make core
make release
prusa-connect module add github.com/gilbert-1705/pcl-cleanup@0.5.0 -from release/cleanup -approve
```

Prusa Connect Local erkennt ein Update nur an der Version; jede Änderung hebt sie deshalb in `module.yaml`, `main.go` (`release`), `openapi.json` und `ui/package.json`. Nach einem Update den Server einmal neu starten, weil er den Browser-Teil nur beim Start lädt.

### Update von 0.4.x

0.5.0 entfernt die Einstellungen `api_token` und `core_url`, weil Speicher-Regeln nicht mehr über die HTTP-API des Servers löschen. Der Server lehnt Einstellungen ab, die ein Modul nicht mehr kennt; **deshalb beide Schlüssel vor dem Update aus den Einstellungen der Erweiterung entfernen.** Danach den API-Zugang, den du für diese Erweiterung angelegt hattest, unter Einstellungen → API-Zugangsdaten löschen. Regeln und Verlauf bleiben, wie sie sind.

## Bauen

```sh
make core      # Prusa Connect Local v0.1.0-beta.1 nach .core holen und die Bibliotheken bauen
make test      # Go-Tests und svelte-check
make release   # release/cleanup/ und release/cleanup-<version>.tar.gz
```

Braucht Go 1.26, Node 22, pnpm 11 und git. `ui/pnpm-workspace.yaml` nimmt neue Paketversionen erst nach sieben Tagen an und indirekte Abhängigkeiten nur aus der Registry.

## Übersetzungen

Die Texte der Seite liegen in `ui/src/locales/`: `en.json` ist die Quelle, `de.json` die deutsche Übersetzung. Fehlt einer Sprache ein Schlüssel, erscheint der englische Text. Für eine neue Sprache `en.json` nach `<sprache>.json` kopieren, die Werte übersetzen und die Schlüssel sowie die Teile `{name}` und `{count, plural, …}` unverändert lassen. Die Texte in `module.yaml` (Titel, Kurztext, Einstellungen) gibt es nur englisch, weil Prusa Connect Local sie als reinen Text liest.

## Aufbau

| Datei | Inhalt |
|---|---|
| `main.go` | Start, die Routen des Moduls, der Zeitplan des Drucker-Laufs |
| `rules.go` | Regeln, Voreinstellungen, Prüfung |
| `policy.go` | die Listen der Administration und die Rechte der Person |
| `plan.go` | was eine Regel jetzt löschen würde, Speicherbelegung |
| `printer.go` | Drucker-Regeln: den Speicher eines Druckers lesen |
| `copies.go` | Drucker-Regeln: die Kopien auf dem Server, die Ordnerliste |
| `auto_printer.go` | der optionale automatische Lauf für Drucker-Regeln |
| `cleaner.go` | Einstellungen und Verlauf |
| `ui/` | die Seite „Cleanup“ (Svelte 5, `@prusa/connect-ui`), mit `ui/src/locales/` |

Routen unter `/api/v1/modules/com.github.aux.gilbert-1705.pcl-cleanup/`:

| Route | Zweck |
|---|---|
| `GET /overview` | Regeln, Einstellungen, Verlauf |
| `PUT /rules` | Regeln speichern (gegen die Rechte der Person geprüft) |
| `GET /plan?rule=…` | was eine Regel jetzt löschen würde (eine Drucker-Regel nur einzeln angefragt) |
| `GET /usage?storage=…` | Belegung je Ordner der obersten Ebene |
| `GET /folders?storage=…&path=…` | die Ordner eine Ebene unter einem Pfad, für die Wahl des Kopie-Ordners |
| `POST /runs` | einen Lauf von der Seite in den Verlauf schreiben |
| `POST /auto/run` | den automatischen Drucker-Lauf jetzt starten |

`/state`, `/openapi.json` und `/ui/` belegt der Server, daher `/overview`.

## Änderungen

Siehe [CHANGELOG.md](CHANGELOG.md) (englisch).

## Sicherheit

Sicherheitsprobleme bitte nicht in öffentlichen Issues melden. Siehe [SECURITY.md](SECURITY.md).

Bei jedem Push, jedem Pull Request und einmal pro Woche laufen in der CI Gitleaks (Secrets, auch in der Git-Historie), Trivy (bekannte Schwachstellen, Fehlkonfiguration, Secrets) und Semgrep (statische Analyse). Bei jedem Push und Pull Request laufen außerdem `go vet`, die Go-Tests, ein Cross-Build und `svelte-check`. Lokal laufen dieselben Scans mit `./scripts/security-review.sh`, und `pre-commit install` ergänzt eine Gitleaks-Prüfung vor jedem Commit.

## Lizenz

MIT, siehe [LICENSE](LICENSE).

Gebaut auf Prusa Connect Local von Prusa Research a.s. Prusa Connect Local selbst steht unter OCL v1.1 mit Zusätzen, siehe dessen Repository. Dieses Repository enthält keinen Code davon; `make core` holt ihn nach `.core`, das nicht eingecheckt wird. Ein gebautes Release enthält Teile davon (das Go-SDK im Programm, `@prusa/connect-ui` im Bündel der Seite).
