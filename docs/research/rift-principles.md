# Rift: abgeleitete Gestaltungsprinzipien

**Stand:** 3. November 2026. Ausschließlich Primärquellen zu Rift aus
[`anomalyco/rift`@`35ada03`](https://github.com/anomalyco/rift/tree/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9).
Rift ist ausdrücklich frühe Software; Schnittstellen und Speicher-Details können sich ändern.

## CLI-UX

- **Pfad als skriptbare Hauptausgabe.** `create`, `list`, `ancestors` und `gc`
  schreiben Pfade nach stdout; Fortschritt, Hinweise und Hook-Ausgabe gehen nach
  stderr. Dadurch bleiben Befehle für Shells und Agenten komponierbar.
  Die optionale Bash/Zsh/Nushell-Integration wechselt nach `init`, `create` und
  `remove` in das passende Verzeichnis. [README](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/README.md), [CLI-Quelle](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/crates/cli/src/main.rs)
- **Sinnvolle, aber übersteuerbare Ortswahl.** Ohne Pfad arbeitet die CLI vom
  aktuellen Verzeichnis, sucht beim Initialisieren den bestehenden Rift- bzw.
  Git-Root und bietet `--here` für die exakte Auswahl. Das ist ein implizites
  Prinzip: sichere Konvention als Standard, Präzision als Opt-in.
  [Spezifikation](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/specs.md)
- **Erklärende Fehler statt stiller Reparatur.** Nicht initialisierte oder
  markerlose Workspaces liefern konkrete nächste Befehle; das Abmelden eines
  Roots verlangt bewusst `--force`. [CLI-Quelle](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/crates/cli/src/main.rs)

## Konfiguration und Hooks

- **Kleine, lokale, versionierte Hook-Konfiguration.** `.rift.toml` hat eine
  obligatorische Version und nur die vier bekannten Hook-Gruppen; unbekannte
  Felder, andere Versionen und leere Befehle werden abgelehnt. Das verhindert
  still ignorierte Tippfehler. [Konfigurationsparser](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/crates/core/src/config.rs)
- **Lifecycle mit klaren Commit-Grenzen.** `precreate`/`preremove` verhindern
  die Operation bei Fehlern; `postcreate`/`postremove` melden Fehler, nachdem
  der jeweilige Zustand bereits dauerhaft ist. Hooks sind sequenziell, erhalten
  Quelle, Ziel sowie Rift- und Parent-ID und können mit `--no-hooks` bzw. der
  Plugin-Option deaktiviert werden. [README](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/README.md), [Hook-Ausführung](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/crates/core/src/hook.rs)
- **Sicherheitsgrenze: Hooks sind vertrauenswürdiger Projektcode.** Sie werden
  lokal via `sh -c` (bzw. `cmd /C`) mit geerbter Umgebung ausgeführt, nicht
  sandboxed. Wer einen fremden Workspace snapshotten oder entfernen lässt,
  autorisiert damit dessen `.rift.toml`; `--no-hooks` ist die explizite
  Ausweichmöglichkeit. *(Implizit aus der Implementierung.)* [Hook-Quelle](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/crates/core/src/hook.rs)
- **Integration hat eine eng validierte Konfigurationsoberfläche.** Das
  OpenCode-Plugin akzeptiert nur `executable`, `copyAll`, `hooks` und optional
  `database`, typprüft sie und lehnt unbekannte Optionen ab. [Plugin-Strategie](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/plugins/opencode/src/strategy.ts)

## Zustand, Speicherung und Sicherheit

- **Identität ist stabil, Pfad ist nicht.** Jeder Workspace trägt eine `.rift`
  mit ULID und ist zentral in SQLite mit `parent_id`, aktuellem Pfad und
  Zeitstempel registriert. Das Modell ist ein Provenienzbaum, keine lose
  Verzeichnisliste. [Spezifikation](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/specs.md)
- **Nebenläufigkeit gehört zum Kernmodell.** SQLite wird mit WAL,
  Fremdschlüsseln und Busy-Timeout geöffnet; die Spezifikation begründet dies
  ausdrücklich mit parallelen Prozessen und Agenten. [Registry-Quelle](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/crates/core/src/registry.rs), [Spezifikation](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/specs.md)
- **Löschen ist zweistufig und baumkonsistent.** Entfernen verschiebt zuerst
  den gesamten Nachfahrenbaum tiefen-zuerst in Rift-eigenen `.trash`-Speicher;
  erst `gc` löscht physisch. Fehlende oder nicht passende Marker verhindern
  riskante Entfernaktionen; Roots bleiben beim Deregistrieren erhalten.
  [README](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/README.md), [Spezifikation](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/specs.md)
- **Sichere Kopie vor universellem Fallback.** Rift nutzt nur unterstützte
  Copy-on-write-Mechanismen (btrfs/Reflink/APFS); bei fehlender Strategie
  schlägt `create` fehl statt Byte-Kopien vorzutäuschen. Filterkopien lassen
  regenerierbare Artefakte aus, `--copy-all` ist der bewusste Exaktheitsmodus.
  [Spezifikation](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/specs.md)
- **Git-Zustand wird bewahrt, gefährliche Zustände werden abgelehnt.** Kopien
  erhalten Index, uncommittete, unversionierte und passende ignorierte Dateien;
  der neue `HEAD` wird losgelöst. Linked Worktrees sowie Merge-, Rebase-,
  Cherry-pick-, Revert-, Bisect- und Lock-Zustände werden nicht kopiert.
  [README](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/README.md)

## Agenten- und OpenCode-Prinzipien

- **Isolation aus realem Arbeitszustand.** Agenten erhalten einen eigenen
  Snapshot des aktuellen Workspaces, nicht bloß einen Commit-Checkout. Das
  erhält lokale Arbeit und ermöglicht parallele Aufgaben. [README](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/README.md)
- **Kleine, strukturierte Agenten-API.** Das Plugin exponiert `rift.create`,
  `rift.list` und `rift.remove` als Code-Mode-Tools mit strukturierten Ein-/
  Ausgaben; der Session-Wechsel bleibt eine separate OpenCode-Operation. Damit
  trennt es Workspace-Lebenszyklus von Agenten-Navigation.
  [Tool-Quelle](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/plugins/opencode/src/tools.ts)
- **Agentensicherheit vor Bequemlichkeit.** Das Plugin verweigert das Löschen
  des Workspaces der aktuellen Session und eines Workspaces mit Kindern,
  unterstützt keine Start-Refs und wandelt bereits committed Hook-Fehler in
  sichtbare Warnungen statt einen nicht existierenden Rollback zu behaupten.
  [Plugin-README](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/plugins/opencode/README.md), [Plugin-Strategie](https://github.com/anomalyco/rift/blob/35ada0360e61fdadbba94bd0f10bc9b3365c0ae9/plugins/opencode/src/strategy.ts)

## Entscheidungen für DevProxy

Die folgenden Empfehlungen sind Ableitungen, keine Aussagen über bereits
implementiertes Verhalten. Maßgeblich bleiben der Laufzeitvertrag im
[README](../../README.md) und die Zuständigkeitsgrenzen in
[AGENTS.md](../../AGENTS.md): DevProxy verwaltet lokale HTTPS-Routen und
Service-Prozesse; Caddy übernimmt TLS und Reverse Proxying. Workspace-Verwaltung
bleibt bei Rift bzw. Git, Aufgabenverwaltung und Agentenkommunikation bei Kata.

| Prinzip aus den Abschnitten oben | Empfehlung für DevProxy |
| --- | --- |
| Skriptbare Hauptausgabe | **Übernehmen:** strukturierte Bereitschaftsergebnisse statt Pfaden; Diagnostik nach stderr, Kindprozess-stdout erhalten. Registrierung ausdrücklich nicht als HTTP-Gesundheit ausgeben. Siehe #11. |
| Übersteuerbare Ortswahl | **Übernehmen:** Projektkonfiguration vom Git-Worktree-Root finden; explizite Konfigurationsauswahl und dokumentierte Präzedenz erhalten. Keine `.rift`-Abhängigkeit. Siehe #13. |
| Erklärende Fehler | **Übernehmen:** bei fehlender oder mehrdeutiger Service-Auswahl passende nächste Befehle und eindeutige Selektoren nennen; nicht stillschweigend einen Service wählen. Siehe #14. |
| Versionierte Hook-Konfiguration | **Teilweise übernehmen:** kleine, versionierte und strikt validierte Projektkonfiguration, aber keine Rift-Hook-Gruppen kopieren. Migration bestehender YAML-Dateien ausdrücklich regeln. Siehe #15. |
| Lifecycle mit Commit-Grenzen | **Prinzip übernehmen, Hooks nicht einführen:** fehlgeschlagenen Start, erfolgte Registrierung und spätere Fehler unterscheidbar melden; keinen Rollback behaupten, der nicht stattgefunden hat. Bereitschaftsvertrag in #11. |
| Hooks als vertrauenswürdiger Projektcode | **Nicht übernehmen:** keine automatische Ausführung von Shell-Code aus Projektkonfiguration. Explizite `run -- …`-Kindprozesse bleiben ein anderer, bewusst angeforderter Vorgang. |
| Eng validierte Plugin-Optionen | **Validierungsprinzip übernehmen:** unbekannte Schlüssel und falsche Typen ablehnen (#15); kein OpenCode-Plugin und keine Rift-spezifischen Optionen hinzufügen. |
| Stabile Identität statt Pfad | **Teilweise übernehmen:** Registrierungs-ID und Service-Identität in Ergebnissen explizit machen (#11, #14). Keine `.rift`-Marker, ULID-Pflicht oder Workspace-Provenienzverwaltung. |
| Nebenläufigkeit im Kernmodell | **Prinzip beibehalten:** parallele Clients und Lease-Erneuerung über denselben Daemon koordinieren. Aus Rift folgt kein Bedarf für SQLite/WAL oder einen zweiten Zustandsspeicher. |
| Zweistufiges, baumkonsistentes Löschen | **Nicht übernehmen:** kein Workspace-Löschen, Trash oder `gc`; Deregistrierung entfernt eine Route, nicht das Projektverzeichnis. |
| Sichere Copy-on-write-Kopie | **Nicht übernehmen:** Snapshot-Backends, Filterkopien und Byte-Kopie-Fallbacks gehören nicht zu einem HTTPS-Router. |
| Git-Zustand bewahren | **Zuständigkeitsgrenze übernehmen:** Git-Zustand nicht verändern; keine Snapshot- oder Detached-HEAD-Logik und kein Verbot von Linked Worktrees, die DevProxy gerade unterstützen soll. |
| Isolation aus realem Arbeitszustand | **Interoperabilität statt Implementierung:** Services in vorhandenen Worktrees oder Snapshots betreiben, diese aber nicht selbst erzeugen. |
| Kleine strukturierte Agenten-API | **Prinzip übernehmen:** CLI und Browser verwenden denselben Daemon-Zustand; maschinenlesbare CLI-Ergebnisse priorisieren (#11). Session-Navigation und Workspace-Tools nicht in den Daemon verlagern. |
| Agentensicherheit vor Bequemlichkeit | **Prinzip übernehmen:** eindeutige Service-Auswahl (#14) und ehrliche Fehler-/Bereitschaftszustände (#11); Rift-spezifische Löschsperren und Start-Ref-Regeln nicht kopieren. |

## Priorisierte, unabhängig prüfbare Folgearbeit

Die Nummern bezeichnen bestehende GitHub-Issues; diese Notiz legt keine neue
Implementierung und keine zusätzliche Abhängigkeit zwischen den Issues fest.

1. **P1 — Ausgabe und Auswahl:**
   [#11: maschinenlesbares Bereitschaftsergebnis](https://github.com/vietz-dev/devproxy/issues/11)
   (Kata `xbfc`): versionierter JSON-Vertrag mit Registrierungs-ID, Service,
   URL, Route und Port; Erfolg und Fehler testen, Kindprozess-Ausgabe bewahren.
   [#14: handlungsleitende Service-Auswahlfehler](https://github.com/vietz-dev/devproxy/issues/14)
   (Kata `p52v`): fehlende und mehrdeutige Treffer getrennt testen; Kandidaten
   und ausführbare Korrekturbefehle ausgeben.
2. **P2 — Konfiguration:**
   [#13: Konfiguration vom Worktree-Root finden](https://github.com/vietz-dev/devproxy/issues/13)
   (Kata `74m3`): Unterverzeichnisse und Linked Worktrees testen, explizite
   Pfade und Präzedenz erhalten.
   [#15: versioniertes Projektschema validieren](https://github.com/vietz-dev/devproxy/issues/15)
   (Kata `x33p`): unbekannte Schlüssel, falsche Typen und nicht unterstützte
   Versionen vor Service-Operationen ablehnen; Dateipfad und Migration erklären.

**Bewusst ohne Implementierungsauftrag:** Hook-System, Workspace-Registry,
Snapshots, Trash/GC, Session-Steuerung und neue Datenbank. Dafür gibt es hier
keinen belegten DevProxy-Bedarf. Das bleibt eine Nicht-Adoptionsentscheidung,
keine ungetrackte Feature-Zusage.
