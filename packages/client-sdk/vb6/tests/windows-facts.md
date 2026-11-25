# Windows facts for the VB6 SDK

The VB6 SDK uses some WinHTTP and VB6 facts that come from general knowledge, not
from a test. This file tells the author how to check them on Windows, and holds the results.

Status (2026-11-25): the author ran the test suite and the examples on Windows, and everything
passed. The author did not run the probe or the steps below. Each fact has a verdict in "Results".
A fact that a test case exercises is confirmed by the passing run. A fact that no test case
exercises is "not checked on its own; the general run passed". The steps stay for the author who
wants to check such a fact.

The probe program is `tests/probe/Probe.vbp`. It writes one line for each result to
`probe-results.txt`, next to `Probe.exe`. Git ignores that file.

## The facts

| Fact | What to check | Machine | Steps | Result |
|---|---|---|---|---|
| F1 | The default receive time limit of `WinHttpRequest` (30 s) | Windows 10 | 16 to 25 | R1 |
| F2 | The form of a WinHTTP error number in `Err.Number` (an HRESULT such as `&H80072EE2`, or 12002) | Windows 10 | 16 to 25 | R2 |
| F3 | The form of a WinHTTP error number in `OnError` | Windows 10 | 16 to 25 | R3 |
| F4 | An asynchronous time-out arrives as an `OnError` event | Windows 10 | 16 to 25 | R4 |
| F5 | The TLS 1.2 option `Option(9)` on Windows 7 SP1, with and without KB3140245 | Windows 10 and Windows 7 SP1 | 23, 26 to 28, 30 to 39, 43 to 52 | R5 |
| F6 | The error numbers of a TLS failure and of a certificate failure | Windows 10 and Windows 7 SP1 | 29, 39 | R6 |
| F7 | VBA-JSON v2.3.1 (`src/CalyxJson.bas`) loads in VB6 with no change other than the prefix | Windows 10 | 11 to 15, 24 | R7 |
| F8 | The exact `Reference=` lines that the VB6 IDE writes for WinHTTP 5.1 and Scripting Runtime | Windows 10 | 3 to 9 | R8 |
| F9 | Go 1.22 and later binaries do not start on Windows 7 | Windows 7 SP1 | 40 to 42 | R9 |

## Safety

- The probe sends no WhatsApp message. It calls `127.0.0.1` and the one HTTPS URL that you give
  on the command line.
- Use only the route `/healthz` of your backend for the TLS steps. This route needs no API key
  and reads only. Do not give an API key to the probe.
- Do not copy the host name or the IP address of your backend into this file. Write `<backend>`
  in its place, because this file goes into Git.

## Steps

Each step starts with the machine where you do it.

### Prepare (Linux)

1. **[Linux]** Run `pnpm --filter @calyx/client-sdk-vb6 fake:windows`. It writes
   `packages/client-sdk/vb6/tests/fakebackend.exe`.
2. **[Linux]** Copy the folder `packages/client-sdk/vb6` to the Windows 10 machine. Keep the
   folder layout, because `Probe.vbp` loads `..\..\src\CalyxJson.bas`.

### Add the references (Windows 10)

3. **[Windows 10]** Open `tests\probe\Probe.vbp` in the VB6 IDE.
4. **[Windows 10]** Click Project > References.
5. **[Windows 10]** Select "Microsoft WinHTTP Services, version 5.1".
6. **[Windows 10]** Select "Microsoft Scripting Runtime", and click OK.
7. **[Windows 10]** Click File > Save Project.
8. **[Windows 10]** Open `Probe.vbp` in Notepad.
9. **[Windows 10]** Copy each line that starts with `Reference=` into R8. Every `.vbp` of the SDK
   uses these lines.

### Build the probe (Windows 10)

10. **[Windows 10]** Run `reg query "HKLM\SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2\Client"` and `netsh winhttp show proxy`. Write both outputs into "The run" below, line "Windows 10: SCHANNEL and proxy".
11. **[Windows 10]** Click File > Make Probe.exe, and save `Probe.exe` in `tests\probe`.
12. **[Windows 10]** If the build ends with no error, write "builds" in R7 and go to step 16.
13. **[Windows 10]** Write the error text and the line of `CalyxJson.bas` into R7.
14. **[Windows 10]** Remove the module `CalyxJson` from the project (Project > Remove CalyxJson).
15. **[Windows 10]** In `ProbeMain.bas`, change `#Const PROBE_JSON = 1` to
    `#Const PROBE_JSON = 0`, and do step 11 again.

