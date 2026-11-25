# VBA-JSON (pinned third-party file)

This folder holds an unchanged copy of VBA-JSON by Tim Hall. The license is MIT (see `LICENSE`).

| Item | Value |
|---|---|
| Project | https://github.com/VBA-tools/VBA-JSON |
| Tag | `v2.3.1` |
| File URL | https://raw.githubusercontent.com/VBA-tools/VBA-JSON/v2.3.1/JsonConverter.bas |
| License URL | https://raw.githubusercontent.com/VBA-tools/VBA-JSON/v2.3.1/LICENSE |
| SHA-256 of `JsonConverter.bas` | `1c240aa3c7ef536c25bf44061b02b0fadeb39bfb449f67c419822650e23f6169` |

Rules:

1. Do not edit `JsonConverter.bas`. The rule `third_party/** -text` in `.gitattributes` keeps the bytes unchanged.
2. The tag uses LF line ends. The tool `vb6gen` makes `CalyxJson.bas` from this file and writes CRLF.
3. Check the file with `sha256sum JsonConverter.bas`. The result must match the table.
4. To update the file, change the tag, the URL and the SHA-256 here and in the source of `vb6gen`.
