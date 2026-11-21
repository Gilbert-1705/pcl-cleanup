# pcl-cleanup – Aufräumen für Prusa Connect Local

*Deutsche Übersetzung. Maßgeblich ist die englische [README.md](README.md).*

Eine Erweiterung für [Prusa Connect Local](https://github.com/prusa3d/prusa-connect), den selbst gehosteten Prusa-Connect-Server. Prusa Connect Local löscht von sich aus nichts: Die Kamera legt laufend Snapshots ab, eine Zeitraffer-Erweiterung schreibt pro Druck einen Ordner, und andere Erweiterungen legen eventuell Bilder ab. Diese Erweiterung löscht so etwas nach Regeln je Ordner. Vorher zeigt sie, was weg kann, wie sich der Speicher verteilt und was zuletzt gelöscht wurde.

Stand: 0.2.0, gebaut und getestet gegen Prusa Connect Local **v0.1.0-beta.1**. Die Oberfläche ist deutsch, die Seite heißt „Aufräumen“.

Bis 0.1.1 lag die Erweiterung zusammen mit einer Zeitraffer-Erweiterung in einem gemeinsamen Repository, unter einem anderen Modulpfad. Seit 0.2.0 hat sie ein eigenes Repository. Wer 0.1.x nutzt, findet den [Umstieg von 0.1.x](#umstieg-von-01x) weiter unten.

## Was sie kann

- Eine Seite „Aufräumen“ in der Seitenleiste unter Erweiterungen, mit Regeln, Vorschau je Regel, „Jetzt aufräumen“, Speicherbelegung und Verlauf.
- Jede **Regel** nennt Speicher, Ordner, Modus und Aufbewahrung:
  - **Dateien:** jede Datei einzeln nach Änderungsdatum, optional mit Muster wie `*.jpg`.
  - **Ordner:** ganze Ordner auf einer bestimmten Ebene, gealtert nach der neuesten Datei darin. Ein Zeitraffer-Lauf geht so immer komplett.
  - `keep_newest` schützt die neuesten N Einheiten unabhängig vom Alter.
  - Eine Regel auf die oberste Ebene eines Speichers wird abgelehnt.
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

## Einstellungen

Unter Settings → Extensions → Aufräumen:

| Einstellung | Standard | Bedeutung |
|---|---|---|
| `core_url` | `http://127.0.0.1:8080` | Adresse des Servers aus Sicht des Moduls. Läuft der Server auf einem anderen Port, hier anpassen. |
| `api_token` | leer | API-Zugang mit Umfang `operate` (Einstellungen → API-Zugangsdaten). Wird als Secret gespeichert. |
| `auto_interval` | `0s` | Wie oft die aktiven Regeln automatisch laufen, z. B. `6h`. `0s` schaltet das aus. |

Berechtigungen des Moduls: `read_storage` und ein eigener Datenordner (`regeln.json`, `verlauf.json`).

### Berechtigungen und der API-Token

Prusa Connect Local schützt die Routen des Moduls: Lesen (`GET`) braucht `view_resource` auf die Erweiterung, alles andere `operate_resource`.

Was das bedeutet, sobald ein `api_token` gesetzt ist: **Wer die Erweiterung bedienen darf, kann Regeln für jeden Speicher setzen und einen automatischen Lauf starten. Dieser Lauf löscht mit den Rechten der Person, der der Token gehört.** Auf einem Server mit nur einer Administratorin oder einem Administrator ändert das nichts. Auf einem Server mit mehreren Personen:

- den Token für ein Konto mit möglichst wenig Rechten anlegen, am besten nur auf den Speichern, die aufgeräumt werden sollen,
- `operate` auf die Erweiterung nur Personen geben, die in diesen Speichern ohnehin löschen dürfen,
- oder `api_token` leer lassen und nur von Hand aufräumen.

Die Seite listet Speicher außerdem mit dem eigenen Recht `read_storage` des Moduls. Wer `view_resource` auf die Erweiterung hat, sieht damit Ordnernamen und Größen aller Speicher.

## Installation

```sh
make core
make release
prusa-connect module add github.com/gilbert-1705/pcl-cleanup@0.2.0 -from release/cleanup -approve
```

Ein Update erkennt Prusa Connect Local nur an der Version. Jede Änderung hebt sie deshalb in `module.yaml`, `main.go` (`release`), `openapi.json` und `ui/package.json` an. Nach einem Update den Server einmal neu starten, weil er den Browser-Teil nur beim Start lädt.

### Umstieg von 0.1.x

Ab 0.2.0 hat die Erweiterung einen neuen Modulpfad und den Modulnamen `cleanup` statt `aufraeumen`. Für Prusa Connect Local ist sie damit ein neues Modul und kein Update. Die alte Installation läuft so lange weiter, bis sie entfernt wird.

1. 0.2.0 wie oben installieren.
2. Wenn die Regeln bleiben sollen: `module-data/aufraeumen/regeln.json` nach `module-data/cleanup/regeln.json` kopieren und den Server neu starten.
3. `core_url`, `api_token` und `auto_interval` bei der neuen Erweiterung eintragen.
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
| `plan.go` | Was eine Regel jetzt löschen würde, Speicherbelegung |
| `cleaner.go` | Automatischer Lauf, Löschen über die HTTP-API |
| `ui/` | Seite „Aufräumen“ (Svelte 5, `@prusa/connect-ui`) |

Die Routen liegen unter `/api/v1/modules/com.github.aux.gilbert-1705.pcl-cleanup/`. Die Tabelle steht in der englischen [README.md](README.md#layout).

## Sicherheit

Sicherheitslücken bitte nicht als öffentliches Issue melden, sondern wie in [SECURITY.md](SECURITY.md) beschrieben.

Bei jedem Push, jedem Pull Request und einmal pro Woche laufen in CI Gitleaks (Secrets, auch in der Git-Historie), Trivy (bekannte Schwachstellen, Fehlkonfiguration, Secrets) und Semgrep (statische Analyse). Lokal geht dasselbe mit `./scripts/security-review.sh`, und `pre-commit install` prüft vor jedem Commit mit Gitleaks.

## Lizenz

MIT, siehe [LICENSE](LICENSE).

Gebaut auf Prusa Connect Local von Prusa Research a.s. Prusa Connect Local selbst steht unter OCL v1.1 mit Zusätzen, siehe dessen Repository. Dieses Repository enthält keinen Code davon: `make core` holt ihn nach `.core`, das nicht eingecheckt wird. Ein gebautes Release enthält aber Teile davon (das Go-SDK im Binary, `@prusa/connect-ui` im Bündel der Seite).