### The local facts F1 to F4 and F7 (Windows 10)

Caution: do not give `fakebackend.exe` an address other than `127.0.0.1`. It accepts the key
`test-key`, so another computer on the network could use it.

16. **[Windows 10]** Open a Command Prompt in the folder `tests`.
17. **[Windows 10]** Run `fakebackend.exe`. It writes "fakebackend listens on 127.0.0.1:8080".
18. **[Windows 10]** Open a second Command Prompt, and run the command below. It starts a
    listener on `127.0.0.1:8081` that accepts a connection and never answers.

    ```bat
    powershell -NoProfile -Command "$l = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 8081); $l.Start(); Read-Host 'The silent listener runs. Press Enter to stop it'; $l.Stop()"
    ```

19. **[Windows 10]** Open a third Command Prompt in the folder `tests\probe`.
20. **[Windows 10]** Run `Probe.exe local`. The run takes about 45 seconds.
21. **[Windows 10]** Wait for the message box "The probe ended", and click OK.
22. **[Windows 10]** Open `probe-results.txt`.
23. **[Windows 10]** Copy the lines of each part into its result:
    - "sync: default time limits, silent listener" goes into R1. If F1 is true, the error comes
      after about 30 seconds.
    - "sync: closed port" goes into R2.
    - "async: closed port" goes into R3.
    - "sync: receive time limit 5 s, fakebackend scenario 5 (control)" goes into R4, line
      "control". If the call fails after about 5 seconds, `SetTimeouts` works.
    - "async: receive time limit 5 s, fakebackend scenario 5" goes into R4. If F4 is true, the
      line has `OnError` after about 5 seconds. An event after about 20 seconds comes from the
      fake backend, which closes the connection at that time. If the control call failed after
      about 5 seconds and the async result shows 20 seconds, `OnError` does not carry the
      time-out.
    - "Option(9) on a request that is not sent" goes into R5, block "Windows 10".
24. **[Windows 10]** Copy the lines of the part "VBA-JSON" into R7.
25. **[Windows 10]** Press Enter in the second Command Prompt, and press Ctrl+C in the first.
    This stops the silent listener and `fakebackend.exe`.

### The TLS facts on Windows 10

26. **[Windows 10]** Find the HTTPS URL of the route `/healthz` of your backend, for example
    `https://<backend>/healthz`.
27. **[Windows 10]** In the folder `tests\probe`, run `Probe.exe tls https://<backend>/healthz`.
28. **[Windows 10]** Copy the lines "sync: default protocols" and "sync: Option(9) = TLS 1.2"
    into R5, block "Windows 10".
29. **[Windows 10]** Run `Probe.exe tls https://<IP address of the backend server>/healthz`, and
    copy the error lines into R6, block "certificate failure". The certificate does not have the
    IP address, so the call fails.

### The TLS facts and the Go binary on Windows 7 SP1

30. **[Windows 10]** Copy `tests\probe\Probe.exe` and `tests\fakebackend.exe` to one folder on
    the Windows 7 SP1 machine.
31. **[Windows 7 SP1]** Open a Command Prompt in that folder.
32. **[Windows 7 SP1]** Run `wmic qfe get hotfixid | find "KB3140245"`. Write the output, or "not
    installed", into R5, line "KB3140245 at the start".
33. **[Windows 7 SP1]** Run
    `reg query "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp" /v DefaultSecureProtocols`.
    Write the output into R5, line "registry at the start".
34. **[Windows 7 SP1]** On a 64-bit Windows 7, run
    `reg query "HKLM\SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp" /v DefaultSecureProtocols`.
    Write the output into the same line.
35. **[Windows 7 SP1]** Run `reg query "HKLM\SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2\Client"` and `netsh winhttp show proxy`. Write both outputs into "The run" below, line "Windows 7 SP1: SCHANNEL and proxy".
36. **[Windows 7 SP1]** If KB3140245 is installed, write "KB3140245 was installed at the start;
    not measured" into the block "Windows 7 SP1 without KB3140245" of R5 and into the block "TLS
    failure" of R6, and go to step 40.
