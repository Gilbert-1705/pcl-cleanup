# pcl-cleanup – Aufräumen für Prusa Connect Local

*Deutsche Übersetzung. Maßgeblich ist die englische [README.md](README.md).*

Eine Erweiterung für [Prusa Connect Local](https://github.com/prusa3d/prusa-connect), den selbst gehosteten Prusa-Connect-Server. Prusa Connect Local löscht von sich aus nichts: Die Kamera legt laufend Snapshots ab, eine Zeitraffer-Erweiterung schreibt pro Druck einen Ordner, und andere Erweiterungen legen eventuell Bilder ab. Diese Erweiterung löscht so etwas nach Regeln je Ordner. Vorher zeigt sie, was weg kann, wie sich der Speicher verteilt und was zuletzt gelöscht wurde.

Stand: 0.3.0, gebaut und getestet gegen Prusa Connect Local **v0.1.0-beta.1**. Die Oberfläche ist deutsch, die Seite heißt „Aufräumen“.

Bis 0.1.1 lag die Erweiterung zusammen mit einer Zeitraffer-Erweiterung in einem gemeinsamen Repository, unter einem anderen Modulpfad. Seit 0.2.0 hat sie ein eigenes Repository. Wer 0.1.x nutzt, findet den [Umstieg von 0.1.x](#umstieg-von-01x) weiter unten.

## Was sie kann

- Eine Seite „Aufräumen“ in der Seitenleiste unter Erweiterungen, mit Regeln, Vorschau je Regel, „Jetzt aufräumen“, Speicherbelegung und Verlauf.
- Jede **Regel** nennt Speicher, Ordner, Modus und Aufbewahrung:
  - **Dateien:** jede Datei einzeln nach Änderungsdatum, optional mit Muster wie `*.jpg`.
  - **Ordner:** ganze Ordner auf einer bestimmten Ebene, gealtert nach der neuesten Datei darin. Ein Zeitraffer-Lauf geht so immer komplett.
  - `keep_newest` schützt die neuesten N Einheiten unabhängig vom Alter.
  - Eine Regel auf die oberste Ebene eines Speichers wird abgelehnt.
- **Drucker-Regeln** (seit 0.3.0) räumen alte Druckdateien vom eigenen Speicher eines Druckers, etwa seinem USB-Stick. Siehe [Speicher eines Druckers](#speicher-eines-druckers).
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
- **Automatisch:** Der Go-Prozess ruft die HTTP-API des eigenen Servers auf (`POST /api/v1/storages/{storage}/deletions`). Dafür braucht er die Adresse des Servers (`core_url`) und einen API-Zugang mit dem Umfang `operate` (`api_token`). **Ohne Token wird nie automatisch gelöscht.**

Löschen ist endgültig. Vor dem ersten Lauf lohnt sich ein Blick in die Vorschau jeder Regel.

## Speicher eines Druckers

Eine Drucker-Regel nennt einen Drucker, einen Ordner auf seinem Speicher (zum Beispiel `usb`) und ein Muster für Druckdateien (`*.bgcode` oder `*.gcode`). Sie ist bewusst eng gefasst:

- **Nur von Hand.** Drucker-Regeln laufen nie automatisch. Die Seite liest erst den Drucker („Drucker lesen“), zeigt dann, was weg kann, und löscht nach deiner Bestätigung.
- **Mit deinen Rechten.** Gelesen und gelöscht wird für die angemeldete Person, die dafür `operate` auf den Drucker braucht. Die Erweiterung selbst hat kein Recht auf irgendeinen Drucker.
- **Nur Druckdateien.** Das Muster muss auf `.bgcode` oder `.gcode` enden. Ordner, schreibgeschützte Dateien und Dateien, für die der Drucker kein Datum meldet, werden nie angeboten.
- **Nie während eines Drucks.** Läuft ein Druck, wird nichts gelesen und nichts gelöscht. Die Datei des laufenden und des letzten Drucks wird nie angeboten.
- **Ein Ordner.** Gelesen wird nur der genannte Ordner, nicht die Ordner darunter. Ein Drucker mit Buddy-Firmware beantwortet einen Befehl nach dem anderen und braucht für etwa 300 Dateien 12 bis 15 Sekunden. Das Lesen dauert also etwas und passiert nur auf Knopfdruck.
- **Nur freigegebene Drucker.** Der Drucker muss in `allowed_devices` stehen. Ab Werk ist die Liste leer, Drucker-Regeln sind also aus, bis jemand einen Drucker einträgt.

Änderungen am Display des Druckers sieht die Erweiterung beim nächsten Lesen.

## Einstellungen

Unter Settings → Extensions → Aufräumen:

| Einstellung | Standard | Bedeutung |
|---|---|---|
| `core_url` | `http://127.0.0.1:8080` | Adresse des Servers aus Sicht des Moduls. Läuft der Server auf einem anderen Port, hier anpassen. |
| `api_token` | leer | API-Zugang mit Umfang `operate` (Einstellungen → API-Zugangsdaten). Wird als Secret gespeichert. |
| `auto_interval` | `0s` | Wie oft die aktiven Regeln automatisch laufen, z. B. `6h`. `0s` schaltet das aus. |
| `allowed_storages` | `["local"]` | Die Speicher, auf die Regeln zeigen dürfen. `"*"` erlaubt alle. Alles andere wird weder gespeichert noch aufgeräumt, und die Belegung zeigt nur diese. |
| `allowed_devices` | `[]` | Die Drucker (IDs), auf deren eigenem Speicher Drucker-Regeln von Hand aufräumen dürfen. `"*"` erlaubt alle, leer keinen. |

Berechtigungen des Moduls: `read_storage` und ein eigener Datenordner (`regeln.json`, `verlauf.json`). Dazu fragt es den Server zwei Dinge, die es selbst nie tut (`checks: write_storage, operate`), auch für Personen, die gerade nicht angemeldet sind (`check_absent_people`). Siehe unten.

### Berechtigungen und der API-Token

Prusa Connect Local schützt die Routen des Moduls: Lesen (`GET`) braucht `view_resource` auf die Erweiterung, alles andere `operate_resource`. Der automatische Lauf löscht mit dem API-Token, und der kann mehr dürfen als die Personen, die Regeln anlegen. Zwei Schichten verhindern, dass eine Regel mehr löscht, als ihr Urheber von Hand löschen dürfte:

1. **Die Listen der Administration.** Regeln dürfen nur auf die Speicher in `allowed_storages` und die Drucker in `allowed_devices` zeigen. Ändern kann sie nur, wer die Erweiterung konfigurieren darf.
2. **Die eigenen Rechte des Urhebers.** Speichert jemand eine neue oder geänderte Regel, fragt die Erweiterung den Server, ob diese Person dort löschen darf (`write_storage` für Speicher, `operate` für den Drucker). Sonst wird die Regel abgelehnt. Die Regel merkt sich, wer sie gespeichert hat. Vor jedem automatischen Lauf fragt die Erweiterung erneut für diese Person und lässt die Regel aus, wenn sie dort nicht mehr löschen darf oder der Server keine Antwort gibt. Die Seite zeigt, warum eine Regel ausgelassen wurde.

Eine Regel, die seit 0.3.0 niemand gespeichert hat, etwa die Voreinstellungen oder Regeln aus einer älteren Version, hat noch keinen Urheber. Von Hand funktioniert sie weiter. Automatisch läuft sie aber erst, wenn jemand mit Löschrecht sie einmal gespeichert hat.

Grenzen, die man kennen sollte:

- In Prusa Connect Local v0.1.0-beta.1 gilt `write_storage` für alle Speicher zusammen, nicht je Speicher. Auf einzelne Speicher eingegrenzt wird es durch `allowed_storages`.
- Auf der Seite (Vorschau, Belegung) wird mit den Rechten der angemeldeten Person gelesen. Der automatische Lauf liest mit dem eigenen Recht `read_storage` des Moduls.
- Den Token für ein Konto mit möglichst wenig Rechten anlegen, oder `api_token` leer lassen und nur von Hand aufräumen.

## Installation

```sh
make core
make release
prusa-connect module add github.com/gilbert-1705/pcl-cleanup@0.3.0 -from release/cleanup -approve
```

Ein Update erkennt Prusa Connect Local nur an der Version. Jede Änderung hebt sie deshalb in `module.yaml`, `main.go` (`release`), `openapi.json` und `ui/package.json` an. Nach einem Update den Server einmal neu starten, weil er den Browser-Teil nur beim Start lädt.

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
| `cleaner.go` | Einstellungen, Verlauf, Löschen über die HTTP-API |
| `ui/` | Seite „Aufräumen“ (Svelte 5, `@prusa/connect-ui`) |

Die Routen liegen unter `/api/v1/modules/com.github.aux.gilbert-1705.pcl-cleanup/`. Die Tabelle steht in der englischen [README.md](README.md#layout).

## Änderungen

- **0.3.0:** Drucker-Regeln für den eigenen Speicher eines Druckers (von Hand). Regeln merken sich, wer sie gespeichert hat; Speichern und jeder automatische Lauf prüfen die eigenen Rechte dieser Person. Neue Einstellungen `allowed_storages` und `allowed_devices`. Die Seite liest Speicher mit den Rechten der angemeldeten Person. Der Verlauf nimmt nur Läufe vorhandener Regeln an.
- **0.2.0:** Eigenes Repository, neuer Modulpfad. Im Ordner-Modus zählt ein an seiner Grenze abgeschnittener Scan nicht gelesene Ordner nicht mehr als alt. Die Seite löscht im Speicher des Plans, den sie gelesen hat.

## Sicherheit

Sicherheitslücken bitte nicht als öffentliches Issue melden, sondern wie in [SECURITY.md](SECURITY.md) beschrieben.

Bei jedem Push, jedem Pull Request und einmal pro Woche laufen in CI Gitleaks (Secrets, auch in der Git-Historie), Trivy (bekannte Schwachstellen, Fehlkonfiguration, Secrets) und Semgrep (statische Analyse). Lokal geht dasselbe mit `./scripts/security-review.sh`, und `pre-commit install` prüft vor jedem Commit mit Gitleaks.

## Lizenz

MIT, siehe [LICENSE](LICENSE).

Gebaut auf Prusa Connect Local von Prusa Research a.s. Prusa Connect Local selbst steht unter OCL v1.1 mit Zusätzen, siehe dessen Repository. Dieses Repository enthält keinen Code davon: `make core` holt ihn nach `.core`, das nicht eingecheckt wird. Ein gebautes Release enthält aber Teile davon (das Go-SDK im Binary, `@prusa/connect-ui` im Bündel der Seite).
