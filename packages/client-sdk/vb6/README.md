# Calyx client SDK for VB6

A client for the HTTP API of the Calyx backend, for Visual Basic 6 programs (for example an ERP).
You add its source files to your VB6 project. A form sends a WhatsApp message and the window
stays responsive.

Status: version 0.1.0, early alpha. Not published. The author ran the SDK on Windows on
2026-11-25, and the checks pass.

| You want to | Read |
|---|---|
| Send a first message from a form | [4. First message](#4-first-message) |
| Handle a failure without a duplicate message | [5. Errors and retries](#5-errors-and-retries) |
| Send from a program with no window (Task Scheduler) | [6. Scheduled programs](#6-scheduled-programs) |
| Send one text to a list of customers | [7. Bulk jobs](#7-bulk-jobs) |
| Fix a TLS failure on Windows 7 | [10. TLS on Windows 7](#10-tls-on-windows-7) |

## 1. What the SDK is

The SDK calls 7 operations of the backend. It has no core client, no stream and no webhook
check. You pair instances in the frontend.

| Method of `CalyxClient` | Operation | Blocking form |
|---|---|---|
| `SendMessage` | `sendMessage`: one text to one phone number | `SendMessageSync` |
| `CreateBulkJobSameText` | `createBulkJob`: one text to many phone numbers | |
| `CreateBulkJobCustom` | `createBulkJob`: one text for each phone number | |
| `GetBulkJob` | `getBulkJob`: the progress of a job | |
| `ListBulkJobs` | `listBulkJobs`: the jobs of an instance | |
| `CancelBulkJob` | `cancelBulkJob`: stop a job | |
| `ListInstances` | `listInstances`: the instances that the API key can use | |
| `GetInstance` | `getInstance`: one instance and its status | `GetInstanceSync` |
| `Resend` | the operation of a failure, sent again without a change | `ResendSync` |
| `AbortAll` | stops the wait for every pending call | |

An asynchronous method starts the call and returns at once. The answer comes later as the
event `Completed` or `Failed`. A blocking (`Sync`) method waits for the answer.

Caution: Calyx is an unofficial integration with WhatsApp. Its use is against the WhatsApp
Terms of Service. WhatsApp can ban the number of an instance at any time, and no one can promise
that a number is never banned. Calyx applies pacing to make abuse hard, but pacing does not make
a number safe.

## 2. Before you start

Get these items before you write code:

1. Get the URL of your backend, for example `https://calyx.example.com`.
2. Get an API key. In the frontend, open "API keys" in the sidebar, and create a key. The page
   shows the key once, so copy it at once. Use the key of a client user (a user of one client
   organization), limited to its own instances. Without a frontend, make the key with the `curl`
   example of `apps/backend/README.md` ("Log in and API keys").
3. Pair an instance in the frontend, and copy its ID (a UUID).
4. Write each phone number with the country code and the number, digits only. For example,
   `34600000000` for the Spanish number 600 00 00 00. Do not add `+`, spaces or dashes.

Caution: keep the API key out of the source code and out of shared folders. Anyone with the key
can send messages from your instances. If the key leaks, make a new key and revoke
the old key in the frontend ("API keys", "Revoke"). Without a frontend, use `curl`
and `DELETE /api/v1/api-keys/{id}`.

The settings of `CalyxClient`:

| Property | Default | Rule |
|---|---|---|
| `BaseUrl` | `""` | Starts with `https://`. `http://` is allowed only for `localhost` and `127.0.0.1` (tests). No user name or password (`@`), no `?` and no `#`. The SDK removes a final `/`. |
| `ApiKey` | `""` | Not empty. Printable ASCII only (characters 33 to 126, no space). The SDK sends it as `Authorization: Bearer <key>`, and never writes it to `Detail` or to an error text. |
| `SendTimeoutSeconds` | 360 | The time limit of `sendMessage`. From 1 to 2147483. See [9. Time limits](#9-time-limits). |
| `CallTimeoutSeconds` | 150 | The time limit of every other call. From 1 to 2147483. |
| `LastError` | `Nothing` | Read only. The `CalyxError` of the last VB6 error that the SDK raised. |

The SDK checks the settings when a method starts a call, not when you set them. A wrong setting
raises `invalid_config` at that time.

## 3. Install

Caution: do not edit the SDK files. Put your changes in the modules of your program. An edited
file makes the next update unsafe, and a mix of versions can fail in ways that no test covers.

1. On Windows 7, do [part 10 (TLS on Windows 7)](#10-tls-on-windows-7) first.
2. Copy the folder `src` of this package into your project folder, for example as `calyx`.
3. In the VB6 IDE, open your project.
4. Click Project > Add File.
5. Add each of the 10 files of the folder: `CalyxCore.bas`, `CalyxData.bas`, `CalyxJson.bas`,
   `CalyxBulkMessage.cls`, `CalyxCall.cls`, `CalyxClient.cls`, `CalyxError.cls`,
   `CalyxFieldError.cls`, `CalyxRequest.cls` and `CalyxSendResult.cls`.
6. Click Project > References.
7. Select "Microsoft WinHTTP Services, version 5.1".
8. Select "Microsoft Scripting Runtime", and click OK.
9. Click File > Save Project.

The `.vbp` files of the examples and of the test project have these two lines. The SDK passed
its test suite on Windows on 2026-11-25 with them. The path part can differ on your computer (for example `C:\Windows\SysWOW64\`).

```text
Reference=*\G{662901FC-6951-4854-9EB2-D9A2570F2B2E}#5.1#0#C:\Windows\system32\winhttp.dll#Microsoft WinHTTP Services, version 5.1
Reference=*\G{420B2830-E718-11CF-893D-00A0C9054228}#1.0#0#C:\Windows\system32\scrrun.dll#Microsoft Scripting Runtime
```

Every public name of the SDK starts with `Calyx`, so it does not collide with a name of your
program. `CalyxJson.bas` is a copy of VBA-JSON with the prefix `Calyx` on each public name. A
project that already has `JsonConverter.bas` can load both.

Caution: the VB6 IDE gives one letter case to an identifier in every loaded module. Do not
declare a variable whose name differs from an SDK name only by case, for example `code`,
`source`, `retry`, `operation` or `request`. The IDE then changes the SDK files when you save.

### The names for your program

Use only these names. Your program can use them in later versions of 0.x too.

| Name | Kind | Use |
|---|---|---|
| `CalyxClient` | class | The only class that your program creates for calls. |
| `CalyxSendResult` | class | The result of `sendMessage`: `MessageId`, `Replayed`, `IdempotencyKey`, `RequestId`. |
| `CalyxError` | class | A failure. See [5. Errors and retries](#5-errors-and-retries). |
| `CalyxFieldError` | class | One item of `CalyxError.Errors`: `Location` and `Message`. |
| `CalyxBulkMessage` | class | One message of `CreateBulkJobCustom`: `Recipient`, `Text`, `DelayMs`. |
| `CalyxNewIdempotencyKey()` | function | A new idempotency key (a UUID version 4 in lower case). |
| `CalyxField(Dict, Name, Default)` | function | Reads a field of an answer. Gives `Default` when the field is absent or `Null`. |
| `CalyxAdviceFor(Source, Code)`, `CalyxRetryText(Retry)` | functions | The retry advice of a code, and its text. |
| `CalyxRetry` | enum | `calyxRetryUnknown`, `calyxRetryNow`, `calyxRetryLater`, `calyxRetryCheckIfSent`, `calyxRetryDoNotRetry`. |
| `CALYX_CODE_*` | constants | The error codes, for example `CALYX_CODE_SEND_IN_PROGRESS`. |
| `CALYX_OP_*` | constants | The operation names, for example `CALYX_OP_SEND_MESSAGE`. |
| `CALYX_SDK_VERSION` | constant | The version of the SDK, `"0.1.0"`. |

The other public names are internal. They are public only because a VB6 class cannot call a
`Private` function of a module. Do not call them, because they can change in any version. For
example: `CalyxClientFailure`, `CalyxFailureFromErr`, `CalyxBodyJson`, `CalyxTickCount`,
`CalyxPrepareRequest`, `CalyxSendRequest`, `CalyxReadAnswer`, `CalyxTransportFailure`,
`CalyxHandleAnswer`, `CalyxRaise`, `CalyxMapBackendError`, `CalyxMapWinHttpError`,
`CalyxCheckBaseUrl`, `CalyxCheckIdempotencyKey`, `CalyxEncodePathPart`, `CalyxUtf8Encode`,
`CalyxUtf8Decode`, `CalyxOperationMethod`, `CalyxOperationPath`, the classes `CalyxCall` and
`CalyxRequest`, and every public name of `CalyxJson.bas`.

## 4. First message

The example `examples/FirstMessage` is one complete form. To run it:

1. Copy `examples/calyx-example.ini.sample` to `calyx-example.ini` in the folder of the example.
2. Fill in `BaseUrl`, `ApiKey` and `InstanceId`.
3. Open `FirstMessage.vbp` in the VB6 IDE, and press F5.

Caution: send only to your own test number. A message to a real number can get the number of
the instance banned. Git ignores `calyx-example.ini`, because it holds the API key. Never
commit it.

The form has a TextBox `txtRecipient`, a CommandButton `cmdSend`, and the labels
`lblRecipient`, `lblTextTitle`, `lblText` and `lblStatus`. This is its code:

```vb
Option Explicit

' The first message from a form. The window stays responsive while the message goes out.
'
' Before you start:
' 1. Copy calyx-example.ini.sample to calyx-example.ini in this folder.
' 2. Fill in BaseUrl, ApiKey and InstanceId.
' Caution: send only to your own test number. A message to a real number can get the number
' of the instance banned.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long
Private Declare Function WritePrivateProfileString Lib "kernel32" Alias "WritePrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpString As String, _
    ByVal lpFileName As String) As Long

' WithEvents gives this form the Completed and Failed events of the client.
Private WithEvents mCalyx As CalyxClient
Private mInstanceId As String

Private Sub Form_Load()
    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    lblText.Caption = MessageText()
    If mCalyx.BaseUrl = "" Or mCalyx.ApiKey = "" Or mInstanceId = "" Then
        lblStatus.Caption = "Copy calyx-example.ini.sample to " & IniPath() & _
            ", and fill in BaseUrl, ApiKey and InstanceId."
        cmdSend.Enabled = False
    End If
End Sub

Private Sub cmdSend_Click()
    Dim sendKey As String
    On Error GoTo SendFailed
    ' Make the idempotency key first, and store it with its time before the send. A real
    ' program stores them with its own record (an invoice, an order). If the program closes
    ' during the send, it can send the same request with this key within 24 hours, and the
    ' backend sends the message at most once.
    sendKey = CalyxNewIdempotencyKey()
    WriteSetting "LastSend", "IdempotencyKey", sendKey
    WriteSetting "LastSend", "MadeAt", Format$(Now, "yyyy-mm-dd hh:nn:ss")
    ' SendMessage returns at once. The answer comes later as Completed or Failed.
    mCalyx.SendMessage mInstanceId, Trim$(txtRecipient.Text), MessageText(), sendKey
    cmdSend.Enabled = False
    lblStatus.Caption = "Sending. The window stays responsive. Key: " & sendKey
    Exit Sub
SendFailed:
    ' A wrong setting or argument raises a VB6 error at once. LastError holds the details.
    If mCalyx.LastError Is Nothing Then
        lblStatus.Caption = "Error: " & Err.Description
    Else
        ShowFailure mCalyx.LastError
    End If
End Sub

' For sendMessage, Result is a CalyxSendResult.
Private Sub mCalyx_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    Dim sent As CalyxSendResult
    If Operation <> CALYX_OP_SEND_MESSAGE Then Exit Sub
    Set sent = Result
    ' Replayed is True when the backend already had this key and did not send again.
    lblStatus.Caption = "Sent. Message ID: " & sent.MessageId & ". Replayed: " & sent.Replayed & "."
    cmdSend.Enabled = True
End Sub

Private Sub mCalyx_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    ShowFailure Failure
    cmdSend.Enabled = True
End Sub

' The example RetryWithResend shows what to do for each advice.
Private Sub ShowFailure(ByVal Failure As CalyxError)
    lblStatus.Caption = "Failed: " & Failure.Code & " (advice: " & Failure.RetryText & "). " & _
        Failure.Detail & " Key: " & Failure.IdempotencyKey
End Sub

Private Sub Form_Unload(Cancel As Integer)
    ' Stop the wait for every pending call. Without it, the client stays alive after the form
    ' closes. The backend can still send a message that it got.
    mCalyx.AbortAll
    Set mCalyx = Nothing
End Sub

' The code editor and the controls of VB6 use the ANSI code page, so the accents are written
' with ChrW$. A VB6 String is Unicode, and the SDK sends it with every accent.
Private Function MessageText() As String
    MessageText = "Hello from Calyx: caf" & ChrW$(233) & ", cr" & ChrW$(232) & "me br" & _
        ChrW$(251) & "l" & ChrW$(233) & "e, jalape" & ChrW$(241) & "o."
End Function

Private Function IniPath() As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    IniPath = folder & "calyx-example.ini"
End Function

Private Function ReadSetting(ByVal SectionName As String, ByVal KeyName As String) As String
    Dim buffer As String
    Dim copied As Long
    buffer = String$(1024, vbNullChar)
    copied = GetPrivateProfileString(SectionName, KeyName, "", buffer, Len(buffer), IniPath())
    ReadSetting = Left$(buffer, copied)
End Function

Private Sub WriteSetting(ByVal SectionName As String, ByVal KeyName As String, ByVal NewValue As String)
    WritePrivateProfileString SectionName, KeyName, NewValue, IniPath()
End Sub
```

Three rules of this form apply to every program:

1. Make the idempotency key with `CalyxNewIdempotencyKey`, and store it with its time before the
   send. A real program stores both with its own record (an invoice, an order).
2. Declare the client `WithEvents` at the level of the form, so the form gets `Completed` and
   `Failed`.
3. Call `mCalyx.AbortAll` in `Form_Unload`. A pending call keeps its client alive, so without
   `AbortAll` the client stays in memory after the form closes, and its events go nowhere.
   Before it returns, `AbortAll` raises `Failed` with `aborted` for each pending call.

Caution: do not resend an `aborted` failure while the form closes. The form is then gone. Set a
closing flag before `AbortAll`, and skip `Resend` when the flag is set, as `FormRetry` does.

The two events:

```vb
Event Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
Event Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
```

- `Operation` is the operation name, for example `sendMessage` (`CALYX_OP_SEND_MESSAGE`).
- `Tag` is the value that you gave to the method, for example the number of the ERP document. It
  is `Empty` when you gave none.
- `Result` is a `CalyxSendResult` for `sendMessage`. For the other operations it is the parsed
  answer, a `Scripting.Dictionary` (see [8. The answer fields](#8-the-answer-fields)).
- `Result` can be `Nothing` for an answer with no body. Check `Result Is Nothing` before you read
  it. No operation of version 0.1.0 has an empty success answer today.

The idempotency key:

- When you give no key, the SDK makes one. Each method that sends returns the key before any
  event.
- A key that you give must be a `String` of 1 to 255 characters from `!` to `~` (printable ASCII
  with no space). Otherwise the method raises `invalid_config`.
- An `Empty` key is refused with `invalid_config`. It does not mean "no key". To get a new key,
  leave the argument out.
- The backend keeps a key for 24 hours. After 24 hours, the same key sends the message again.

Text with accents: a VB6 `String` is Unicode, and the SDK sends it with every character. The
request body is ASCII JSON: each character above 126 goes out as `\uXXXX`, for example
`caf\u00E9`. The backend reads it as normal JSON. The code editor and the standard controls of
VB6 use the ANSI code page. A text typed in a TextBox can then lose characters on some code pages.
The example builds its text with `ChrW$`.

## 5. Errors and retries

### How a failure reaches your program

| Where | When | What you get |
|---|---|---|
| The event `Failed` | An asynchronous call failed after it started. | `Failure As CalyxError` |
| A VB6 error at once | A wrong setting or argument, a refused `Resend`, WinHTTP could not start the call, or a `Sync` method failed. | `Err.Number = vbObjectError + 513`, `Err.Source = "Calyx"`, `Err.Description = "<code>: <detail>"`. `mCalyx.LastError` holds the `CalyxError`. |

No asynchronous method raises `Failed` before it returns. Each public method sets `LastError` to
`Nothing` when it starts. When the call has an idempotency key, `LastError` has it too. An error
that the SDK did not raise reaches your program unchanged, and `LastError` stays `Nothing`.

The properties of `CalyxError`:

| Property | Meaning |
|---|---|
| `Source` | `backend` (the backend answered with an error) or `client` (the SDK found the failure). This is not `Err.Source`, which is always `"Calyx"`. |
| `Code` | The error code, for example `send_in_progress`. Compare it with a `CALYX_CODE_*` constant. |
| `Status` | The HTTP status. 0 for a `client` failure. A non-JSON error answer (for example the 504 HTML page of a proxy) gives `invalid_response` with `Status` 0. The HTTP status is then only in `Detail`. |
| `Detail` | A text for a person. Never contains the API key. |
| `RequestId` | The `X-Request-Id` of the answer, or `""`. Give it to the operator of the backend. |
| `IdempotencyKey`, `Operation`, `Tag` | The values of the call. |
| `Retry`, `RetryText` | The advice: a `CalyxRetry` value, and its text, for example `retry_later`. |
| `RetryAfter` | Seconds from the `Retry-After` header. 0 when the backend sends none. |
| `Errors` | A `Collection` of `CalyxFieldError` (`Location`, `Message`), for `invalid_request`. |
| `WinHttpError` | The WinHTTP error number, 0 when none. |
| `CanResend` | True when the failure has a request and its advice is `retry_now`, `retry_later` or `unknown`. |

### What you see, what it means, what your code does

| What you see | What it means | What your code does |
|---|---|---|
| Advice `retry_now`: `timeout`, `network_error`, `invalid_response`, `aborted`, `send_outcome_unknown`, `bulk_outcome_unknown` | The answer did not arrive. The backend can still finish the call. | Call `Resend Failure`. The same key cannot send the message twice. For `aborted`, do not resend while the form closes (see part 4, rule 3). |
| Advice `retry_later`: `send_in_progress`, `instance_queue_full`, `rate_limited`, `instance_not_connected`, `core_unavailable` | The call can work after a pause. `send_in_progress`: the first request with this key still waits in the queue. | Wait `RetryAfter` seconds (a Timer that ticks each second), then call `Resend Failure`. When `RetryAfter` is 0, wait 30 seconds. |
| Advice `check_if_sent`: `idempotency_key_outcome_unknown` | The core could not learn the result of the first send. It never sends again with this key. | Stop. Show "the result is unknown". A person checks the chat. Send again only if the message is not in the chat, with a new key, after 10 minutes. `Resend` refuses this failure. |
| Advice `do_not_retry`: for example `invalid_request`, `unauthenticated`, `not_found`, `duplicate_message`, `idempotency_key_conflict`, `invalid_config` | The same request fails again. `duplicate_message`: the same text went to the same number in the last 10 minutes. | Stop. Correct the request or the setup. Read `Detail` and `Errors`. `Resend` refuses this failure. |
| Advice `unknown`: `internal`, `conflict` | The error has no clear advice. | Decide in your program. `CanResend` is True, and the same key cannot send twice. The example waits as for `retry_later`. |
| `invalid_config` with "a redirect" in `Detail` | The backend answered `3xx`. The SDK follows no redirect, because the redirect could send the API key to another host. | Correct `BaseUrl`. |

`CalyxAdviceFor(Source, Code)` gives the advice of any code. The full map is in
`packages/client-sdk/testdata/retry-advice.json`.

A proxy or a firewall on the network of the customer can end a long send before 6 minutes. Your
program then sees `invalid_response` or `network_error`. Call `Resend`: the backend answers
`send_in_progress` while the message waits, or `Replayed = True` after the send.

### `Resend`

`Resend Failure` sends the request of the failure again without a change: the same body, the
same idempotency key and the same tag. Your program never handles the key on a retry, so it
cannot use a new key by mistake. It returns the idempotency key.

1. `Resend` raises `invalid_config` for a failure with the advice `check_if_sent` or
   `do_not_retry`, for `Nothing`, and for a failure with no request. Check `CanResend` first.
2. `Resend` uses the time limit of the first call. To use a new time limit, call the method
   again with the same idempotency key.
3. `Resend` uses the current `BaseUrl` and `ApiKey` of the client, and checks them first.
4. `Resend` does not wait. For `retry_later`, wait first with a Timer.
5. The SDK never calls `Resend` by itself. Your program decides.
6. Do not resend more than 24 hours after the time of the key. The backend no longer knows the
   key, so the message goes out again. Store the time of the key with the key.

### The example `RetryWithResend`

The form resends after 1 second for `retry_now`. For `retry_later` and `unknown`, it waits
`RetryAfter` seconds, or 30 seconds when `RetryAfter` is 0. The Timer ticks each second, and
`lblStatus` shows the seconds that are left. The form starts no new retry 6 minutes after the
first failure. Then it says "the result is unknown; the message can still arrive; do not send
it again with a new key". A retry that is already pending can still end with `Completed`. The
form can then end later than 6 minutes, with "Sent".

The form has the TextBoxes `txtRecipient` and `txtMessage`, a CommandButton `cmdSend`, a Timer
`tmrRetry`, and the labels `lblRecipient`, `lblMessage` and `lblStatus`. This is its code:

```vb
Option Explicit

' A retry that cannot duplicate. Resend sends the same body with the same idempotency
' key, so the core sends the message at most once.
'
' What the form does with each advice (Failure.Retry):
' - retry_now: Resend after 1 second.
' - retry_later, unknown: Resend after RetryAfter seconds. When RetryAfter is 0, after 30 seconds.
' - check_if_sent: stop. The result is unknown, and the key can never send again.
' - do_not_retry: stop. The same request fails again.
' After 6 minutes of retries, the form stops, because the result is unknown.
'
' Before you start, copy calyx-example.ini.sample to calyx-example.ini in this folder, and fill
' in BaseUrl, ApiKey and InstanceId.
' Caution: send only to your own test number. A message to a real number can get the number
' of the instance banned.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long

' A Timer starts every retry. With retry_now too, a short wait keeps a computer with no network
' from sending the request many times each second.
Private Const RETRY_NOW_SECONDS As Long = 1
' The wait when the backend sends no Retry-After (RetryAfter is 0).
Private Const RETRY_LATER_SECONDS As Long = 30
Private Const RETRY_LIMIT_SECONDS As Long = 360

Private WithEvents mCalyx As CalyxClient
Private mInstanceId As String
' The failure that waits for the Timer.
Private mWaiting As CalyxError
' A Timer holds at most about 65 seconds, and RetryAfter can be 240. So the Timer ticks each
' second, and this variable counts the seconds that are left.
Private mSecondsLeft As Long
' True from the first failure of a send to its end. mFirstFailureAt is the time of that failure.
Private mRetrying As Boolean
Private mFirstFailureAt As Date
Private mClosing As Boolean

Private Sub Form_Load()
    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    If mCalyx.BaseUrl = "" Or mCalyx.ApiKey = "" Or mInstanceId = "" Then
        lblStatus.Caption = "Copy calyx-example.ini.sample to " & IniPath() & _
            ", and fill in BaseUrl, ApiKey and InstanceId."
        cmdSend.Enabled = False
    End If
End Sub

Private Sub cmdSend_Click()
    Dim sendKey As String
    mRetrying = False
    cmdSend.Enabled = False
    On Error GoTo SendFailed
    ' With no key, SendMessage makes one and returns it before any event.
    sendKey = mCalyx.SendMessage(mInstanceId, Trim$(txtRecipient.Text), txtMessage.Text)
    lblStatus.Caption = "Sending. Key: " & sendKey
    Exit Sub
SendFailed:
    ShowCallError Err.Description
End Sub

Private Sub mCalyx_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    Dim sent As CalyxSendResult
    If Operation <> CALYX_OP_SEND_MESSAGE Then Exit Sub
    Set sent = Result
    ' After a Resend, Replayed is True when the first request had already sent the message.
    Finish "Sent. Message ID: " & sent.MessageId & ". Replayed: " & sent.Replayed & "."
End Sub

Private Sub mCalyx_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    ' AbortAll in Form_Unload raises Failed with aborted (advice retry_now). Do not resend then.
    If mClosing Then Exit Sub
    HandleFailure Failure
End Sub

Private Sub HandleFailure(ByVal Failure As CalyxError)
    If Not mRetrying Then
        mRetrying = True
        mFirstFailureAt = Now
    End If
    ' CanResend is True for retry_now, retry_later and unknown, when the failure has a request.
    If Failure.CanResend Then
        If Failure.Retry = calyxRetryNow Then
            WaitThenResend Failure, RETRY_NOW_SECONDS
        ElseIf Failure.RetryAfter > 0 Then
            WaitThenResend Failure, Failure.RetryAfter
        Else
            WaitThenResend Failure, RETRY_LATER_SECONDS
        End If
    ElseIf Failure.Retry = calyxRetryCheckIfSent Then
        Finish "The result is unknown (" & Failure.Code & "). The message can still arrive. " & _
            "Check the chat before you send it again. Do not send it again with a new key now."
    Else
        Finish "The send failed (" & Failure.Code & "), and the same request fails again. " & _
            "Correct the cause first. " & Failure.Detail
    End If
End Sub

Private Sub WaitThenResend(ByVal Failure As CalyxError, ByVal WaitSeconds As Long)
    If DateDiff("s", mFirstFailureAt, Now) >= RETRY_LIMIT_SECONDS Then
        Finish "The result is unknown. The message can still arrive. " & _
            "Do not send it again with a new key. Key: " & Failure.IdempotencyKey
        Exit Sub
    End If
    Set mWaiting = Failure
    mSecondsLeft = WaitSeconds
    tmrRetry.Interval = 1000
    tmrRetry.Enabled = True
    ShowWait
End Sub

Private Sub ShowWait()
    lblStatus.Caption = mWaiting.Code & ": the form sends the same request again in " & _
        mSecondsLeft & " s. Key: " & mWaiting.IdempotencyKey
End Sub

Private Sub tmrRetry_Timer()
    Dim waiting As CalyxError
    If mWaiting Is Nothing Then
        tmrRetry.Enabled = False
        Exit Sub
    End If
    mSecondsLeft = mSecondsLeft - 1
    If mSecondsLeft > 0 Then
        ShowWait
        Exit Sub
    End If
    tmrRetry.Enabled = False
    Set waiting = mWaiting
    Set mWaiting = Nothing
    If waiting Is Nothing Then Exit Sub
    On Error GoTo ResendFailed
    ' Resend uses the key, the body and the time limit of the first call.
    mCalyx.Resend waiting
    lblStatus.Caption = "Sent again with the same key: " & waiting.IdempotencyKey
    Exit Sub
ResendFailed:
    ShowCallError Err.Description
End Sub

' A method raises a VB6 error at once for a wrong setting, or when WinHTTP cannot start the
' call. LastError holds the CalyxError, with its advice.
Private Sub ShowCallError(ByVal ErrorText As String)
    If mCalyx.LastError Is Nothing Then
        Finish "Error: " & ErrorText
    Else
        HandleFailure mCalyx.LastError
    End If
End Sub

Private Sub Finish(ByVal Explanation As String)
    lblStatus.Caption = Explanation
    mRetrying = False
    cmdSend.Enabled = True
End Sub

Private Sub Form_Unload(Cancel As Integer)
    mClosing = True
    tmrRetry.Enabled = False
    ' Stop the wait for every pending call. The backend can still send a message that it got.
    mCalyx.AbortAll
    Set mCalyx = Nothing
End Sub

Private Function IniPath() As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    IniPath = folder & "calyx-example.ini"
End Function

Private Function ReadSetting(ByVal SectionName As String, ByVal KeyName As String) As String
    Dim buffer As String
    Dim copied As Long
    buffer = String$(1024, vbNullChar)
    copied = GetPrivateProfileString(SectionName, KeyName, "", buffer, Len(buffer), IniPath())
    ReadSetting = Left$(buffer, copied)
End Function
```

## 6. Scheduled programs

A program with no window, for example an EXE that the Windows Task Scheduler starts, can use the
three blocking methods:

| Method | Returns |
|---|---|
| `SendMessageSync(InstanceId, Recipient, Text, [IdempotencyKey], [Tag])` | A `CalyxSendResult`. |
| `GetInstanceSync(InstanceId, [Tag])` | The instance, a `Scripting.Dictionary`. |
| `ResendSync(Failure)` | An `Object`: a `CalyxSendResult` for `sendMessage`, the parsed answer (a `Scripting.Dictionary`) for the other operations, or `Nothing` for an empty answer. |

Caution: do not use a `Sync` method in a form. The window freezes until the answer, up to the
time limit (6 minutes for a send).

Rules of the `Sync` methods:

- Each one has the arguments of the asynchronous method with the same name.
- A failure raises a VB6 error: `vbObjectError + 513` with the source `"Calyx"`.
  `mCalyx.LastError` holds the `CalyxError`, with the key, `Status`, `RequestId` and
  `WinHttpError`.
- They raise no event.
- `ResendSync` follows every rule of `Resend`. A successful `getBulkJob` or `listBulkJobs` answer
  of `ResendSync` is saved, the same as an asynchronous answer (see "The saved answers" in
  [7. Bulk jobs](#7-bulk-jobs)).

The example `examples/ScheduledSend` checks the instance, then sends each reminder of the day.
It reads the reminders from the section `[Reminders]` of `calyx-example.ini`. It writes each
result to `scheduled-send.log` next to its EXE, with no phone number and no API key. Git ignores
the log.

```vb
Option Explicit

' A program with no window. The Windows Task Scheduler starts it, for example once a day.
' It checks the instance with GetInstanceSync, then sends each reminder with SendMessageSync.
'
' A Sync method blocks until the answer, so it is for a program with no form. In a form, the
' window freezes for up to the time limit (6 minutes for a send). A failure raises a VB6 error,
' and LastError holds the CalyxError. A Sync method raises no event.
'
' Before you start, copy calyx-example.ini.sample to calyx-example.ini in this folder. Fill in
' BaseUrl, ApiKey, InstanceId and the section [Reminders].
' Caution: send only to your own test number. A message to a real number can get the number
' of the instance banned.
'
' The program writes each result to scheduled-send.log in this folder.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long

Private mCalyx As CalyxClient
Private mInstanceId As String

Public Sub Main()
    Dim instanceInfo As Object
    Dim instanceStatus As String
    Dim reminderCount As Long
    Dim reminderIndex As Long

    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    WriteLog "Start."

    On Error GoTo CheckFailed
    Set instanceInfo = mCalyx.GetInstanceSync(mInstanceId)
    On Error GoTo 0
    ' The status field can be Null. CalyxField returns the default for a Null or absent field.
    instanceStatus = CalyxField(instanceInfo, "status", "")
    If instanceStatus <> "CONNECTED" Then
        WriteLog "The instance is not connected (status " & instanceStatus & "). No reminder sent."
        Exit Sub
    End If

    reminderCount = Val(ReadSetting("Reminders", "Count"))
    For reminderIndex = 1 To reminderCount
        SendReminder reminderIndex
    Next reminderIndex
    WriteLog "End."
    Exit Sub
CheckFailed:
    LogFailure "GetInstanceSync", Err.Description
End Sub

' The core applies its pacing, so each send can wait some minutes before it returns.
Private Sub SendReminder(ByVal ReminderNumber As Long)
    Dim reminderKey As String
    Dim sent As CalyxSendResult
    ' One idempotency key for each reminder of the day. If the Task Scheduler starts the program
    ' again on the same day, the backend sends nothing new: it answers with Replayed = True, or
    ' with send_in_progress while the first send waits. The backend keeps a key for 24 hours.
    reminderKey = "reminder-" & Format$(Date, "yyyymmdd") & "-" & ReminderNumber
    On Error GoTo SendFailed
    Set sent = mCalyx.SendMessageSync(mInstanceId, _
        ReadSetting("Reminders", "Recipient" & ReminderNumber), _
        ReadSetting("Reminders", "Text" & ReminderNumber), reminderKey)
    WriteLog "Reminder " & ReminderNumber & ": sent, message " & sent.MessageId & _
        ", replayed " & sent.Replayed & ", key " & reminderKey
    Exit Sub
SendFailed:
    LogFailure "Reminder " & ReminderNumber, Err.Description
End Sub

' The log gets the code and the key of the failure from LastError, and never the API key.
Private Sub LogFailure(ByVal What As String, ByVal ErrorText As String)
    Dim lastFailure As CalyxError
    Set lastFailure = mCalyx.LastError
    If lastFailure Is Nothing Then
        WriteLog What & ": failed: " & ErrorText
    Else
        WriteLog What & ": failed, code " & lastFailure.Code & ", advice " & _
            lastFailure.RetryText & ", key " & lastFailure.IdempotencyKey & ". " & lastFailure.Detail
    End If
End Sub

' A log failure must not stop an unattended run: an error dialog in a Task Scheduler session
' hangs the task.
Private Sub WriteLog(ByVal LogText As String)
    On Error Resume Next
    Dim fileNumber As Integer
    fileNumber = FreeFile
    Open AppFile("scheduled-send.log") For Append As #fileNumber
    Print #fileNumber, Format$(Now, "yyyy-mm-dd hh:nn:ss") & "  " & LogText
    Close #fileNumber
End Sub

Private Function AppFile(ByVal FileName As String) As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    AppFile = folder & FileName
End Function

' GetPrivateProfileStringA reads the file in the ANSI code page of Windows.
Private Function ReadSetting(ByVal SectionName As String, ByVal KeyName As String) As String
    Dim buffer As String
    Dim copied As Long
    buffer = String$(1024, vbNullChar)
    copied = GetPrivateProfileString(SectionName, KeyName, "", buffer, Len(buffer), _
        AppFile("calyx-example.ini"))
    ReadSetting = Left$(buffer, copied)
End Function
```

The key `reminder-<yyyymmdd>-<n>` makes a second run on the same day safe. The backend answers
`Replayed = True`, or `send_in_progress` while the first send waits, and sends nothing new.

To start the program every day:

1. Build `ScheduledSend.exe` (File > Make ScheduledSend.exe).
2. Put `calyx-example.ini` next to the EXE.
3. Open the Windows Task Scheduler, and click Create Basic Task.
4. Select a daily trigger and a time.
5. Select "Start a program", and select `ScheduledSend.exe`.
6. Set "Start in" to the folder of the EXE.

## 7. Bulk jobs

Caution: use bulk jobs only for transactional messages or for customers who asked for the
message. Bulk messages to people who did not ask for them get a number banned fast, and they
are against the purpose of Calyx.

The core applies its pacing to every job: a delay between two messages and pauses. The SDK has
no setting to change the pacing. A long list takes a long time: plan for minutes or hours, not
seconds.

The two create methods:

| Method | Input | Rules |
|---|---|---|
| `CreateBulkJobSameText(InstanceId, Recipients, Text, [IdempotencyKey], [Tag])` | `Recipients As Collection` of phone numbers | Every item must be a `String`. A phone number in a `Double` loses digits. `Nothing` or an item that is not a `String` raises `invalid_config`. |
| `CreateBulkJobCustom(InstanceId, Messages, [IdempotencyKey], [Tag])` | `Messages As Collection` of `CalyxBulkMessage` | Every item must be a `CalyxBulkMessage`. `DelayMs` is optional: see below. |

- Each create method returns the idempotency key before any event.
- A job has 1 to 5000 messages. More gives `too_many_messages`. A text has at most 4096
  characters.
- `DelayMs` of a `CalyxBulkMessage`: `Empty` (not set) or `Null` leaves the field out. Any other
  value must be a whole number from 0 to 2147483647, as a number or as a `String` of digits only.
  A `Boolean` or `"1.5"` raises `invalid_config`. The core refuses a value under its minimum.
- After a create failure with `CanResend`, call `Resend`. A new create with a new key can make a
  second job.

How to follow a job:

1. Start a Timer with an interval of 30 seconds after the create completes. Or wait until
   `estimated_finish_at` of the job.
2. Keep a "pending" flag. Skip a tick while the last poll waits for its answer.
3. Set the flag before you call `GetBulkJob`, and clear it in `Completed` and `Failed`.
4. Stop the Timer when the `status` is `COMPLETED`, `CANCELLED` or `FAILED`.
5. Always offer a cancel button that calls `CancelBulkJob`.
6. In `Form_Unload`, call `AbortAll`. A job that the backend created goes on. Cancel it first if
   you do not want it.

### The saved answers

`GetBulkJob` and `ListBulkJobs` save each answer. When your program asks for the same job, or
the same list, within 25 seconds, the SDK makes no server call. It raises `Completed` with the
saved answer.

- `Completed` comes before the method returns. So set your "pending" flag before the call.
- Do not call `GetBulkJob` or `ListBulkJobs` from their own `Completed` handler. Within 25
  seconds, the call raises `Completed` again at once, with no end.
- Every caller gets the same `Dictionary` object. Do not change it.
- `CreateBulkJobSameText`, `CreateBulkJobCustom` and `CancelBulkJob` remove the saved answers
  that they make old. A call that was pending at that time can still save an old answer. An old
  answer is at most 25 seconds old.
- Each `CalyxClient` has its own saved answers. A failure is never saved.
- A poll every 30 seconds never gets a saved answer.

### The example `BulkJob`

The form has a multi-line TextBox `txtRecipients` (one phone number on each line), a TextBox
`txtMessage`, the CommandButtons `cmdStart` and `cmdCancel`, a Timer `tmrPoll` (30000 ms), and a
label `lblStatus`. These are the procedures that call the SDK:

```vb
Private WithEvents mCalyx As CalyxClient
Private mInstanceId As String
Private mJobId As String
' True from the GetBulkJob call to its Completed or Failed event.
Private mPollPending As Boolean
' A create that failed with an unknown result. Start sends it again with Resend.
Private mCreateFailure As CalyxError

Private Sub cmdStart_Click()
    Dim phones As Collection
    Dim failedCreate As CalyxError
    Dim jobKey As String
    On Error GoTo StartFailed
    If mCreateFailure Is Nothing Then
        Set phones = PhoneList(txtRecipients.Text)
        If phones.Count = 0 Then
            lblStatus.Caption = "Type at least one phone number."
            Exit Sub
        End If
        jobKey = mCalyx.CreateBulkJobSameText(mInstanceId, phones, txtMessage.Text)
    Else
        ' The same body with the same idempotency key, so the backend creates the job at most
        ' once. Changes to the list after the failure are not sent.
        Set failedCreate = mCreateFailure
        Set mCreateFailure = Nothing
        jobKey = mCalyx.Resend(failedCreate)
    End If
    cmdStart.Enabled = False
    lblStatus.Caption = "Creating the bulk job. Key: " & jobKey
    Exit Sub
StartFailed:
    ShowCallError Err.Description
End Sub

Private Sub tmrPoll_Timer()
    ' Skip this tick while the last poll waits for its answer.
    If mPollPending Then Exit Sub
    ' Set the flag before the call. Within 25 seconds of the last answer, GetBulkJob raises
    ' Completed with the saved answer before it returns.
    mPollPending = True
    On Error GoTo PollFailed
    mCalyx.GetBulkJob mJobId
    Exit Sub
PollFailed:
    mPollPending = False
    ShowCallError Err.Description
End Sub

Private Sub cmdCancel_Click()
    On Error GoTo CancelFailed
    mCalyx.CancelBulkJob mJobId
    lblStatus.Caption = "Cancelling the job."
    Exit Sub
CancelFailed:
    ShowCallError Err.Description
End Sub

' For the bulk operations, Result is a Scripting.Dictionary with the fields of the job. Do not
' change it: a saved answer is the same object for every caller.
Private Sub mCalyx_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    Select Case Operation
    Case CALYX_OP_CREATE_BULK_JOB
        mJobId = CalyxField(Result, "id", "")
        If Len(mJobId) = 0 Then
            ' Without an ID, the Timer and Cancel cannot ask about the job.
            cmdStart.Enabled = True
            lblStatus.Caption = "Error: the answer has no job ID."
        Else
            cmdCancel.Enabled = True
            tmrPoll.Enabled = True
            ShowJob Result
        End If
    Case CALYX_OP_GET_BULK_JOB
        mPollPending = False
        ShowJob Result
    Case CALYX_OP_CANCEL_BULK_JOB
        ShowJob Result
    End Select
End Sub

Private Sub mCalyx_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    Select Case Operation
    Case CALYX_OP_GET_BULK_JOB
        ' The Timer asks again at the next tick.
        mPollPending = False
        ShowFailure Failure
    Case CALYX_OP_CREATE_BULK_JOB
        RememberCreateFailure Failure
    Case Else
        ShowFailure Failure
    End Select
End Sub

Private Sub Form_Unload(Cancel As Integer)
    tmrPoll.Enabled = False
    ' Stop the wait for every pending call. A job that the backend created goes on: cancel it
    ' first if you do not want it.
    mCalyx.AbortAll
    Set mCalyx = Nothing
End Sub
```

The full form, with `Form_Load`, `RememberCreateFailure`, `ShowFailure`, `ShowJob`,
`ShowCallError`, `PhoneList` and the other helpers, is in
`examples/BulkJob/FormBulkJob.frm`.

### Large jobs on a slow connection

The backend reads a request body for at most 60 seconds, and a body has at most 16 MiB. A 16 MiB
body needs about 2.2 Mbit/s of upload to arrive in time.

1. Estimate the body in bytes. For `CreateBulkJobCustom`, use the number of messages multiplied
   by (the text length + 50). For `CreateBulkJobSameText`, use the text length plus the number
   of recipients multiplied by about 15. Count each character above 126 as 6 bytes, because it
   goes out as `\uXXXX`.
2. Keep the body under 7 MB for each Mbit/s of upload. For example, at 1 Mbit/s, keep the body
   under 7 MB.
3. If the body is larger, split the list into more jobs. Never make a body of more than 16 MiB.

## 8. The answer fields

The SDK parses each answer with VBA-JSON (`CalyxJson.bas`). The field names are the names of the
wire, for example `estimated_finish_at`.

| JSON | VB6 |
|---|---|
| object | `Scripting.Dictionary` |
| array | `Collection` |
| text | `String` |
| number | `Double` |
| `true`, `false` | `Boolean` |
| `null` | `Null` |

Caution: a `Null` field put in a `String` raises VB6 error 94. Read each field with
`CalyxField(Dict, Name, Default)`. It gives `Default` when the field is absent or `Null`:

```vb
instanceStatus = CalyxField(instanceInfo, "status", "")
```

Timestamps are UTC text in the ISO 8601 form, for example `2026-11-21T10:30:00Z`. The SDK does
not change them to local time.

`sendMessage` gives a `CalyxSendResult`:

| Property | Meaning |
|---|---|
| `MessageId` | The ID of the message. |
| `Replayed` | True when the backend already had this key and sent nothing new. |
| `IdempotencyKey` | The key of the call. |
| `RequestId` | The `X-Request-Id` of the answer, or `""`. |

An instance (`getInstance`, `GetInstanceSync`, and each item of `listInstances`):

| Field | Can be `Null` | Meaning |
|---|---|---|
| `id` | no | The ID of the instance. |
| `client_id` | no | The client organization of the instance. |
| `display_name` | no | The name in the frontend. |
| `provider` | no | `WHATSAPP_WEB` or `META_CLOUD`. |
| `send_only` | no | True when the instance only sends. |
| `enabled` | no | False when the instance is disabled. |
| `state` | no | `creating`, `active`, `removing` or `missing`. |
| `status` | **yes** | `CONNECTED`, `CONNECTING`, `NEEDS_AUTH`, `DISCONNECTED` or `ERROR`. `Null` when the backend does not know it. |
| `status_changed_at` | **yes** | The time of the last change of `status`. |
| `status_stale` | no | True when the backend cannot reach the core. `status` is then the last known status. |
| `device_identity` | **yes** | What the phone shows in its list of linked devices. |
| `locale` | **yes** | For example `es-ES`. |
| `phone_number_id` | **yes** | `META_CLOUD` only. |
| `webhooks` | no | A `Collection` of webhooks: `url`, `event`, `has_secret`. |
| `alert` | **yes** | The open alert: `kind` (`needs_auth`, `error`, `disconnected` or `missing`), `since` (can be `Null`), `emailed_at` (can be `Null`). |

A bulk job (`createBulkJob`, `getBulkJob`, `cancelBulkJob`, and each item of `listBulkJobs`):

| Field | Can be `Null` | Meaning |
|---|---|---|
| `id` | no | The ID of the job. |
| `instance_id` | no | The instance of the job. |
| `status` | no | `QUEUED`, `RUNNING`, `COMPLETED`, `CANCELLED` or `FAILED`. |
| `total_messages`, `sent_messages`, `failed_messages` | no | The counts of messages. |
| `progress_percent` | no | From 0 to 100. |
| `estimated_finish_at` | **yes** | The estimated end of the job. |
| `created_at` | no | The time of the create. |
| `replayed` | no | True when the create used a key that the backend already had. |

`listInstances` and `listBulkJobs` give a `Dictionary` with one field, `items`: a `Collection`
of instances or of jobs.

## 9. Time limits

| Limit | Value | Reason |
|---|---|---|
| `SendTimeoutSeconds` | 360 s | The backend waits up to 5 minutes for a send in the queue of the core. The limit must be longer, or the SDK stops a call that the backend still serves. |
| `CallTimeoutSeconds` | 150 s | `createBulkJob` can take 120 s: 60 s to read the body and 60 s for the core call. `listBulkJobs` answers within 30 s. |
| Resolve the host name | 30 s, fixed | Long enough for a slow network. A longer wait only delays the failure. |
| Connect | 30 s, fixed | Long enough for a slow network. A longer wait only delays the failure. |
| Saved answers | 25 s, fixed | A poll every 30 seconds never gets a saved answer. A faster poll causes load on the backend and the core. |

- WinHTTP gets the time limit of the operation for the send of the request body and for the
  receive of the answer.
- Each time limit must be from 1 to 2147483 seconds, because WinHTTP takes milliseconds in a
  `Long`. Another value raises `invalid_config` when a method starts a call.
- A call that passes its time limit fails with `timeout` (advice `retry_now`).
- `Resend` keeps the time limit of the first call.
- The proxy in front of the backend must allow at least 6 minutes for an answer on the send
  route. Otherwise the proxy answers a long send with a 504 page, and your program sees
  `invalid_response`.

## 10. TLS on Windows 7

Windows 7 SP1 does not use TLS 1.2 in WinHTTP by default. The backend needs TLS 1.2, so a call
fails with `network_error`. Its `Detail` has the WinHTTP error number and names this section.
The SDK sets TLS 1.2 on each request (`Option(9)`). On Windows 7 SP1 this needs the update
KB3140245. The SDK passed its test suite on Windows on 2026-11-25. That run did not test TLS on
Windows 7 SP1, so the need for KB3140245 is not tested.

Caution: steps 3 to 5 change the TLS settings of Windows for every program that uses WinHTTP.
Do them first on a test computer.

1. Run `wmic qfe get hotfixid | find "KB3140245"`. If the output has the update, go to step 4.
2. Get KB3140245 for your edition of Windows 7 (x86 or x64) from the Microsoft Update Catalog.
3. Install it, and restart Windows.
4. Run this command in a Command Prompt that runs as administrator:

   ```bat
   reg add "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp" /v DefaultSecureProtocols /t REG_DWORD /d 0x00000A00 /f
   ```

5. On a 64-bit Windows 7, also run this command. A VB6 program is a 32-bit program, and it reads
   this path:

   ```bat
   reg add "HKLM\SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp" /v DefaultSecureProtocols /t REG_DWORD /d 0x00000A00 /f
   ```

6. Restart Windows.

The value `0xA00` turns on TLS 1.1 and TLS 1.2.

The SDK passed its test suite on Windows on 2026-11-25. The suite checks the time-out number
12002. The other numbers are not tested. WinHTTP can give a number as an HRESULT
(`&H80072EE2`) or as the bare number (12002). The SDK uses the low 16 bits, so both forms give
the same result.

| WinHTTP error | Name | Code |
|---|---|---|
| 12002 | `ERROR_WINHTTP_TIMEOUT` | `timeout` |
| 12037 to 12045 | the certificate errors, for example `ERROR_WINHTTP_SECURE_INVALID_CA` (12045) | `network_error`, with the TLS hint |
| 12057 | `ERROR_WINHTTP_SECURE_CERT_REV_FAILED` | `network_error`, with the TLS hint |
| 12157 | `ERROR_WINHTTP_SECURE_CHANNEL_ERROR` | `network_error`, with the TLS hint |
| 12169 | `ERROR_WINHTTP_SECURE_INVALID_CERT` | `network_error`, with the TLS hint |
| 12170 | `ERROR_WINHTTP_SECURE_CERT_REVOKED` | `network_error`, with the TLS hint |
| 12175 | `ERROR_WINHTTP_SECURE_FAILURE` | `network_error`, with the TLS hint |
| any other number | for example `ERROR_WINHTTP_CANNOT_CONNECT` (12029) | `network_error` |

### The proxy

WinHTTP does not read the proxy settings of Internet Explorer. If your network needs a proxy:

1. Set the proxy in Internet Explorer.
2. Run `netsh winhttp import proxy source=ie` in a Command Prompt that runs as administrator.
3. Run `netsh winhttp show proxy`, and check the proxy.

### Check the connection

The test program has a mode that only reads. It calls `GetInstanceSync` once, so no message goes
out.

Caution: the mode writes `BaseUrl` and the instance ID to `results.txt`. Do not commit that file
(Git ignores it). Do not put a user name or a password in `BaseUrl`.

1. Build `tests\CalyxTests.exe` from `tests\CalyxTests.vbp`.
2. Open a Command Prompt in the folder `tests`.
3. Run `CalyxTests.exe tls-check <BaseUrl> <API key> <instance ID>`.
4. Open `results.txt`. A `PASS` line shows the instance. A `FAIL` line shows the code, the
   WinHTTP error number and the detail.

The mode uses the default `CallTimeoutSeconds` (150 s). With no answer, it waits up to that
limit.

## 11. Versions and updates

The version is in `CALYX_SDK_VERSION` of `CalyxCore.bas`. The headers of the files of `src` have
no version. `CHANGELOG.md` lists the changes of each version. The versions stay at `0.x` while
the contracts change.

The oldest backend that version 0.1.0 supports is the backend of this repository on 2026-11-21,
the date of version 0.1.0.

Caution: replace all the SDK files together. A project with files of two versions can fail in
ways that no test covers.

1. Read `CHANGELOG.md` for the changes between your version and the new version.
2. Close your project in the VB6 IDE.
3. Replace each of the 10 files of your SDK folder with the file of the new version.
4. Open your project, and click File > Make to build it.
5. Check that `CALYX_SDK_VERSION` has the new version.
6. Test your program against a test instance before you deliver it.

Do not edit an SDK file to fix a problem. Put the change in a module of your program, and tell
the maintainers about the problem.

## 12. Licences

| Files | Licence |
|---|---|
| This package (`packages/client-sdk/vb6`) | Apache-2.0 (`LICENSE`) |
| `src/CalyxJson.bas` | MIT. It is a copy of VBA-JSON v2.3.1 by Tim Hall, with the prefix `Calyx` on each public name. The MIT notice stays at the top of the file. The original is in `third_party/vba-json`. |

A program that includes `CalyxJson.bas` keeps its MIT notice.