37. **[Windows 7 SP1]** Run `Probe.exe tls https://<backend>/healthz`.
38. **[Windows 7 SP1]** Copy all lines of the run into R5, block "Windows 7 SP1 without
    KB3140245".
39. **[Windows 7 SP1]** Copy the error lines of the run into R6, block "TLS failure".
40. **[Windows 7 SP1]** Run `fakebackend.exe`.
41. **[Windows 7 SP1]** Write the result into R9: "starts" if it writes "fakebackend listens on
    127.0.0.1:8080", or the error text.
42. **[Windows 7 SP1]** If `fakebackend.exe` runs, press Ctrl+C.

Caution: steps 43, 46 and 47 change the TLS settings of Windows 7 for every program that uses
WinHTTP. Do them only on the test machine.

43. **[Windows 7 SP1]** If KB3140245 is not installed, install it. Get the package for your
    Windows 7 edition (x86 or x64) from the Microsoft Update Catalog.
44. **[Windows 7 SP1]** Restart Windows, and open a Command Prompt in the folder of step 30.
45. **[Windows 7 SP1]** Run `Probe.exe tls https://<backend>/healthz`, and copy all lines of the
    run into R5, block "Windows 7 SP1 with KB3140245, no registry value".
46. **[Windows 7 SP1]** Run
    `reg add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp" /v DefaultSecureProtocols /t REG_DWORD /d 0x00000A00 /f`.
    The value `0xA00` turns on TLS 1.1 and TLS 1.2.
47. **[Windows 7 SP1]** On a 64-bit Windows 7, run
    `reg add "HKLM\SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp" /v DefaultSecureProtocols /t REG_DWORD /d 0x00000A00 /f`.
    VB6 programs are 32-bit, so they read this path.
48. **[Windows 7 SP1]** Restart Windows, and open a Command Prompt in the folder of step 30.
49. **[Windows 7 SP1]** Run `Probe.exe tls https://<backend>/healthz`, and copy all lines of the
    run into R5, block "Windows 7 SP1 with KB3140245 and the registry value".
50. **[Windows 7 SP1]** Look at the block of step 45. If "Option(9) = TLS 1.2" gives status 200
    there, write "the SDK needs no registry value with KB3140245" into R5, line "conclusion".
51. **[Windows 7 SP1]** Else, write "the SDK needs the registry value" into the same line.
52. **[Windows 7 SP1]** Keep or remove the registry values, as the machine needs.

### Finish

53. **[Any]** Fill in "The run" below.
54. **[Any]** Compare each result with its expected value.
55. **[Any]** If a fact is false, change the SDK and this file.

## Results

The expected values come from general knowledge. The probe did not run, so no section has probe
lines. The verdicts come from the run of the test suite on Windows on 2026-11-25. The group
numbers are the cases of `RunGroup` in `tests/TestCases.bas`.

| Fact | Verdict (2026-11-25) | Covering test |
|---|---|---|
| F1 | Not checked on its own; the general run passed | None. The SDK sets its own time limits, and no test reads the default |
| F2 | Confirmed for the time-out | Groups 58, 62 and 66 (blocking calls, scenario 5): `LastError.WinHttpError` is 12002 |
| F3 | Confirmed for the time-out | Groups 15, 16 and 35 to 39 (asynchronous calls, scenario 5): `WinHttpError` is 12002 |
| F4 | Confirmed | Groups 15 and 16: the event comes in under 15 s with a limit of 5 s, and scenario 5 stays silent for 20 s |
| F5 | Not checked on its own; the general run passed | None. Each request sets `Option(9)` and the run passed, but the tests use plain http and one Windows version |
| F6 | Not checked on its own; the general run passed | None. Group 3 gives these numbers to `CalyxMapWinHttpError` only |
| F7 | Confirmed | The test project builds with `CalyxJson.bas`, and every group that reads or writes JSON passes, for example group 4 |
| F8 | Confirmed that the lines work | `tests/CalyxTests.vbp` has the two lines, and the suite ran. Nobody compared them with a new IDE write |
| F9 | Not checked on its own; the general run passed | None. Nobody ran `fakebackend.exe` on Windows 7 |

For F2 and F3, the SDK accepts the HRESULT form and the bare number (group 3 checks both). The
run did not record which form WinHTTP gives. A related fact: `Option(6) = False` (no redirect)
is confirmed by groups 19, 20 and 40 to 44, which get a 302 and do not follow it.

