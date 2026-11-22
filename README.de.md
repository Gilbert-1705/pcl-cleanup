# pcl-cleanup – Aufräumen für Prusa Connect Local

*Deutsche Übersetzung. Maßgeblich ist die englische [README.md](README.md).*

Eine Erweiterung für [Prusa Connect Local](https://github.com/prusa3d/prusa-connect), den selbst gehosteten Prusa-Connect-Server. Prusa Connect Local löscht von sich aus nichts: Die Kamera legt laufend Snapshots ab, eine Zeitraffer-Erweiterung schreibt pro Druck einen Ordner, und andere Erweiterungen legen eventuell Bilder ab. Diese Erweiterung löscht so etwas nach Regeln je Ordner. Vorher zeigt sie, was weg kann, wie sich der Speicher verteilt und was zuletzt gelöscht wurde.

Stand: 0.4.1, gebaut und getestet gegen Prusa Connect Local **v0.1.0-beta.1**. Die Oberfläche ist deutsch, die Seite heißt „Aufräumen“.

Bis 0.1.1 lag die Erweiterung zusammen mit einer Zeitraffer-Erweiterung in einem gemeinsamen Repository, unter einem anderen Modulpfad. Seit 0.2.0 hat sie ein eigenes Repository. Wer 0.1.x nutzt, findet den [Umstieg von 0.1.x](#umstieg-von-01x) weiter unten.

## Was sie kann

- Eine Seite „Aufräumen“ in der Seitenleiste unter Erweiterungen, mit Regeln, Vorschau je Regel, „Jetzt aufräumen“, Speicherbelegung und Verlauf.
- Jede **Regel** nennt Speicher, Ordner, Modus und Aufbewahrung:
  - **Dateien:** jede Datei einzeln nach Änderungsdatum, optional mit Muster wie `*.jpg`.
  - **Ordner:** ganze Ordner auf einer bestimmten Ebene, gealtert nach der neuesten Datei darin. Ein Zeitraffer-Lauf geht so immer komplett.
  - `keep_newest` schützt die neuesten N Einheiten unabhängig vom Alter.
  - Eine Regel auf die oberste Ebene eines Speichers wird abgelehnt.
- **Drucker-Regeln** (seit 0.3.0) räumen alte Druckdateien vom eigenen Speicher eines Druckers, etwa seinem USB-Stick. Seit 0.4.0 können sie nur die neuesten N Dateien behalten und löschen nur, was auch im Speicher von Connect Local liegt. Siehe [Speicher eines Druckers](#speicher-eines-druckers).
- Grenzen pro Scan: 500 000 Einträge, 3 000 Pfade pro Plan, 200 Einträge in der Belegung. Ein abgeschnittener Plan sagt das. Im Ordner-Modus bleiben Ordner, die nicht vollständig gelesen wurden, aus so einem Plan heraus. Sie werden dann weder behalten noch gelöscht.

**Voreingestellte Regeln:**

| Regel | Ort | Behalten | Status |
|---|---|---|---|
| Kamera-Snapshots | `local:camera`, `*.jpg` | 14 Tage | aktiv |
| Zeitraffer | `local:zeitraffer`, Ordner auf Ebene 2 | 180 Tage, die neuesten 10 immer | aktiv |
| PrintWatch-Snapshots | `local:printwatch`, `*.jpg` | 7 Tage | aus |

## Wie gelöscht wird

Das Modulprotokoll von Prusa Connect Local kann Speicher lesen und schreiben, aber nicht löschen. Deshalb gibt es zwei Wege:

- **Von Hand:** „Jetzt aufräumen“ zeigt vorher Anzahl und Größe und löscht dann im Browser mit den Rechten der angemeldeten Person.
- **Automatisch, im Speicher:** Der Go-Prozess ruft die HTTP-API des eigenen Servers auf (`POST /api/v1/storages/{storage}/deletions`). Dafür braucht er die Adresse des Servers (`core_url`) und einen API-Zugang mit dem Umfang `operate` (`api_token`). **Ohne Token löscht keine Speicher-Regel automatisch.**
- **Automatisch, am Drucker** (seit 0.4.0, ab Werk aus): über das Modulprotokoll, als Erweiterung selbst, nur mit `printer_auto` und der Operator-Rolle, die die Administration ihr auf diesem Drucker gibt. Siehe [Automatisch am Drucker aufräumen](#automatisch-am-drucker-aufräumen).

Löschen ist endgültig. Vor dem ersten Lauf lohnt sich ein Blick in die Vorschau jeder Regel.

## Speicher eines Druckers

Eine Drucker-Regel nennt einen Drucker, einen Ordner auf seinem Speicher (zum Beispiel `usb`) und ein Muster für Druckdateien (`*.bgcode` oder `*.gcode`). Sie ist bewusst eng gefasst:

- **Ab Werk nur von Hand.** Die Seite liest erst den Drucker („Drucker lesen“), zeigt dann, was weg kann, und löscht nach deiner Bestätigung. Ein automatischer Lauf ist eine Option, die die Administration einschalten muss, siehe [Automatisch am Drucker aufräumen](#automatisch-am-drucker-aufräumen).
- **Mit deinen Rechten.** Von Hand wird für die angemeldete Person gelesen und gelöscht, die dafür `operate` auf den Drucker braucht.
- **Nur Druckdateien.** Das Muster muss auf `.bgcode` oder `.gcode` enden. Ordner, schreibgeschützte Dateien und Dateien, für die der Drucker kein Datum meldet, werden nie angeboten.
- **Nie während eines Drucks.** Läuft ein Druck, wird nichts gelesen und nichts gelöscht. Die Datei des laufenden und des letzten Drucks wird nie angeboten.
- **Ein Ordner.** Gelesen wird nur der genannte Ordner, nicht die Ordner darunter. Ein Drucker mit Buddy-Firmware beantwortet einen Befehl nach dem anderen und braucht für etwa 300 Dateien 12 bis 15 Sekunden. Das Lesen dauert also etwas und passiert nur auf Knopfdruck.
- **Nur freigegebene Drucker.** Der Drucker muss in `allowed_devices` stehen. Ab Werk ist die Liste leer, Drucker-Regeln sind also aus, bis jemand einen Drucker einträgt.

Änderungen am Display des Druckers sieht die Erweiterung beim nächsten Lesen.

### Nur die neuesten N behalten, nur löschen, was Connect Local noch hat

Seit 0.4.0 kann eine Drucker-Regel nur die neuesten N Druckdateien behalten (`keep_newest`), ohne Altersgrenze (`keep_days: 0`, nur bei Drucker-Regeln und nur mit `keep_newest` von mindestens 1). Neue Drucker-Regeln starten mit den neuesten 10.

Ab Werk **löscht eine Drucker-Regel eine Datei außerdem nur, wenn dieselbe Datei in einem Speicher des Servers liegt**: gleicher langer Dateiname (Groß- und Kleinschreibung egal) und gleiche Größe in Byte, im Speicher `copy_storage` unter dem Ordner `copy_path` samt Unterordnern (leer: ganzer Speicher). Das ist normalerweise der Fall, wenn du über Connect Local druckst. Auf der Seite wählst du den Ordner aus einer Liste.

- Eine Datei **ohne** solche Kopie (etwa direkt aus dem Slicer auf den Stick kopiert) zählt trotzdem zu den neuesten N, wird aber nie gelöscht. Sonst würden ältere Dateien mit Kopie nachrücken und an ihrer Stelle gehen. Die Vorschau zeigt diese Dateien getrennt („keine Kopie in Connect Local“).
- Lassen sich die Kopien nicht vollständig lesen (Speicher nicht lesbar, Ordner fehlt, Scan an seiner Grenze abgeschnitten), wird **nichts** gelöscht.
- Die Kopien werden mit den Rechten der Person auf der Seite gelesen, und nur, wenn die Regel überhaupt etwas löschen würde.
- Für eine Regel, die von Hand läuft, lässt sich der Abgleich ausschalten (`skip_copy_check`). Eine aktive Regel gleicht immer ab.

Der Kopie-Speicher muss in `allowed_storages` stehen. Eine mit 0.3.0 gespeicherte Drucker-Regel hat noch keinen Kopie-Speicher: Sie lädt weiter, löscht aber nichts, bis du sie bearbeitest und speicherst.

### Automatisch am Drucker aufräumen

Ab Werk aus. Schaltet die Administration es ein, laufen aktive Drucker-Regeln zusätzlich alle `auto_interval`. Weil in dem Moment niemand angemeldet ist, liest und löscht die Erweiterung **als sie selbst**, über das Modulprotokoll (`device.files`, `device.command` mit `delete_file`). Ein API-Token braucht sie dafür nicht. Jeder dieser Punkte muss erfüllt sein, sonst wird die Regel ausgelassen und die Seite sagt, warum:

1. `printer_auto` ist an und `auto_interval` gesetzt.
2. Der Drucker steht in `allowed_devices`, der Kopie-Speicher in `allowed_storages`, und die Regel ist aktiv. Eine aktive Drucker-Regel gleicht immer mit den Kopien ab.
3. Die Person, die die Regel zuletzt gespeichert hat, darf den Drucker weiter bedienen (vor jedem Lauf gefragt).
4. **Die Administration hat die Erweiterung zum Operator dieses Druckers gemacht.** Bis dahin hat die Instanz kein Recht auf irgendeinen Drucker; `-approve` vergibt das nicht. Mit dem Standard-Namen der Instanz:

   ```
   POST /api/v1/grants/device/{device_id}
   {"relation": "operator", "subject": "sink:cleanup"}
   ```

   Zurücknehmen mit `DELETE /api/v1/grants/device/{device_id}?relation=operator&subject=sink:cleanup`.
5. Der Drucker ist verbunden und druckt nicht, sein Ordner wurde frisch gelesen, und die Kopien wurden vollständig gelesen.

Jeder Lauf löscht höchstens `printer_auto_max` Dateien je Regel (Standard 50), die ältesten zuerst. Direkt vor jeder Datei fragt er die Druckaufträge erneut ab und hört auf, wenn ein Druck begonnen hat oder die Datei zu der des laufenden oder letzten Drucks geworden ist. Jede gelöschte Datei steht im Verlauf.

**Erst ein Probelauf.** `printer_auto_dry_run` ist ab Werk an: Der automatische Lauf löscht dann nichts und schreibt nur in den Verlauf, welche Dateien er gelöscht hätte. Ausschalten, wenn die Liste stimmt.

**Das Risiko, offen gesagt.** Die Operator-Rolle auf einem Drucker ist mehr als Dateien löschen: Mit ihr könnte die Erweiterung auch Drucke starten, pausieren oder abbrechen und andere Befehle senden. Diese Erweiterung listet nur Dateien, liest die Druckaufträge und sendet `delete_file`, aber die Freigabe selbst beschränkt sie nicht darauf. Nur einschalten, wenn du das in Kauf nimmst, und nur auf den Druckern freigeben, die du brauchst. Ohne Freigabe und `printer_auto` bleiben Drucker-Regeln Handarbeit.

## Einstellungen

Unter Settings → Extensions → Aufräumen:

| Einstellung | Standard | Bedeutung |
|---|---|---|
| `core_url` | `http://127.0.0.1:8080` | Adresse des Servers aus Sicht des Moduls. Muss dieser Rechner sein (`127.0.0.1`, `::1` oder `localhost`); läuft der Server auf einem anderen Port, den Port anpassen. Andere Hosts werden abgelehnt, Weiterleitungen nicht befolgt. |
| `api_token` | leer | API-Zugang mit Umfang `operate` (Einstellungen → API-Zugangsdaten). Wird als Secret gespeichert. |
| `auto_interval` | `0s` | Wie oft die aktiven Regeln automatisch laufen, z. B. `6h`. `0s` schaltet das aus. Speicher-Regeln brauchen dazu `api_token`, Drucker-Regeln `printer_auto`. |
| `allowed_storages` | `["local"]` | Die Speicher, auf die Regeln zeigen dürfen. `"*"` erlaubt alle. Alles andere wird weder gespeichert noch aufgeräumt, und die Belegung zeigt nur diese. |
| `allowed_devices` | `[]` | Die Drucker (IDs), auf deren eigenem Speicher Drucker-Regeln aufräumen dürfen. `"*"` erlaubt alle, leer keinen. |
| `printer_auto` | `false` | Drucker-Regeln dürfen aktiv sein und laufen alle `auto_interval`. Siehe [Automatisch am Drucker aufräumen](#automatisch-am-drucker-aufräumen). |
| `printer_auto_max` | `50` | Höchstens so viele Dateien je Drucker-Regel und automatischem Lauf, 1 bis 500. |
| `printer_auto_dry_run` | `true` | Der automatische Drucker-Lauf notiert nur, was er löschen würde. |

Berechtigungen des Moduls: `read_storage` und ein eigener Datenordner (`regeln.json`, `verlauf.json`). Auf Druckern hat es nichts, außer die Administration gibt ihm für den automatischen Drucker-Lauf die Operator-Rolle. Dazu fragt es den Server zwei Dinge, die es selbst nie tut (`checks: write_storage, operate`), auch für Personen, die gerade nicht angemeldet sind (`check_absent_people`). Siehe unten.

### Berechtigungen und der API-Token

Prusa Connect Local schützt die Routen des Moduls: Lesen (`GET`) braucht `view_resource` auf die Erweiterung, alles andere `operate_resource`. Der automatische Lauf löscht mit dem API-Token, und der kann mehr dürfen als die Personen, die Regeln anlegen. Zwei Schichten verhindern, dass eine Regel mehr löscht, als ihr Urheber von Hand löschen dürfte:

1. **Die Listen der Administration.** Regeln dürfen nur auf die Speicher in `allowed_storages` und die Drucker in `allowed_devices` zeigen. Ändern kann sie nur, wer die Erweiterung konfigurieren darf.
2. **Die eigenen Rechte des Urhebers.** Speichert jemand eine neue oder geänderte Regel, fragt die Erweiterung den Server, ob diese Person dort löschen darf (`write_storage` für Speicher, `operate` für den Drucker). Sonst wird die Regel abgelehnt. Die Regel merkt sich, wer sie gespeichert hat. Vor jedem automatischen Lauf fragt die Erweiterung erneut für diese Person und lässt die Regel aus, wenn sie dort nicht mehr löschen darf oder der Server keine Antwort gibt. Die Seite zeigt, warum eine Regel ausgelassen wurde.

Eine Regel, die seit 0.3.0 niemand gespeichert hat, etwa die Voreinstellungen oder Regeln aus einer älteren Version, hat noch keinen Urheber. Von Hand funktioniert sie weiter. Automatisch läuft sie aber erst, wenn jemand mit Löschrecht sie einmal gespeichert hat.

Grenzen, die man kennen sollte:

- In Prusa Connect Local v0.1.0-beta.1 gilt `write_storage` für alle Speicher zusammen, nicht je Speicher. Auf einzelne Speicher eingegrenzt wird es durch `allowed_storages`.
- Auf der Seite (Vorschau, Belegung, Ordner) wird mit den Rechten der angemeldeten Person gelesen. Der automatische Lauf liest Speicher mit dem eigenen Recht `read_storage` des Moduls und Drucker nur mit der Operator-Rolle, die die Administration ihm gegeben hat (siehe oben).
- Den Token für ein Konto mit möglichst wenig Rechten anlegen, oder `api_token` leer lassen und nur von Hand aufräumen.

## Installation

```sh
make core
make release
prusa-connect module add github.com/gilbert-1705/pcl-cleanup@0.4.1 -from release/cleanup -approve
```

Ein Update erkennt Prusa Connect Local nur an der Version. Jede Änderung hebt sie deshalb in `module.yaml`, `main.go` (`release`), `openapi.json` und `ui/package.json` an. Nach einem Update den Server einmal neu starten, weil er den Browser-Teil nur beim Start lädt.

### Umstieg von 0.3.x

0.4.0 ist ein Update desselben Moduls. Es fragt zusätzlich `device.command` an (nur für den optionalen automatischen Drucker-Lauf) und hat drei neue Einstellungen, alle mit sicheren Standardwerten: `printer_auto` aus, `printer_auto_max` 50, `printer_auto_dry_run` an. Fragt das Update sie nicht ab, gelten diese Standardwerte.

1. Jede Drucker-Regel einmal bearbeiten: Speicher und Ordner mit den Kopien wählen oder den Abgleich für diese Regel ausschalten. Bis dahin löscht die Regel nichts und sagt das.
2. Nur wenn der automatische Drucker-Lauf gewünscht ist: `printer_auto` einschalten, `auto_interval` setzen, der Erweiterung die Operator-Rolle auf dem Drucker geben (siehe oben), die Regel aktiv schalten und die Probeläufe im Verlauf prüfen, bevor `printer_auto_dry_run` ausgeschaltet wird.

### Umstieg von 0.2.x

0.3.0 ist ein Update desselben Moduls, fragt aber mehr an als 0.2.x: die zwei Prüfungen (`write_storage`, `operate`) und `check_absent_people`. Beim Update darauf achten, dass sie gewährt werden (auf der Kommandozeile: `-approve`). Ohne sie scheitert das Speichern einer Regel mit „the server could not say whether … may delete there“, und der automatische Lauf lässt jede Regel aus. Danach:

1. `allowed_storages` prüfen: Ab Werk steht dort `["local"]`. Jeden Speicher eintragen, den die Regeln nutzen, oder `"*"`.
2. Auf der Seite jede Regel, die automatisch laufen soll, einmal speichern, als Person mit Löschrecht dort. Bis dahin lässt der automatische Lauf sie aus und sagt das.
3. Für Drucker-Regeln die ID des Druckers in `allowed_devices` eintragen.

### Umstieg von 0.1.x

Ab 0.2.0 hat die Erweiterung einen neuen Modulpfad und den Modulnamen `cleanup` statt `aufraeumen`. Für Prusa Connect Local ist sie damit ein neues Modul und kein Update. Die alte Installation läuft so lange weiter, bis sie entfernt wird.

1. Die aktuelle Version wie oben installieren.
2. Wenn die Regeln bleiben sollen: `module-data/aufraeumen/regeln.json` nach `module-data/cleanup/regeln.json` kopieren und den Server neu starten.
3. Die Einstellungen bei der neuen Erweiterung eintragen und die Regeln einmal speichern (siehe oben).
4. Die alte Installation entfernen: `prusa-connect module remove <früherer Modulpfad>`. Ihr Datenordner `module-data/aufraeumen` bleibt liegen und kann danach weg.

Bis Schritt 4 nicht beide automatisch laufen lassen. Wer `core.modules_local` nutzt, trägt dort den neuen Modulpfad ein.

## Selbst bauen

```sh
make core      # Prusa Connect Local v0.1.0-beta.1 nach .core holen, Bibliotheken bauen
make test      # Go-Tests und svelte-check
make release   # release/cleanup/ und release/cleanup-<version>.tar.gz
```

Nötig sind Go 1.26, Node 22, pnpm 11 und git. `ui/pnpm-workspace.yaml` nimmt neue Paketversionen erst nach sieben Tagen an und indirekte Abhängigkeiten nur aus der Registry.

## Aufbau

| Datei | Inhalt |
|---|---|
| `main.go` | Start, Einstellungen, Routen des Moduls |
| `rules.go` | Regeln, Voreinstellung, Prüfung |
| `policy.go` | Listen der Administration und Rechte des Urhebers |
| `plan.go` | Was eine Regel jetzt löschen würde, Speicherbelegung |
| `printer.go` | Drucker-Regeln: den Speicher eines Druckers lesen |
| `copies.go` | Drucker-Regeln: die Kopien auf dem Server, die Ordnerliste |
| `auto_printer.go` | der optionale automatische Lauf für Drucker-Regeln |
| `cleaner.go` | Einstellungen, Verlauf, Löschen über die HTTP-API |
| `ui/` | Seite „Aufräumen“ (Svelte 5, `@prusa/connect-ui`) |

Die Routen liegen unter `/api/v1/modules/com.github.aux.gilbert-1705.pcl-cleanup/`. Die Tabelle steht in der englischen [README.md](README.md#layout).

## Änderungen

- **0.4.1:** Veröffentlichung über den Prusa Plugin Marketplace: `require.core`, englischer Titel und Kurztext, `CHANGELOG.md`, Release-Workflow. `core_url` muss auf diesen Rechner zeigen (127.0.0.1, ::1, localhost), und der Client folgt keinen Weiterleitungen mehr.
- **0.4.0:** Drucker-Regeln behalten die neuesten N (`keep_days: 0`) und löschen ab Werk nur Dateien mit Kopie auf dem Server (gleicher Name und gleiche Größe). Optionaler automatischer Lauf für Drucker-Regeln (`printer_auto`, aus), mit Obergrenze je Lauf und einem Probelauf, der ab Werk an ist.
- **0.3.0:** Drucker-Regeln für den eigenen Speicher eines Druckers (von Hand). Regeln merken sich, wer sie gespeichert hat; Speichern und jeder automatische Lauf prüfen die eigenen Rechte dieser Person. Neue Einstellungen `allowed_storages` und `allowed_devices`. Die Seite liest Speicher mit den Rechten der angemeldeten Person. Der Verlauf nimmt nur Läufe vorhandener Regeln an.
- **0.2.0:** Eigenes Repository, neuer Modulpfad. Im Ordner-Modus zählt ein an seiner Grenze abgeschnittener Scan nicht gelesene Ordner nicht mehr als alt. Die Seite löscht im Speicher des Plans, den sie gelesen hat.

## Sicherheit

Sicherheitslücken bitte nicht als öffentliches Issue melden, sondern wie in [SECURITY.md](SECURITY.md) beschrieben.

Bei jedem Push, jedem Pull Request und einmal pro Woche laufen in CI Gitleaks (Secrets, auch in der Git-Historie), Trivy (bekannte Schwachstellen, Fehlkonfiguration, Secrets) und Semgrep (statische Analyse). Lokal geht dasselbe mit `./scripts/security-review.sh`, und `pre-commit install` prüft vor jedem Commit mit Gitleaks.

## Lizenz

MIT, siehe [LICENSE](LICENSE).

Gebaut auf Prusa Connect Local von Prusa Research a.s. Prusa Connect Local selbst steht unter OCL v1.1 mit Zusätzen, siehe dessen Repository. Dieses Repository enthält keinen Code davon: `make core` holt ihn nach `.core`, das nicht eingecheckt wird. Ein gebautes Release enthält aber Teile davon (das Go-SDK im Binary, `@prusa/connect-ui` im Bündel der Seite).