### The run

- Windows 10: version and build: Not recorded. VB6 service pack: Not recorded. Date: 2026-11-25.
- Windows 10: SCHANNEL and proxy: Not recorded.
- Windows 7 SP1: version and build: Not run. Nobody ran the suite on Windows 7 SP1.
- Windows 7 SP1: SCHANNEL and proxy: Not recorded.

### R1: the default receive time limit (F1)

Expected: an error after about 30 seconds, with the WinHTTP time-out error 12002
(`ERROR_WINHTTP_TIMEOUT`).

```text
Not checked on its own; the general run passed (2026-11-25).
```

### R2: the error number in Err.Number (F2)

Expected: an HRESULT, for example `&H80072EFD` (12029, `ERROR_WINHTTP_CANNOT_CONNECT`). The low
16 bits give 12029.

```text
Confirmed for the time-out on 2026-11-25: groups 58, 62 and 66. The form is not recorded.
```

### R3: the error number in OnError (F3)

Expected: the same form as R2, or the bare number 12029. The SDK accepts both forms.

```text
Confirmed for the time-out on 2026-11-25: groups 15, 16 and 35 to 39. The form is not recorded.
```

### R4: the asynchronous time-out (F4)

Expected: `OnError` after about 5 seconds, with the time-out error 12002 in the form of R3.

- control (the synchronous call with the same limit): groups 58, 62 and 66 return in under 15 s
- conclusion: if the control call fails at about 5 s, `SetTimeouts` works. Then a 20 s async
  result means `OnError` does not carry the time-out. The async groups 15 and 16 return in under
  15 s, so `OnError` carries the time-out.

```text
Confirmed on 2026-11-25: groups 15 and 16.
```

### R5: Option(9) and TLS 1.2 (F5)

Expected: on Windows 10, both calls give status 200. On Windows 7 SP1 without KB3140245, the
set of `Option(9)` or the call with it fails. With KB3140245, the call with `Option(9)` gives
status 200.

- KB3140245 at the start: Not run
- registry at the start: Not run
- conclusion: Not run. See the verdict of F5.

Block "Windows 10":

```text
Not checked on its own; the general run passed (2026-11-25).
```

Block "Windows 7 SP1 without KB3140245":

```text
Not checked on its own; the general run passed (2026-11-25).
```

Block "Windows 7 SP1 with KB3140245, no registry value":

```text
Not checked on its own; the general run passed (2026-11-25).
```

Block "Windows 7 SP1 with KB3140245 and the registry value":

```text
Not checked on its own; the general run passed (2026-11-25).
```

### R6: the TLS and certificate error numbers (F6)

Expected: a number from 12037 to 12045, 12157 (`ERROR_WINHTTP_SECURE_CHANNEL_ERROR`) or 12175
(`ERROR_WINHTTP_SECURE_FAILURE`). The SDK maps each of them to `network_error`.

Block "TLS failure" (Windows 7 SP1 without KB3140245):

```text
Not checked on its own; the general run passed (2026-11-25).
```

Block "certificate failure" (Windows 10):

```text
Not checked on its own; the general run passed (2026-11-25).
```

### R7: VBA-JSON in VB6 (F7)

Expected: the build ends with no error. The JSON line is
`TypeName Dictionary, a = 1, b(1) = True, IsNull(b(2)) = True, AscW of the last character of c = 233`,
and the next line is `CalyxConvertToJson: {"a":1,"b":[true,null],"c":"x\u00E9"}`.

```text
Confirmed on 2026-11-25: the test project builds with CalyxJson.bas and its groups pass.
```

### R8: the Reference= lines (F8)

Expected: one line for WinHTTP 5.1 (`winhttpcom.dll`) and one line for Scripting Runtime
(`scrrun.dll`). The IDE can also write a line for OLE Automation (`stdole2.tlb`). Copy every
line.

```text
Confirmed on 2026-11-25 that the lines of tests/CalyxTests.vbp work; the suite ran.
```

### R9: a Go binary on Windows 7 SP1 (F9)

Expected: `fakebackend.exe` does not start on Windows 7, because it is built with Go 1.27.

```text
Not checked on its own; the general run passed (2026-11-25).
```
