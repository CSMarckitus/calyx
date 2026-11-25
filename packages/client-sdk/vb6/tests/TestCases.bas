Attribute VB_Name = "TestCases"
' The test cases. TestMain records the results.
'
' The fake backend (tools/cmd/fakebackend) never calls a core, so no WhatsApp message goes out.
' The ID in the path selects the scenario. ListInstances has no ID, so it always gets scenario 1.
Option Explicit

' The fake backend accepts only this key. A failure line never shows it.
Private Const TEST_API_KEY As String = "test-key"
Private Const WRONG_API_KEY As String = "wrong-key"

' The fixed values of scenario 1, the same as in tools/cmd/fakebackend/scenarios.go.
Private Const SCENARIO_ID_PREFIX As String = "00000000-0000-4000-8000-00000000000"
Private Const CLIENT_ID As String = "00000000-0000-4000-8000-0000000000c1"
Private Const SEND_MESSAGE_ID As String = "3EB0C767D0A1B2C3D4E5"
Private Const TIME_CHANGED As String = "2026-11-20T10:00:00Z"
Private Const TIME_EMAILED As String = "2026-11-20T10:05:00Z"
Private Const JOB_ID As String = "00000000-0000-4000-8000-0000000000b1"
Private Const TIME_CREATED As String = "2026-11-20T09:00:00Z"
Private Const TIME_FINISH As String = "2026-11-20T10:30:00Z"

Private Const RECIPIENT_NUMBER As String = "34600000000"
Private Const SECOND_RECIPIENT As String = "34600000001"
Private Const PLAIN_TEXT As String = "Hello"
' VBA-JSON writes a text like this one as a number, with no quotes, unless the SDK prevents it.
Private Const DIGITS_TEXT As String = "1234567890123456"
Private Const JSON_CONTENT_TYPE As String = "application/json; charset=utf-8"
Private Const HTML_DETAIL As String = "The backend answered 502 with a body that is not problem+json."
Private Const WAIT_LIMIT_SECONDS As Single = 30
' Scenario 5 stays silent for 20 s. A time limit of 5 s must end the call well before that.
Private Const SHORT_LIMIT_SECONDS As Long = 5
Private Const SHORT_LIMIT_MAX_WAIT As Single = 15
' The UTF-8 bytes of SampleText().
Private Const SAMPLE_UTF8_HEX As String = "4f6cc3a120636166c3a920f09f9880"
' The body of scenario 7. VBA-JSON writes each character above 126 as \uXXXX, so it is ASCII.
Private Const SAMPLE_BODY_JSON As String = "{""to"":""34600000000"",""text"":""Ol\u00E1 caf\u00E9 \uD83D\uDE00""}"

' The five bulk-job methods, by name. StartBulkCall starts the one that a case names.
Private Const BULK_SAME_TEXT As String = "CreateBulkJobSameText"
Private Const BULK_CUSTOM As String = "CreateBulkJobCustom"
Private Const BULK_GET As String = "GetBulkJob"
Private Const BULK_LIST As String = "ListBulkJobs"
Private Const BULK_CANCEL As String = "CancelBulkJob"
' The bodies of TwoRecipients() and TwoMessages(). The first message sets DelayMs to the String
' "500", which must become the JSON number 500. The second leaves DelayMs Empty.
Private Const SAME_TEXT_BODY_JSON As String = "{""recipients"":[""34600000000"",""34600000001""],""text"":""Hello""}"
Private Const CUSTOM_BODY_JSON As String = "{""messages"":[{""to"":""34600000000"",""text"":""Hello"",""delay_ms"":500},{""to"":""34600000001"",""text"":""Hello""}]}"

' The three blocking methods, by name. CaseSyncScenario calls the one that a case names.
Private Const SYNC_SEND As String = "SendMessageSync"
Private Const SYNC_GET As String = "GetInstanceSync"
Private Const SYNC_RESEND As String = "ResendSync"
Private Const SYNC_TAG As String = "tag-sync"

' WINHTTP_TIMEOUT_NUMBER is confirmed: the real time-out cases (groups 15, 16, 58, 62 and 66) check
' it. Provisional: the other numbers only feed CalyxMapWinHttpError (group 3). They are not checked
' on their own; the general run passed.
Private Const WINHTTP_TIMEOUT_NUMBER As Long = 12002
Private Const WINHTTP_TIMEOUT_HRESULT As Long = &H80072EE2
Private Const WINHTTP_SECURE_FAILURE_NUMBER As Long = 12175
Private Const WINHTTP_SECURE_FAILURE_HRESULT As Long = &H80072F8F
Private Const WINHTTP_CANNOT_CONNECT_NUMBER As Long = 12029

Private Const CASE_GROUP_COUNT As Long = 68

Private mBaseUrl As String
Private mDataFolder As String
' The listener of the running case. RunGroup stops it after each group, also after an error.
Private mListener As TestListener
' The fake counts the requests of each key and never resets, so each run makes a new key.
' Group 11 sends the third request with the key of group 10.
Private mScenario2Key As String

Public Sub RunAllCases(ByVal BaseUrl As String, ByVal DataFolder As String)
    Dim groupIndex As Long
    mBaseUrl = BaseUrl
    If Right$(mBaseUrl, 1) = "/" Then mBaseUrl = Left$(mBaseUrl, Len(mBaseUrl) - 1)
    mDataFolder = DataFolder
    If Right$(mDataFolder, 1) <> "\" And Right$(mDataFolder, 1) <> "/" Then mDataFolder = mDataFolder & "\"
    For groupIndex = 1 To CASE_GROUP_COUNT
        RunGroup groupIndex
    Next groupIndex
End Sub

' An error in a case comes back here, fails the open case, and the next group runs.
Private Sub RunGroup(ByVal Which As Long)
    On Error GoTo Crashed
    Select Case Which
    Case 1: CaseBackendErrors
    Case 2: CaseRetryAdvice
    Case 3: CaseWinHttpErrors
    Case 4: CaseUtf8RoundTrip
    Case 5: CaseKeyFormat
    Case 6: CaseUnauthenticated
    Case 7: CaseSendScenario1
    Case 8: CaseGetInstanceScenario1
    Case 9: CaseListInstancesScenario1
    Case 10: CaseSendScenario2
    Case 11: CaseSendScenario2Replay
    Case 12: CaseSendScenario3
    Case 13: CaseSendScenario4
    Case 14: CaseGetInstanceScenario4
    Case 15: CaseSendScenario5
    Case 16: CaseGetInstanceScenario5
    Case 17: CaseGetInstanceScenario6
    Case 18: CaseSendScenario7
    Case 19: CaseSendScenario8
    Case 20: CaseGetInstanceScenario8
    Case 21: CaseAbortAll
    Case 22: CaseBadApiKey
    Case 23: CaseSendDigitsText
    Case 24: CaseUserInBaseUrl
    Case 25: CaseBulkScenario1 BULK_SAME_TEXT
    Case 26: CaseBulkScenario1 BULK_CUSTOM
    Case 27: CaseBulkScenario1 BULK_GET
    Case 28: CaseBulkScenario1 BULK_LIST
    Case 29: CaseBulkScenario1 BULK_CANCEL
    Case 30: CaseBulkFailure BULK_SAME_TEXT, 4
    Case 31: CaseBulkFailure BULK_CUSTOM, 4
    Case 32: CaseBulkFailure BULK_GET, 4
    Case 33: CaseBulkFailure BULK_LIST, 4
    Case 34: CaseBulkFailure BULK_CANCEL, 4
    Case 35: CaseBulkFailure BULK_SAME_TEXT, 5
    Case 36: CaseBulkFailure BULK_CUSTOM, 5
    Case 37: CaseBulkFailure BULK_GET, 5
    Case 38: CaseBulkFailure BULK_LIST, 5
    Case 39: CaseBulkFailure BULK_CANCEL, 5
    Case 40: CaseBulkFailure BULK_SAME_TEXT, 8
    Case 41: CaseBulkFailure BULK_CUSTOM, 8
    Case 42: CaseBulkFailure BULK_GET, 8
    Case 43: CaseBulkFailure BULK_LIST, 8
    Case 44: CaseBulkFailure BULK_CANCEL, 8
    Case 45: CaseGetBulkJobScenario6
    Case 46: CaseBadDelayMs
    Case 47: CaseQueryInBaseUrl
    Case 48: CaseSavedAnswer BULK_GET, BULK_CANCEL
    Case 49: CaseSavedAnswer BULK_LIST, BULK_CANCEL
    Case 50: CaseSavedAnswer BULK_LIST, BULK_SAME_TEXT
    Case 51: CaseResendScenario2
    Case 52: CaseResendRefused
    Case 53: CaseBadBulkItems
    Case 54: CaseNullDelayMs
    Case 55: CaseSyncScenario SYNC_SEND, 1
    Case 56: CaseSyncScenario SYNC_SEND, 3
    Case 57: CaseSyncScenario SYNC_SEND, 4
    Case 58: CaseSyncScenario SYNC_SEND, 5
    Case 59: CaseSyncScenario SYNC_GET, 1
    Case 60: CaseSyncScenario SYNC_GET, 3
    Case 61: CaseSyncScenario SYNC_GET, 4
    Case 62: CaseSyncScenario SYNC_GET, 5
    Case 63: CaseSyncScenario SYNC_RESEND, 1
    Case 64: CaseSyncScenario SYNC_RESEND, 3
    Case 65: CaseSyncScenario SYNC_RESEND, 4
    Case 66: CaseSyncScenario SYNC_RESEND, 5
    Case 67: CaseErrorsCopy
    Case 68: CaseRetryAfterForms
    End Select
    StopListener
    EndCase
    Exit Sub
Crashed:
    If Not CaseIsOpen() Then BeginCase "group " & Which
    Check False, "VB6 error " & Err.Number & ": " & Err.Description
    StopListener
    EndCase
End Sub

' ---- The pure functions ----

Private Sub CaseBackendErrors()
    Dim testData As Object
    Dim entry As Variant
    BeginCase "read backend-errors.json"
    Set testData = ReadJsonFile("backend-errors.json")
    EndCase
    For Each entry In testData.Item("cases")
        CheckBackendErrorCase entry
    Next entry
End Sub

Private Sub CheckBackendErrorCase(ByVal OneCase As Object)
    Dim headers As Object
    Dim want As Object
    Dim wantErrors As Object
    Dim mapped As CalyxError
    Dim gotErrors As Collection
    Dim gotError As CalyxFieldError
    Dim index As Long
    BeginCase "CalyxMapBackendError: " & OneCase.Item("name")
    Set headers = OneCase.Item("headers")
    Set want = OneCase.Item("expected")
    Set mapped = CalyxMapBackendError(CLng(OneCase.Item("status")), _
        TextOf(headers, "Content-Type"), TextOf(headers, "Retry-After"), _
        TextOf(headers, "X-Request-Id"), OneCase.Item("body"))
    CheckEqual mapped.Source, want.Item("source"), "Source"
    CheckEqual mapped.Code, want.Item("code"), "Code"
    CheckEqual mapped.Status, want.Item("status"), "Status"
    CheckEqual mapped.Detail, want.Item("detail"), "Detail"
    CheckEqual mapped.RetryText, want.Item("retry"), "RetryText"
    CheckEqual mapped.RequestId, want.Item("requestId"), "RequestId"
    CheckEqual mapped.RetryAfter, want.Item("retryAfterSeconds"), "RetryAfter"
    Set wantErrors = want.Item("errors")
    Set gotErrors = mapped.Errors
    CheckEqual gotErrors.Count, wantErrors.Count, "Errors.Count"
    If gotErrors.Count = wantErrors.Count Then
        For index = 1 To wantErrors.Count
            Set gotError = gotErrors.Item(index)
            CheckEqual gotError.Location, wantErrors.Item(index).Item("location"), "Errors(" & index & ").Location"
            CheckEqual gotError.Message, wantErrors.Item(index).Item("message"), "Errors(" & index & ").Message"
        Next index
    End If
    EndCase
End Sub

' The program can change the Collection that Errors returns. A second read must not see it.
Private Sub CaseErrorsCopy()
    Dim mapped As CalyxError
    Dim firstRead As Collection
    Dim secondRead As Collection
    BeginCase "CalyxError.Errors returns a copy"
    Set mapped = CalyxMapBackendError(422, "application/problem+json", "", "r-copy", _
        "{""status"":422,""code"":""invalid_request"",""errors"":[{""location"":""body.to"",""message"":""must not be empty""}]}")
    Set firstRead = mapped.Errors
    CheckEqual firstRead.Count, 1, "Errors.Count"
    firstRead.Remove 1
    firstRead.Add New CalyxFieldError
    firstRead.Add New CalyxFieldError
    Set secondRead = mapped.Errors
    CheckEqual secondRead.Count, 1, "Errors.Count after the program changed the first read"
    If secondRead.Count = 1 Then CheckEqual secondRead.Item(1).Location, "body.to", "Errors(1).Location after the change"
    EndCase
End Sub

' CalyxMapBackendError with a Retry-After value of the forms that Go's strconv.Atoi accepts.
Private Sub CaseRetryAfterForms()
    CheckRetryAfterForm "+5", 5
    CheckRetryAfterForm "0000000060", 60
    CheckRetryAfterForm "0", 0
    CheckRetryAfterForm "2147483647", 2147483647
    CheckRetryAfterForm "2147483648", 0
    CheckRetryAfterForm "+", 0
    CheckRetryAfterForm "+-5", 0
    CheckRetryAfterForm "5 s", 0
End Sub

Private Sub CheckRetryAfterForm(ByVal Header As String, ByVal Expected As Long)
    Dim mapped As CalyxError
    BeginCase "Retry-After """ & Header & """"
    Set mapped = CalyxMapBackendError(429, "application/problem+json", Header, "r-form", _
        "{""status"":429,""code"":""rate_limited"",""detail"":""Too many requests.""}")
    CheckEqual mapped.RetryAfter, Expected, "RetryAfter"
    EndCase
End Sub

' Only the backend and client rows: VB6 never sees a core code.
Private Sub CaseRetryAdvice()
    Dim testData As Object
    Dim entry As Variant
    Dim rowSource As String
    Dim rowCode As String
    BeginCase "read retry-advice.json"
    Set testData = ReadJsonFile("retry-advice.json")
    EndCase
    For Each entry In testData.Item("rows")
        rowSource = entry.Item("source")
        rowCode = entry.Item("code")
        If rowSource = "backend" Or rowSource = "client" Then
            BeginCase "CalyxAdviceFor: " & rowSource & " " & rowCode
            CheckEqual CalyxRetryText(CalyxAdviceFor(rowSource, rowCode)), entry.Item("retry"), "advice"
            EndCase
        End If
    Next entry
End Sub

' The shared test data has no WinHTTP cases, so these use the documented WinHTTP numbers.
Private Sub CaseWinHttpErrors()
    CheckWinHttpCase "timeout, bare number", WINHTTP_TIMEOUT_NUMBER, WINHTTP_TIMEOUT_NUMBER, CALYX_CODE_TIMEOUT, False
    CheckWinHttpCase "timeout, HRESULT", WINHTTP_TIMEOUT_HRESULT, WINHTTP_TIMEOUT_NUMBER, CALYX_CODE_TIMEOUT, False
    CheckWinHttpCase "TLS failure, bare number", WINHTTP_SECURE_FAILURE_NUMBER, WINHTTP_SECURE_FAILURE_NUMBER, CALYX_CODE_NETWORK_ERROR, True
    CheckWinHttpCase "TLS failure, HRESULT", WINHTTP_SECURE_FAILURE_HRESULT, WINHTTP_SECURE_FAILURE_NUMBER, CALYX_CODE_NETWORK_ERROR, True
    CheckWinHttpCase "cannot connect", WINHTTP_CANNOT_CONNECT_NUMBER, WINHTTP_CANNOT_CONNECT_NUMBER, CALYX_CODE_NETWORK_ERROR, False
End Sub

Private Sub CheckWinHttpCase(ByVal CaseName As String, ByVal Number As Long, _
    ByVal WantNumber As Long, ByVal WantCode As String, ByVal WantTlsHint As Boolean)
    Dim mapped As CalyxError
    BeginCase "CalyxMapWinHttpError: " & CaseName
    Set mapped = CalyxMapWinHttpError(Number, "a WinHTTP text")
    CheckEqual mapped.Source, "client", "Source"
    CheckEqual mapped.Code, WantCode, "Code"
    CheckEqual mapped.Status, 0, "Status"
    CheckEqual mapped.WinHttpError, WantNumber, "WinHttpError"
    CheckEqual mapped.RetryText, "retry_now", "RetryText"
    Check InStr(mapped.Detail, "WinHTTP error " & WantNumber & " (") > 0, _
        "Detail does not name the error number: " & mapped.Detail
    CheckEqual InStr(mapped.Detail, "TLS on Windows 7") > 0, WantTlsHint, "Detail names the TLS section"
    EndCase
End Sub

Private Function SampleText() As String
    ' "Ola cafe" with an a acute, an e acute, and the emoji U+1F600 as a surrogate pair.
    SampleText = "Ol" & ChrW$(&HE1) & " caf" & ChrW$(&HE9) & " " & ChrW$(&HD83D&) & ChrW$(&HDE00&)
End Function

Private Sub CaseUtf8RoundTrip()
    Dim encoded() As Byte
    Dim fixedBytes() As Byte
    Dim emptyBytes() As Byte
    BeginCase "UTF-8 round trip"
    encoded = CalyxUtf8Encode(SampleText())
    CheckEqual BytesToHex(encoded), SAMPLE_UTF8_HEX, "CalyxUtf8Encode"
    Check CalyxUtf8Decode(encoded) = SampleText(), "CalyxUtf8Decode does not give back the text of CalyxUtf8Encode"
    fixedBytes = HexToBytes(SAMPLE_UTF8_HEX)
    Check CalyxUtf8Decode(fixedBytes) = SampleText(), "CalyxUtf8Decode of the fixed bytes is not the sample text"
    emptyBytes = CalyxUtf8Encode("")
    CheckEqual BytesToHex(emptyBytes), "", "CalyxUtf8Encode of an empty text"
    CheckEqual CalyxUtf8Decode(emptyBytes), "", "CalyxUtf8Decode of no bytes"
    EndCase
End Sub

Private Sub CaseKeyFormat()
    Dim firstKey As String
    Dim secondKey As String
    BeginCase "CalyxNewIdempotencyKey format"
    firstKey = CalyxNewIdempotencyKey()
    secondKey = CalyxNewIdempotencyKey()
    CheckKeyFormat firstKey
    CheckKeyFormat secondKey
    Check firstKey <> secondKey, "two new keys are the same"
    EndCase
End Sub

' A UUID version 4 in lower case, with no braces: xxxxxxxx-xxxx-4xxx-[89ab]xxx-xxxxxxxxxxxx.
Private Sub CheckKeyFormat(ByVal Candidate As String)
    Dim i As Long
    Dim ch As String
    Dim valid As Boolean
    valid = (Len(Candidate) = 36)
    If valid Then
        For i = 1 To 36
            ch = Mid$(Candidate, i, 1)
            Select Case i
            Case 9, 14, 19, 24
                If ch <> "-" Then valid = False
            Case Else
                If InStr("0123456789abcdef", ch) = 0 Then valid = False
            End Select
        Next i
    End If
    If valid Then valid = (Mid$(Candidate, 15, 1) = "4") And (InStr("89ab", Mid$(Candidate, 20, 1)) > 0)
    Check valid, "the key """ & Candidate & """ is not a lower-case UUID version 4"
End Sub

' ---- The calls to the fake backend ----

Private Sub CaseUnauthenticated()
    BeginCase "401 with a wrong API key (GetInstance, scenario 1)"
    StartListener WRONG_API_KEY
    mListener.Client.GetInstance ScenarioId(1), "tag-401"
    If Waited() Then CheckFailed CALYX_OP_GET_INSTANCE, "tag-401", "backend", CALYX_CODE_UNAUTHENTICATED, 401, "do_not_retry"
End Sub

Private Sub CaseSendScenario1()
    Dim sentKey As String
    Dim sent As CalyxSendResult
    Dim lastRequest As Object
    BeginCase "scenario 1: SendMessage completes"
    StartListener TEST_API_KEY
    sentKey = mListener.Client.SendMessage(ScenarioId(1), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-send-1")
    CheckKeyFormat sentKey
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_SEND_MESSAGE, "tag-send-1", "CalyxSendResult") Then Exit Sub
    Set sent = mListener.Result
    CheckEqual sent.MessageId, SEND_MESSAGE_ID, "MessageId"
    CheckEqual sent.Replayed, False, "Replayed"
    CheckEqual sent.IdempotencyKey, sentKey, "IdempotencyKey"
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("method"), "POST", "method"
    CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & ScenarioId(1) & "/messages", "path"
    CheckHeader lastRequest, "Idempotency-Key", sentKey
    CheckAuthorization lastRequest
    CheckHeader lastRequest, "Content-Type", JSON_CONTENT_TYPE
    CheckHeader lastRequest, "User-Agent", "calyx-vb6-sdk/" & CALYX_SDK_VERSION
    CheckEqual lastRequest.Item("bodyHex"), AsciiHex("{""to"":""" & RECIPIENT_NUMBER & """,""text"":""" & PLAIN_TEXT & """}"), "bodyHex"
End Sub

Private Sub CaseGetInstanceScenario1()
    Dim instance As Object
    Dim hooks As Object
    Dim alert As Object
    Dim lastRequest As Object
    BeginCase "scenario 1: GetInstance completes with every field"
    StartListener TEST_API_KEY
    mListener.Client.GetInstance ScenarioId(1), "tag-get-1"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_GET_INSTANCE, "tag-get-1", "Dictionary") Then Exit Sub
    Set instance = mListener.Result
    CheckEqual instance.Item("id"), ScenarioId(1), "id"
    CheckEqual instance.Item("client_id"), CLIENT_ID, "client_id"
    CheckEqual instance.Item("display_name"), "Shop", "display_name"
    CheckEqual instance.Item("provider"), "META_CLOUD", "provider"
    CheckEqual instance.Item("send_only"), True, "send_only"
    CheckEqual instance.Item("enabled"), True, "enabled"
    CheckEqual instance.Item("state"), "active", "state"
    CheckEqual instance.Item("status"), "CONNECTED", "status"
    CheckEqual instance.Item("status_changed_at"), TIME_CHANGED, "status_changed_at"
    CheckEqual instance.Item("status_stale"), False, "status_stale"
    CheckEqual instance.Item("device_identity"), "34600000000:1@s.whatsapp.net", "device_identity"
    CheckEqual instance.Item("locale"), "es", "locale"
    CheckEqual instance.Item("phone_number_id"), "1234567890", "phone_number_id"
    Set hooks = instance.Item("webhooks")
    CheckEqual hooks.Count, 1, "webhooks.Count"
    If hooks.Count = 1 Then
        CheckEqual hooks.Item(1).Item("url"), "https://example.invalid/hook", "webhooks(1).url"
        CheckEqual hooks.Item(1).Item("event"), "message.received", "webhooks(1).event"
        CheckEqual hooks.Item(1).Item("has_secret"), True, "webhooks(1).has_secret"
    End If
    Set alert = instance.Item("alert")
    CheckEqual alert.Item("kind"), "disconnected", "alert.kind"
    CheckEqual alert.Item("since"), TIME_CHANGED, "alert.since"
    CheckEqual alert.Item("emailed_at"), TIME_EMAILED, "alert.emailed_at"
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("method"), "GET", "method"
    CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & ScenarioId(1), "path"
    CheckAuthorization lastRequest
    CheckNoHeader lastRequest, "Idempotency-Key"
    CheckNoHeader lastRequest, "Content-Type"
End Sub

Private Sub CaseListInstancesScenario1()
    Dim listAnswer As Object
    Dim items As Object
    Dim lastRequest As Object
    BeginCase "scenario 1: ListInstances completes"
    StartListener TEST_API_KEY
    mListener.Client.ListInstances "tag-list-1"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_LIST_INSTANCES, "tag-list-1", "Dictionary") Then Exit Sub
    Set listAnswer = mListener.Result
    Set items = listAnswer.Item("items")
    CheckEqual items.Count, 1, "items.Count"
    If items.Count = 1 Then
        CheckEqual items.Item(1).Item("id"), ScenarioId(1), "items(1).id"
        CheckEqual items.Item(1).Item("display_name"), "Shop", "items(1).display_name"
    End If
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("method"), "GET", "method"
    CheckEqual lastRequest.Item("path"), "/api/v1/instances", "path"
    CheckAuthorization lastRequest
End Sub

Private Sub CaseSendScenario2()
    Dim attempt As Long
    Dim returnedKey As String
    BeginCase "scenario 2: SendMessage, the first two answers are send_in_progress"
    mScenario2Key = CalyxNewIdempotencyKey()
    StartListener TEST_API_KEY
    For attempt = 1 To 2
        mListener.ClearEvent
        returnedKey = mListener.Client.SendMessage(ScenarioId(2), RECIPIENT_NUMBER, PLAIN_TEXT, mScenario2Key, "tag-send-2")
        CheckEqual returnedKey, mScenario2Key, "returned key, attempt " & attempt
        If Not Waited() Then Exit Sub
        If CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-send-2", "backend", CALYX_CODE_SEND_IN_PROGRESS, 409, "retry_later") Then
            CheckEqual mListener.Failure.IdempotencyKey, mScenario2Key, "Failure.IdempotencyKey, attempt " & attempt
            CheckEqual mListener.Failure.CanResend, True, "Failure.CanResend, attempt " & attempt
        End If
    Next attempt
End Sub

Private Sub CaseSendScenario2Replay()
    Dim sent As CalyxSendResult
    BeginCase "scenario 2: SendMessage, the third answer is replayed"
    If Len(mScenario2Key) = 0 Then
        Check False, "the first two requests of scenario 2 did not run"
        Exit Sub
    End If
    StartListener TEST_API_KEY
    mListener.Client.SendMessage ScenarioId(2), RECIPIENT_NUMBER, PLAIN_TEXT, mScenario2Key, "tag-send-2"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_SEND_MESSAGE, "tag-send-2", "CalyxSendResult") Then Exit Sub
    Set sent = mListener.Result
    CheckEqual sent.MessageId, SEND_MESSAGE_ID, "MessageId"
    CheckEqual sent.Replayed, True, "Replayed"
    CheckEqual sent.IdempotencyKey, mScenario2Key, "IdempotencyKey"
End Sub

Private Sub CaseSendScenario3()
    Dim sentKey As String
    BeginCase "scenario 3: SendMessage fails with send_outcome_unknown"
    StartListener TEST_API_KEY
    sentKey = mListener.Client.SendMessage(ScenarioId(3), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-send-3")
    If Not Waited() Then Exit Sub
    If CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-send-3", "backend", CALYX_CODE_SEND_OUTCOME_UNKNOWN, 504, "retry_now") Then
        CheckEqual mListener.Failure.IdempotencyKey, sentKey, "Failure.IdempotencyKey"
        CheckEqual mListener.Failure.CanResend, True, "Failure.CanResend"
    End If
End Sub

Private Sub CaseSendScenario4()
    Dim sentKey As String
    BeginCase "scenario 4: SendMessage gets an HTML 502"
    StartListener TEST_API_KEY
    sentKey = mListener.Client.SendMessage(ScenarioId(4), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-send-4")
    If Not Waited() Then Exit Sub
    If CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-send-4", "client", CALYX_CODE_INVALID_RESPONSE, 0, "retry_now") Then
        CheckEqual mListener.Failure.Detail, HTML_DETAIL, "Detail"
        CheckEqual mListener.Failure.IdempotencyKey, sentKey, "Failure.IdempotencyKey"
        CheckEqual mListener.Failure.CanResend, True, "Failure.CanResend"
    End If
End Sub

Private Sub CaseGetInstanceScenario4()
    BeginCase "scenario 4: GetInstance gets an HTML 502"
    StartListener TEST_API_KEY
    mListener.Client.GetInstance ScenarioId(4), "tag-get-4"
    If Not Waited() Then Exit Sub
    If CheckFailed(CALYX_OP_GET_INSTANCE, "tag-get-4", "client", CALYX_CODE_INVALID_RESPONSE, 0, "retry_now") Then
        CheckEqual mListener.Failure.Detail, HTML_DETAIL, "Detail"
    End If
End Sub

Private Sub CaseSendScenario5()
    Dim sentKey As String
    Dim startTime As Single
    BeginCase "scenario 5: SendMessage times out after SendTimeoutSeconds = 5"
    StartListener TEST_API_KEY
    mListener.Client.SendTimeoutSeconds = SHORT_LIMIT_SECONDS
    startTime = Timer
    sentKey = mListener.Client.SendMessage(ScenarioId(5), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-send-5")
    If Not Waited() Then Exit Sub
    Check SecondsSince(startTime) < SHORT_LIMIT_MAX_WAIT, "the event came after " & Format$(SecondsSince(startTime), "0.0") & " s"
    If CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-send-5", "client", CALYX_CODE_TIMEOUT, 0, "retry_now") Then
        CheckEqual mListener.Failure.WinHttpError, WINHTTP_TIMEOUT_NUMBER, "WinHttpError"
        CheckEqual mListener.Failure.IdempotencyKey, sentKey, "Failure.IdempotencyKey"
    End If
End Sub

Private Sub CaseGetInstanceScenario5()
    Dim startTime As Single
    BeginCase "scenario 5: GetInstance times out after CallTimeoutSeconds = 5"
    StartListener TEST_API_KEY
    mListener.Client.CallTimeoutSeconds = SHORT_LIMIT_SECONDS
    startTime = Timer
    mListener.Client.GetInstance ScenarioId(5), "tag-get-5"
    If Not Waited() Then Exit Sub
    Check SecondsSince(startTime) < SHORT_LIMIT_MAX_WAIT, "the event came after " & Format$(SecondsSince(startTime), "0.0") & " s"
    If CheckFailed(CALYX_OP_GET_INSTANCE, "tag-get-5", "client", CALYX_CODE_TIMEOUT, 0, "retry_now") Then
        CheckEqual mListener.Failure.WinHttpError, WINHTTP_TIMEOUT_NUMBER, "WinHttpError"
    End If
End Sub

Private Sub CaseGetInstanceScenario6()
    Dim instance As Object
    BeginCase "scenario 6: GetInstance with every nullable field null"
    StartListener TEST_API_KEY
    mListener.Client.GetInstance ScenarioId(6), "tag-get-6"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_GET_INSTANCE, "tag-get-6", "Dictionary") Then Exit Sub
    Set instance = mListener.Result
    CheckEqual instance.Item("id"), ScenarioId(6), "id"
    CheckEqual instance.Item("display_name"), "Shop", "display_name"
    CheckEqual instance.Item("status"), Null, "status"
    CheckEqual instance.Item("status_changed_at"), Null, "status_changed_at"
    CheckEqual instance.Item("device_identity"), Null, "device_identity"
    CheckEqual instance.Item("locale"), Null, "locale"
    CheckEqual instance.Item("phone_number_id"), Null, "phone_number_id"
    Check IsNull(instance.Item("alert")), "alert is not Null"
    CheckEqual instance.Item("status_stale"), True, "status_stale"
    CheckEqual instance.Item("webhooks").Count, 0, "webhooks.Count"
    CheckEqual CalyxField(instance, "locale", "none"), "none", "CalyxField of a Null field"
    CheckEqual CalyxField(instance, "absent_field", "none"), "none", "CalyxField of an absent field"
    CheckEqual CalyxField(instance, "display_name", "none"), "Shop", "CalyxField of a set field"
    Check CalyxField(instance, "alert", Nothing) Is Nothing, "CalyxField of a Null object field is not the default Nothing"
End Sub

' The request side: the body is ASCII JSON with \uXXXX escapes.
' The UTF-8 decoding of an answer is in CaseUtf8RoundTrip, because no fake answer has accents.
Private Sub CaseSendScenario7()
    Dim sentKey As String
    Dim lastRequest As Object
    BeginCase "scenario 7: SendMessage with accents and an emoji, bytes in /_fake/last"
    StartListener TEST_API_KEY
    sentKey = mListener.Client.SendMessage(ScenarioId(7), RECIPIENT_NUMBER, SampleText(), , "tag-send-7")
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_SEND_MESSAGE, "tag-send-7", "CalyxSendResult") Then Exit Sub
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & ScenarioId(7) & "/messages", "path"
    CheckHeader lastRequest, "Idempotency-Key", sentKey
    CheckAuthorization lastRequest
    CheckHeader lastRequest, "Content-Type", JSON_CONTENT_TYPE
    CheckEqual lastRequest.Item("bodyHex"), AsciiHex(SAMPLE_BODY_JSON), "bodyHex"
End Sub

Private Sub CaseSendScenario8()
    BeginCase "scenario 8: SendMessage gets a 302 and does not follow it"
    StartListener TEST_API_KEY
    mListener.Client.SendMessage ScenarioId(8), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-send-8"
    If Not Waited() Then Exit Sub
    If CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-send-8", "client", CALYX_CODE_INVALID_CONFIG, 0, "do_not_retry") Then
        CheckEqual mListener.Failure.CanResend, False, "Failure.CanResend"
    End If
End Sub

Private Sub CaseGetInstanceScenario8()
    BeginCase "scenario 8: GetInstance gets a 302 and does not follow it"
    StartListener TEST_API_KEY
    mListener.Client.GetInstance ScenarioId(8), "tag-get-8"
    If Not Waited() Then Exit Sub
    CheckFailed CALYX_OP_GET_INSTANCE, "tag-get-8", "client", CALYX_CODE_INVALID_CONFIG, 0, "do_not_retry"
End Sub

' AbortAll raises Failed with aborted before it returns, and no event comes after it.
Private Sub CaseAbortAll()
    Dim sentKey As String
    BeginCase "AbortAll on a pending SendMessage (scenario 5)"
    StartListener TEST_API_KEY
    mListener.Client.SendTimeoutSeconds = SHORT_LIMIT_SECONDS
    sentKey = mListener.Client.SendMessage(ScenarioId(5), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-abort")
    CheckEqual mListener.EventCount, 0, "events before AbortAll"
    mListener.Client.AbortAll
    ' No DoEvents between AbortAll and these checks: the event came inside AbortAll.
    If CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-abort", "client", CALYX_CODE_ABORTED, 0, "retry_now") Then
        CheckEqual mListener.Failure.IdempotencyKey, sentKey, "Failure.IdempotencyKey"
    End If
    ' Longer than the time limit of the aborted call, so a late time-out event would show.
    PumpEvents SHORT_LIMIT_SECONDS + 2
    CheckEqual mListener.EventCount, 1, "events after the wait"
End Sub

' A key with a space gives invalid_config at call time, with no network call.
Private Sub CaseBadApiKey()
    Dim raised As Boolean
    BeginCase "an ApiKey with a space gives invalid_config and sends nothing"
    StartListener "bad key"
    On Error Resume Next
    mListener.Client.GetInstance ScenarioId(1), "tag-bad-key"
    raised = (Err.Number <> 0)
    On Error GoTo 0
    Check raised, "GetInstance did not raise an error"
    CheckEqual mListener.Client.LastError.Code, CALYX_CODE_INVALID_CONFIG, "LastError.Code"
    CheckEqual mListener.Client.LastError.RetryText, "do_not_retry", "LastError.RetryText"
    Check InStr(mListener.Client.LastError.Detail, "bad key") = 0, "Detail shows the key"
    PumpEvents 1
    CheckEqual mListener.EventCount, 0, "events"
End Sub

' A text of 16 digits stays a JSON string in the body.
Private Sub CaseSendDigitsText()
    Dim lastRequest As Object
    BeginCase "scenario 1: SendMessage with a text of 16 digits sends a JSON string"
    StartListener TEST_API_KEY
    mListener.Client.SendMessage ScenarioId(1), RECIPIENT_NUMBER, DIGITS_TEXT, , "tag-digits"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_SEND_MESSAGE, "tag-digits", "CalyxSendResult") Then Exit Sub
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("bodyHex"), AsciiHex("{""to"":""" & RECIPIENT_NUMBER & """,""text"":""" & DIGITS_TEXT & """}"), "bodyHex"
End Sub

' WinHTTP would send this request, with the key, to evil.example in clear text.
Private Sub CaseUserInBaseUrl()
    Dim raised As Boolean
    BeginCase "a BaseUrl with ""@"" gives invalid_config and sends nothing"
    StartListener TEST_API_KEY
    mListener.Client.BaseUrl = "http://localhost:1@evil.example"
    On Error Resume Next
    mListener.Client.GetInstance ScenarioId(1), "tag-user-url"
    raised = (Err.Number <> 0)
    On Error GoTo 0
    Check raised, "GetInstance did not raise an error"
    CheckEqual mListener.Client.LastError.Code, CALYX_CODE_INVALID_CONFIG, "LastError.Code"
    CheckEqual mListener.Client.LastError.RetryText, "do_not_retry", "LastError.RetryText"
    PumpEvents 1
    CheckEqual mListener.EventCount, 0, "events"
End Sub

' A query or a fragment in BaseUrl would swallow the path of each request.
Private Sub CaseQueryInBaseUrl()
    BeginCase "a BaseUrl with ""?"" or ""#"" gives invalid_config and sends nothing"
    StartListener TEST_API_KEY
    CheckBaseUrlRefused mBaseUrl & "/?x=1"
    CheckBaseUrlRefused mBaseUrl & "/#top"
    PumpEvents 1
    CheckEqual mListener.EventCount, 0, "events"
End Sub

Private Sub CheckBaseUrlRefused(ByVal Url As String)
    Dim raised As Boolean
    mListener.Client.BaseUrl = Url
    On Error Resume Next
    mListener.Client.GetBulkJob ScenarioId(1), "tag-query-url"
    raised = (Err.Number <> 0)
    On Error GoTo 0
    Check raised, "GetBulkJob did not raise an error for " & Url
    If mListener.Client.LastError Is Nothing Then
        Check False, "LastError is Nothing for " & Url
    Else
        CheckEqual mListener.Client.LastError.Code, CALYX_CODE_INVALID_CONFIG, "LastError.Code for " & Url
    End If
End Sub

' ---- The bulk jobs ----

' Every bulk method on scenario 1, with the request in /_fake/last.
Private Sub CaseBulkScenario1(ByVal MethodName As String)
    Dim sentKey As String
    Dim bulkAnswer As Object
    Dim items As Object
    Dim lastRequest As Object
    BeginCase "scenario 1: " & MethodName & " completes"
    StartListener TEST_API_KEY
    sentKey = StartBulkCall(MethodName, 1, "tag-bulk-1")
    If IsCreate(MethodName) Then CheckKeyFormat sentKey
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(BulkOperation(MethodName), "tag-bulk-1", "Dictionary") Then Exit Sub
    Set bulkAnswer = mListener.Result
    If MethodName = BULK_LIST Then
        Set items = bulkAnswer.Item("items")
        CheckEqual items.Count, 1, "items.Count"
        If items.Count = 1 Then CheckJobScenario1 items.Item(1), JOB_ID
    ElseIf IsCreate(MethodName) Then
        CheckJobScenario1 bulkAnswer, JOB_ID
    Else
        ' The fake gives back the ID of the path.
        CheckJobScenario1 bulkAnswer, ScenarioId(1)
    End If
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("method"), BulkHttpMethod(MethodName), "method"
    CheckEqual lastRequest.Item("path"), BulkPath(MethodName, 1), "path"
    CheckAuthorization lastRequest
    CheckHeader lastRequest, "User-Agent", "calyx-vb6-sdk/" & CALYX_SDK_VERSION
    If IsCreate(MethodName) Then
        CheckHeader lastRequest, "Idempotency-Key", sentKey
        CheckHeader lastRequest, "Content-Type", JSON_CONTENT_TYPE
        If MethodName = BULK_SAME_TEXT Then
            CheckEqual lastRequest.Item("bodyHex"), AsciiHex(SAME_TEXT_BODY_JSON), "bodyHex"
        Else
            CheckEqual lastRequest.Item("bodyHex"), AsciiHex(CUSTOM_BODY_JSON), "bodyHex"
        End If
    Else
        CheckNoHeader lastRequest, "Idempotency-Key"
        CheckNoHeader lastRequest, "Content-Type"
        CheckEqual lastRequest.Item("bodyHex"), "", "bodyHex"
    End If
End Sub

' The job of scenario 1, with every field set.
Private Sub CheckJobScenario1(ByVal GotJob As Object, ByVal WantId As String)
    CheckEqual GotJob.Item("id"), WantId, "id"
    CheckEqual GotJob.Item("instance_id"), ScenarioId(1), "instance_id"
    CheckEqual GotJob.Item("status"), "RUNNING", "status"
    CheckEqual GotJob.Item("total_messages"), 10, "total_messages"
    CheckEqual GotJob.Item("sent_messages"), 4, "sent_messages"
    CheckEqual GotJob.Item("failed_messages"), 1, "failed_messages"
    CheckEqual GotJob.Item("progress_percent"), 50.5, "progress_percent"
    CheckEqual GotJob.Item("estimated_finish_at"), TIME_FINISH, "estimated_finish_at"
    CheckEqual GotJob.Item("created_at"), TIME_CREATED, "created_at"
    CheckEqual GotJob.Item("replayed"), True, "replayed"
End Sub

' Scenario 4 (an HTML 502), 5 (no answer, a time limit of 5 s) or 8 (a 302).
Private Sub CaseBulkFailure(ByVal MethodName As String, ByVal Scenario As Long)
    Dim sentKey As String
    Dim startTime As Single
    Dim expectedCode As String
    Dim expectedRetry As String
    Select Case Scenario
    Case 4
        expectedCode = CALYX_CODE_INVALID_RESPONSE
        expectedRetry = "retry_now"
    Case 5
        expectedCode = CALYX_CODE_TIMEOUT
        expectedRetry = "retry_now"
    Case Else
        expectedCode = CALYX_CODE_INVALID_CONFIG
        expectedRetry = "do_not_retry"
    End Select
    BeginCase "scenario " & Scenario & ": " & MethodName & " fails with " & expectedCode
    StartListener TEST_API_KEY
    If Scenario = 5 Then mListener.Client.CallTimeoutSeconds = SHORT_LIMIT_SECONDS
    startTime = Timer
    sentKey = StartBulkCall(MethodName, Scenario, "tag-bulk-fail")
    If Not Waited() Then Exit Sub
    If Scenario = 5 Then
        Check SecondsSince(startTime) < SHORT_LIMIT_MAX_WAIT, "the event came after " & Format$(SecondsSince(startTime), "0.0") & " s"
    End If
    If Not CheckFailed(BulkOperation(MethodName), "tag-bulk-fail", "client", expectedCode, 0, expectedRetry) Then Exit Sub
    If Scenario = 4 Then CheckEqual mListener.Failure.Detail, HTML_DETAIL, "Detail"
    If Scenario = 5 Then CheckEqual mListener.Failure.WinHttpError, WINHTTP_TIMEOUT_NUMBER, "WinHttpError"
    CheckEqual mListener.Failure.IdempotencyKey, sentKey, "Failure.IdempotencyKey"
End Sub

Private Sub CaseGetBulkJobScenario6()
    Dim nullJob As Object
    BeginCase "scenario 6: GetBulkJob with the nullable field null"
    StartListener TEST_API_KEY
    mListener.Client.GetBulkJob ScenarioId(6), "tag-bulk-6"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_GET_BULK_JOB, "tag-bulk-6", "Dictionary") Then Exit Sub
    Set nullJob = mListener.Result
    CheckEqual nullJob.Item("id"), ScenarioId(6), "id"
    CheckEqual nullJob.Item("status"), "RUNNING", "status"
    CheckEqual nullJob.Item("estimated_finish_at"), Null, "estimated_finish_at"
    CheckEqual nullJob.Item("replayed"), False, "replayed"
    CheckEqual CalyxField(nullJob, "estimated_finish_at", "none"), "none", "CalyxField of a Null field"
End Sub

' A wrong DelayMs gives invalid_config at call time, with the key in LastError and no request.
Private Sub CaseBadDelayMs()
    BeginCase "CreateBulkJobCustom with a wrong DelayMs gives invalid_config and sends nothing"
    StartListener TEST_API_KEY
    CheckDelayRefused "abc"
    CheckDelayRefused -1
    CheckDelayRefused 1.5
    CheckDelayRefused "1.5"
    PumpEvents 1
    CheckEqual mListener.EventCount, 0, "events"
End Sub

Private Sub CheckDelayRefused(ByVal DelayMs As Variant)
    Dim messageList As Collection
    Dim bulkMessage As CalyxBulkMessage
    Dim givenKey As String
    Dim raised As Boolean
    Set bulkMessage = New CalyxBulkMessage
    bulkMessage.Recipient = RECIPIENT_NUMBER
    bulkMessage.Text = PLAIN_TEXT
    bulkMessage.DelayMs = DelayMs
    Set messageList = New Collection
    messageList.Add bulkMessage
    givenKey = CalyxNewIdempotencyKey()
    On Error Resume Next
    mListener.Client.CreateBulkJobCustom ScenarioId(1), messageList, givenKey, "tag-bad-delay"
    raised = (Err.Number <> 0)
    On Error GoTo 0
    Check raised, "CreateBulkJobCustom did not raise an error for DelayMs " & CStr(DelayMs)
    If mListener.Client.LastError Is Nothing Then
        Check False, "LastError is Nothing for DelayMs " & CStr(DelayMs)
        Exit Sub
    End If
    CheckEqual mListener.Client.LastError.Code, CALYX_CODE_INVALID_CONFIG, "LastError.Code for DelayMs " & CStr(DelayMs)
    CheckEqual mListener.Client.LastError.IdempotencyKey, givenKey, "LastError.IdempotencyKey for DelayMs " & CStr(DelayMs)
End Sub

' A wrong Recipients or Messages gives invalid_config at call time, with no request.
Private Sub CaseBadBulkItems()
    Dim recipientList As Collection
    Dim messageList As Collection
    Dim raised As Boolean
    BeginCase "bulk methods refuse a wrong Recipients or Messages with invalid_config"
    StartListener TEST_API_KEY
    Set recipientList = New Collection
    recipientList.Add 34600000
    On Error Resume Next
    mListener.Client.CreateBulkJobSameText ScenarioId(1), recipientList, PLAIN_TEXT
    raised = (Err.Number <> 0)
    On Error GoTo 0
    CheckRefused "a Recipients item that is not a String", raised
    Set recipientList = Nothing
    On Error Resume Next
    mListener.Client.CreateBulkJobSameText ScenarioId(1), recipientList, PLAIN_TEXT
    raised = (Err.Number <> 0)
    On Error GoTo 0
    CheckRefused "Recipients = Nothing", raised
    Set messageList = Nothing
    On Error Resume Next
    mListener.Client.CreateBulkJobCustom ScenarioId(1), messageList
    raised = (Err.Number <> 0)
    On Error GoTo 0
    CheckRefused "Messages = Nothing", raised
    Set messageList = New Collection
    messageList.Add "not a message"
    On Error Resume Next
    mListener.Client.CreateBulkJobCustom ScenarioId(1), messageList
    raised = (Err.Number <> 0)
    On Error GoTo 0
    CheckRefused "a Messages item that is not a CalyxBulkMessage", raised
    PumpEvents 1
    CheckEqual mListener.EventCount, 0, "events"
End Sub

Private Sub CheckRefused(ByVal Label As String, ByVal WasRaised As Boolean)
    Check WasRaised, "no error was raised for " & Label
    If mListener.Client.LastError Is Nothing Then
        Check False, "LastError is Nothing for " & Label
        Exit Sub
    End If
    CheckEqual mListener.Client.LastError.Code, CALYX_CODE_INVALID_CONFIG, "LastError.Code for " & Label
End Sub

' DelayMs = Null means "not set": the body has no delay_ms, checked in the bytes of /_fake/last.
Private Sub CaseNullDelayMs()
    Dim messageList As Collection
    Dim bulkMessage As CalyxBulkMessage
    Dim lastRequest As Object
    BeginCase "CreateBulkJobCustom with DelayMs = Null leaves delay_ms out of the body"
    StartListener TEST_API_KEY
    Set bulkMessage = New CalyxBulkMessage
    bulkMessage.Recipient = RECIPIENT_NUMBER
    bulkMessage.Text = PLAIN_TEXT
    bulkMessage.DelayMs = Null
    Set messageList = New Collection
    messageList.Add bulkMessage
    mListener.Client.CreateBulkJobCustom ScenarioId(1), messageList, , "tag-null-delay"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_CREATE_BULK_JOB, "tag-null-delay", "Dictionary") Then Exit Sub
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("bodyHex"), AsciiHex("{""messages"":[{""to"":""" & RECIPIENT_NUMBER & """,""text"":""" & PLAIN_TEXT & """}]}"), "bodyHex"
End Sub

' A second call within 25 s gets the saved answer before the method returns, and sends no
' request. After the change call (a cancel or a create), the same call sends a request again.
Private Sub CaseSavedAnswer(ByVal MethodName As String, ByVal ChangeName As String)
    Dim firstAnswer As Object
    Dim lastRequest As Object
    BeginCase "saved answers: " & MethodName & " within 25 s, then after " & ChangeName
    StartListener TEST_API_KEY
    StartBulkCall MethodName, 1, "tag-saved-1"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(BulkOperation(MethodName), "tag-saved-1", "Dictionary") Then Exit Sub
    Set firstAnswer = mListener.Result
    ' A request on another route, so /_fake/last shows that the next call sends nothing.
    mListener.ClearEvent
    mListener.Client.GetInstance ScenarioId(1), "tag-saved-mark"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_GET_INSTANCE, "tag-saved-mark", "Dictionary") Then Exit Sub
    mListener.ClearEvent
    StartBulkCall MethodName, 1, "tag-saved-2"
    ' No DoEvents before these checks: the saved answer comes inside the method.
    If CheckCompleted(BulkOperation(MethodName), "tag-saved-2", "Dictionary") Then
        Check mListener.Result Is firstAnswer, "the saved answer is not the same Dictionary"
    End If
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & ScenarioId(1), "path of the last request after the saved answer"
    mListener.ClearEvent
    StartBulkCall ChangeName, 1, "tag-saved-change"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(BulkOperation(ChangeName), "tag-saved-change", "Dictionary") Then Exit Sub
    mListener.ClearEvent
    StartBulkCall MethodName, 1, "tag-saved-3"
    CheckEqual mListener.EventCount, 0, "events inside " & MethodName & " after " & ChangeName
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(BulkOperation(MethodName), "tag-saved-3", "Dictionary") Then Exit Sub
    Check Not (mListener.Result Is firstAnswer), "the answer after " & ChangeName & " is the saved one"
    ' The create and the list have the same path, so the method shows which one came last.
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("method"), BulkHttpMethod(MethodName), "method of the last request after " & ChangeName
    CheckEqual lastRequest.Item("path"), BulkPath(MethodName, 1), "path of the last request after " & ChangeName
End Sub

' Starts one bulk method on the ID of a scenario. Returns the idempotency key, or "" for a
' method with no key.
Private Function StartBulkCall(ByVal MethodName As String, ByVal Scenario As Long, _
    ByVal TagText As String) As String
    Select Case MethodName
    Case BULK_SAME_TEXT
        StartBulkCall = mListener.Client.CreateBulkJobSameText(ScenarioId(Scenario), TwoRecipients(), PLAIN_TEXT, , TagText)
    Case BULK_CUSTOM
        StartBulkCall = mListener.Client.CreateBulkJobCustom(ScenarioId(Scenario), TwoMessages(), , TagText)
    Case BULK_GET
        mListener.Client.GetBulkJob ScenarioId(Scenario), TagText
    Case BULK_LIST
        mListener.Client.ListBulkJobs ScenarioId(Scenario), TagText
    Case BULK_CANCEL
        mListener.Client.CancelBulkJob ScenarioId(Scenario), TagText
    Case Else
        Err.Raise vbObjectError + 1001, "TestCases", "No bulk method " & MethodName & "."
    End Select
End Function

Private Function IsCreate(ByVal MethodName As String) As Boolean
    IsCreate = (MethodName = BULK_SAME_TEXT Or MethodName = BULK_CUSTOM)
End Function

Private Function BulkOperation(ByVal MethodName As String) As String
    Select Case MethodName
    Case BULK_GET: BulkOperation = CALYX_OP_GET_BULK_JOB
    Case BULK_LIST: BulkOperation = CALYX_OP_LIST_BULK_JOBS
    Case BULK_CANCEL: BulkOperation = CALYX_OP_CANCEL_BULK_JOB
    Case Else: BulkOperation = CALYX_OP_CREATE_BULK_JOB
    End Select
End Function

' Written here, not taken from CalyxData.bas, so the test does not depend on the SDK.
Private Function BulkHttpMethod(ByVal MethodName As String) As String
    If MethodName = BULK_GET Or MethodName = BULK_LIST Then
        BulkHttpMethod = "GET"
    Else
        BulkHttpMethod = "POST"
    End If
End Function

Private Function BulkPath(ByVal MethodName As String, ByVal Scenario As Long) As String
    Select Case MethodName
    Case BULK_GET: BulkPath = "/api/v1/bulk-jobs/" & ScenarioId(Scenario)
    Case BULK_CANCEL: BulkPath = "/api/v1/bulk-jobs/" & ScenarioId(Scenario) & "/cancel"
    Case Else: BulkPath = "/api/v1/instances/" & ScenarioId(Scenario) & "/bulk-jobs"
    End Select
End Function

Private Function TwoRecipients() As Collection
    Dim recipientList As Collection
    Set recipientList = New Collection
    recipientList.Add RECIPIENT_NUMBER
    recipientList.Add SECOND_RECIPIENT
    Set TwoRecipients = recipientList
End Function

Private Function TwoMessages() As Collection
    Dim messageList As Collection
    Dim bulkMessage As CalyxBulkMessage
    Set messageList = New Collection
    Set bulkMessage = New CalyxBulkMessage
    bulkMessage.Recipient = RECIPIENT_NUMBER
    bulkMessage.Text = PLAIN_TEXT
    bulkMessage.DelayMs = "500"
    messageList.Add bulkMessage
    Set bulkMessage = New CalyxBulkMessage
    bulkMessage.Recipient = SECOND_RECIPIENT
    bulkMessage.Text = PLAIN_TEXT
    messageList.Add bulkMessage
    Set TwoMessages = messageList
End Function

' ---- Resend ----

' The fake answers send_in_progress twice for a new key, then the first answer as replayed.
Private Sub CaseResendScenario2()
    Dim resendKey As String
    Dim returnedKey As String
    Dim lastFailure As CalyxError
    Dim sent As CalyxSendResult
    Dim lastRequest As Object
    BeginCase "scenario 2: Resend twice gives send_in_progress, then replayed"
    resendKey = CalyxNewIdempotencyKey()
    StartListener TEST_API_KEY
    mListener.Client.SendMessage ScenarioId(2), RECIPIENT_NUMBER, PLAIN_TEXT, resendKey, "tag-resend-2"
    If Not Waited() Then Exit Sub
    If Not CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-resend-2", "backend", CALYX_CODE_SEND_IN_PROGRESS, 409, "retry_later") Then Exit Sub
    Set lastFailure = mListener.Failure
    mListener.ClearEvent
    returnedKey = mListener.Client.Resend(lastFailure)
    CheckEqual returnedKey, resendKey, "key of the first Resend"
    If Not Waited() Then Exit Sub
    If Not CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-resend-2", "backend", CALYX_CODE_SEND_IN_PROGRESS, 409, "retry_later") Then Exit Sub
    CheckEqual mListener.Failure.IdempotencyKey, resendKey, "Failure.IdempotencyKey of the first Resend"
    CheckHeader FakeLast(), "Idempotency-Key", resendKey
    Set lastFailure = mListener.Failure
    mListener.ClearEvent
    returnedKey = mListener.Client.Resend(lastFailure)
    CheckEqual returnedKey, resendKey, "key of the second Resend"
    If Not Waited() Then Exit Sub
    If Not CheckCompleted(CALYX_OP_SEND_MESSAGE, "tag-resend-2", "CalyxSendResult") Then Exit Sub
    Set sent = mListener.Result
    CheckEqual sent.MessageId, SEND_MESSAGE_ID, "MessageId"
    CheckEqual sent.Replayed, True, "Replayed"
    CheckEqual sent.IdempotencyKey, resendKey, "IdempotencyKey"
    ' The same request: the same path, key and body.
    Set lastRequest = FakeLast()
    CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & ScenarioId(2) & "/messages", "path"
    CheckHeader lastRequest, "Idempotency-Key", resendKey
    CheckEqual lastRequest.Item("bodyHex"), AsciiHex(PlainSendBody()), "bodyHex"
End Sub

' Resend refuses do_not_retry, check_if_sent, Nothing and a failure with no request.
Private Sub CaseResendRefused()
    Dim sentKey As String
    Dim badFailure As CalyxError
    Dim madeRequest As CalyxRequest
    BeginCase "Resend refuses a failure that it must not send again"
    StartListener TEST_API_KEY
    ' Scenario 8 gives invalid_config, with the advice do_not_retry.
    sentKey = mListener.Client.SendMessage(ScenarioId(8), RECIPIENT_NUMBER, PLAIN_TEXT, , "tag-resend-8")
    If Not Waited() Then Exit Sub
    If Not CheckFailed(CALYX_OP_SEND_MESSAGE, "tag-resend-8", "client", CALYX_CODE_INVALID_CONFIG, 0, "do_not_retry") Then Exit Sub
    Set badFailure = mListener.Failure
    mListener.ClearEvent
    CheckResendRefused badFailure, "do_not_retry", sentKey
    ' The fake cannot give idempotency_key_outcome_unknown, so the test makes the failure with
    ' the Friend members of the SDK.
    Set madeRequest = New CalyxRequest
    madeRequest.Operation = CALYX_OP_SEND_MESSAGE
    madeRequest.Method = "POST"
    madeRequest.Path = "/api/v1/instances/" & ScenarioId(1) & "/messages"
    madeRequest.BodyJson = PlainSendBody()
    madeRequest.IdempotencyKey = CalyxNewIdempotencyKey()
    madeRequest.TimeoutSeconds = SHORT_LIMIT_SECONDS
    Set badFailure = New CalyxError
    badFailure.Source = "backend"
    badFailure.Code = CALYX_CODE_IDEMPOTENCY_KEY_OUTCOME_UNKNOWN
    badFailure.Status = 409
    badFailure.AttachRequest madeRequest
    CheckEqual badFailure.RetryText, "check_if_sent", "RetryText of the made failure"
    CheckResendRefused badFailure, "check_if_sent", madeRequest.IdempotencyKey
    CheckResendRefused Nothing, "Nothing", ""
    ' A failure from CalyxClientFailure has no request.
    CheckResendRefused CalyxClientFailure(CALYX_CODE_NETWORK_ERROR, "made by the test"), "no request", ""
    PumpEvents 1
    CheckEqual mListener.EventCount, 0, "events after the refused Resend calls"
End Sub

Private Sub CheckResendRefused(ByVal Failure As CalyxError, ByVal What As String, ByVal WantKey As String)
    Dim raised As Boolean
    On Error Resume Next
    mListener.Client.Resend Failure
    raised = (Err.Number <> 0)
    On Error GoTo 0
    Check raised, "Resend did not raise an error for " & What
    If mListener.Client.LastError Is Nothing Then
        Check False, "LastError is Nothing for " & What
        Exit Sub
    End If
    CheckEqual mListener.Client.LastError.Code, CALYX_CODE_INVALID_CONFIG, "LastError.Code for " & What
    CheckEqual mListener.Client.LastError.IdempotencyKey, WantKey, "LastError.IdempotencyKey for " & What
    CheckEqual mListener.Client.LastError.CanResend, False, "LastError.CanResend for " & What
End Sub

' The body of a SendMessage to RECIPIENT_NUMBER with PLAIN_TEXT.
Private Function PlainSendBody() As String
    PlainSendBody = "{""to"":""" & RECIPIENT_NUMBER & """,""text"":""" & PLAIN_TEXT & """}"
End Function

' ---- The blocking methods ----

' One blocking method on one scenario. The method returns the result, or raises
' vbObjectError + 513 with the CalyxError in LastError. A blocking method raises no event.
' Scenario 3 exists only on the send route, so GetInstanceSync completes on it.
Private Sub CaseSyncScenario(ByVal MethodName As String, ByVal Scenario As Long)
    Dim syncClient As CalyxClient
    Dim syncKey As String
    Dim syncOperation As String
    Dim syncResult As Object
    Dim errNumber As Long
    Dim errSource As String
    Dim startTime As Single
    BeginCase "scenario " & Scenario & ": " & MethodName
    StartListener TEST_API_KEY
    Set syncClient = mListener.Client
    ' Scenario 5 stays silent for 20 s. ResendSync uses the limit of the made request.
    If Scenario = 5 Then
        syncClient.SendTimeoutSeconds = SHORT_LIMIT_SECONDS
        syncClient.CallTimeoutSeconds = SHORT_LIMIT_SECONDS
    End If
    If MethodName = SYNC_GET Then
        syncOperation = CALYX_OP_GET_INSTANCE
    Else
        syncOperation = CALYX_OP_SEND_MESSAGE
        syncKey = CalyxNewIdempotencyKey()
    End If
    startTime = Timer
    On Error Resume Next
    Select Case MethodName
    Case SYNC_SEND
        Set syncResult = syncClient.SendMessageSync(ScenarioId(Scenario), RECIPIENT_NUMBER, PLAIN_TEXT, syncKey, SYNC_TAG)
    Case SYNC_GET
        Set syncResult = syncClient.GetInstanceSync(ScenarioId(Scenario), SYNC_TAG)
    Case SYNC_RESEND
        Set syncResult = syncClient.ResendSync(MadeSendFailure(Scenario, syncKey))
    End Select
    errNumber = Err.Number
    errSource = Err.Source
    On Error GoTo 0
    If Scenario = 5 Then
        Check SecondsSince(startTime) < SHORT_LIMIT_MAX_WAIT, "the method returned after " & Format$(SecondsSince(startTime), "0.0") & " s"
    End If
    CheckEqual mListener.EventCount, 0, "events"
    Select Case Scenario
    Case 1
        CheckSyncDone MethodName, errNumber, syncResult, ScenarioId(1), syncKey
    Case 3
        If MethodName = SYNC_GET Then
            CheckSyncDone MethodName, errNumber, syncResult, ScenarioId(3), syncKey
        ElseIf CheckSyncFailure(errNumber, errSource, syncOperation, "backend", CALYX_CODE_SEND_OUTCOME_UNKNOWN, 504, syncKey) Then
            CheckEqual syncClient.LastError.RetryText, "retry_now", "LastError.RetryText"
        End If
    Case 4
        If CheckSyncFailure(errNumber, errSource, syncOperation, "client", CALYX_CODE_INVALID_RESPONSE, 0, syncKey) Then
            CheckEqual syncClient.LastError.Detail, HTML_DETAIL, "LastError.Detail"
        End If
    Case 5
        If CheckSyncFailure(errNumber, errSource, syncOperation, "client", CALYX_CODE_TIMEOUT, 0, syncKey) Then
            CheckEqual syncClient.LastError.WinHttpError, WINHTTP_TIMEOUT_NUMBER, "LastError.WinHttpError"
        End If
    End Select
End Sub

' Checks a blocking call that returned. The request in /_fake/last must have the same headers
' as the asynchronous path.
Private Sub CheckSyncDone(ByVal MethodName As String, ByVal RaisedNumber As Long, _
    ByVal GotResult As Object, ByVal WantId As String, ByVal WantKey As String)
    Dim sent As CalyxSendResult
    Dim lastRequest As Object
    If RaisedNumber <> 0 Then
        Check False, "the method raised VB6 error " & RaisedNumber & ": " & FailureText(mListener.Client.LastError)
        Exit Sub
    End If
    Check mListener.Client.LastError Is Nothing, "LastError is not Nothing"
    Set lastRequest = FakeLast()
    CheckAuthorization lastRequest
    CheckHeader lastRequest, "User-Agent", "calyx-vb6-sdk/" & CALYX_SDK_VERSION
    If MethodName = SYNC_GET Then
        CheckEqual TypeName(GotResult), "Dictionary", "TypeName of the result"
        If TypeName(GotResult) = "Dictionary" Then CheckEqual GotResult.Item("id"), WantId, "id"
        CheckEqual lastRequest.Item("method"), "GET", "method"
        CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & WantId, "path"
        CheckNoHeader lastRequest, "Idempotency-Key"
        CheckNoHeader lastRequest, "Content-Type"
        Exit Sub
    End If
    CheckEqual TypeName(GotResult), "CalyxSendResult", "TypeName of the result"
    If TypeName(GotResult) = "CalyxSendResult" Then
        Set sent = GotResult
        CheckEqual sent.MessageId, SEND_MESSAGE_ID, "MessageId"
        CheckEqual sent.Replayed, False, "Replayed"
        CheckEqual sent.IdempotencyKey, WantKey, "IdempotencyKey"
    End If
    CheckEqual lastRequest.Item("method"), "POST", "method"
    CheckEqual lastRequest.Item("path"), "/api/v1/instances/" & WantId & "/messages", "path"
    CheckHeader lastRequest, "Idempotency-Key", WantKey
    CheckHeader lastRequest, "Content-Type", JSON_CONTENT_TYPE
    CheckEqual lastRequest.Item("bodyHex"), AsciiHex(PlainSendBody()), "bodyHex"
End Sub

' Checks the VB6 error and LastError of a blocking call that failed. Returns True when
' LastError has the expected code.
Private Function CheckSyncFailure(ByVal RaisedNumber As Long, ByVal RaisedSource As String, _
    ByVal WantOperation As String, ByVal WantSource As String, ByVal WantCode As String, _
    ByVal WantStatus As Long, ByVal WantKey As String) As Boolean
    Dim got As CalyxError
    CheckEqual RaisedNumber, vbObjectError + 513, "Err.Number"
    CheckEqual RaisedSource, "Calyx", "Err.Source"
    Set got = mListener.Client.LastError
    If got Is Nothing Then
        Check False, "LastError is Nothing"
        Exit Function
    End If
    CheckEqual got.Operation, WantOperation, "LastError.Operation"
    CheckEqual got.Tag, SYNC_TAG, "LastError.Tag"
    CheckEqual got.Source, WantSource, "LastError.Source"
    CheckEqual got.Code, WantCode, "LastError.Code"
    CheckEqual got.Status, WantStatus, "LastError.Status"
    CheckEqual got.IdempotencyKey, WantKey, "LastError.IdempotencyKey"
    CheckEqual got.CanResend, True, "LastError.CanResend"
    If got.Code <> WantCode Then Check False, "LastError: " & FailureText(got)
    CheckSyncFailure = (got.Code = WantCode)
End Function

' A network_error failure of a send to one scenario, made with the Friend members of the SDK.
' Its advice is retry_now, so ResendSync sends its request.
Private Function MadeSendFailure(ByVal Scenario As Long, ByVal Key As String) As CalyxError
    Dim madeRequest As CalyxRequest
    Dim madeFailure As CalyxError
    Set madeRequest = New CalyxRequest
    madeRequest.Operation = CALYX_OP_SEND_MESSAGE
    madeRequest.Method = "POST"
    madeRequest.Path = "/api/v1/instances/" & ScenarioId(Scenario) & "/messages"
    madeRequest.BodyJson = PlainSendBody()
    madeRequest.IdempotencyKey = Key
    madeRequest.TimeoutSeconds = SHORT_LIMIT_SECONDS
    madeRequest.Tag = SYNC_TAG
    Set madeFailure = CalyxClientFailure(CALYX_CODE_NETWORK_ERROR, "made by the test")
    madeFailure.AttachRequest madeRequest
    Set MadeSendFailure = madeFailure
End Function

' ---- Helpers ----

Private Function ScenarioId(ByVal Scenario As Long) As String
    ScenarioId = SCENARIO_ID_PREFIX & Scenario
End Function

' A new client and listener for each case. The client never writes the key into results.txt.
Private Sub StartListener(ByVal ApiKey As String)
    Dim newClient As CalyxClient
    StopListener
    Set newClient = New CalyxClient
    newClient.BaseUrl = mBaseUrl
    newClient.ApiKey = ApiKey
    Set mListener = New TestListener
    mListener.Attach newClient
End Sub

' A pending call keeps the client alive, so AbortAll runs before the listener lets go.
Private Sub StopListener()
    On Error Resume Next
    If mListener Is Nothing Then Exit Sub
    If Not mListener.Client Is Nothing Then mListener.Client.AbortAll
    mListener.Detach
    Set mListener = Nothing
End Sub

Private Function Waited() As Boolean
    Waited = WaitForEvent(mListener, WAIT_LIMIT_SECONDS)
    If Not Waited Then Check False, "no event within " & WAIT_LIMIT_SECONDS & " s"
End Function

' Checks one Completed event. Returns True when the result has the expected type.
Private Function CheckCompleted(ByVal WantOperation As String, ByVal WantTag As String, _
    ByVal WantResultType As String) As Boolean
    CheckEqual mListener.EventCount, 1, "number of events"
    If mListener.EventName <> "Completed" Then
        Check False, "expected Completed, got " & mListener.EventName & ": " & FailureText(mListener.Failure)
        Exit Function
    End If
    CheckEqual mListener.Operation, WantOperation, "Operation"
    CheckEqual mListener.Tag, WantTag, "Tag"
    CheckEqual TypeName(mListener.Result), WantResultType, "TypeName of Result"
    CheckCompleted = (TypeName(mListener.Result) = WantResultType)
End Function

' Checks one Failed event. Returns True when the failure has the expected code.
Private Function CheckFailed(ByVal WantOperation As String, ByVal WantTag As String, _
    ByVal WantSource As String, ByVal WantCode As String, ByVal WantStatus As Long, _
    ByVal WantRetry As String) As Boolean
    Dim got As CalyxError
    CheckEqual mListener.EventCount, 1, "number of events"
    If mListener.EventName <> "Failed" Then
        Check False, "expected Failed, got " & mListener.EventName
        Exit Function
    End If
    Set got = mListener.Failure
    CheckEqual mListener.Operation, WantOperation, "Operation"
    CheckEqual mListener.Tag, WantTag, "Tag"
    CheckEqual got.Operation, WantOperation, "Failure.Operation"
    CheckEqual got.Tag, WantTag, "Failure.Tag"
    CheckEqual got.Source, WantSource, "Failure.Source"
    CheckEqual got.Code, WantCode, "Failure.Code"
    CheckEqual got.Status, WantStatus, "Failure.Status"
    CheckEqual got.RetryText, WantRetry, "Failure.RetryText"
    If got.Code <> WantCode Then Check False, "Failure: " & FailureText(got)
    CheckFailed = (got.Code = WantCode)
End Function

Private Function FailureText(ByVal Failure As CalyxError) As String
    If Failure Is Nothing Then
        FailureText = "(no failure)"
    Else
        FailureText = Failure.Source & " " & Failure.Code & " " & Failure.Status & ", " & Failure.Detail
    End If
End Function

' The last request that the fake backend got: {method, path, headers, bodyHex}.
Private Function FakeLast() As Object
    Dim http As WinHttp.WinHttpRequest
    Dim lastBytes() As Byte
    Set http = New WinHttp.WinHttpRequest
    http.Open "GET", mBaseUrl & "/_fake/last", False
    http.Send
    lastBytes = http.ResponseBody
    Set FakeLast = CalyxParseJson(CalyxUtf8Decode(lastBytes))
End Function

' Go writes header names in canonical form, for example Idempotency-Key.
Private Sub CheckHeader(ByVal Recorded As Object, ByVal HeaderName As String, ByVal WantValue As String)
    Dim headers As Object
    Set headers = Recorded.Item("headers")
    If headers.Exists(HeaderName) Then
        CheckEqual headers.Item(HeaderName), WantValue, "header " & HeaderName
    Else
        Check False, "no header " & HeaderName
    End If
End Sub

Private Sub CheckNoHeader(ByVal Recorded As Object, ByVal HeaderName As String)
    Check Not Recorded.Item("headers").Exists(HeaderName), "the request has the header " & HeaderName
End Sub

' The message never shows the value, because it holds the API key.
Private Sub CheckAuthorization(ByVal Recorded As Object)
    Dim headers As Object
    Set headers = Recorded.Item("headers")
    If headers.Exists("Authorization") Then
        Check headers.Item("Authorization") = "Bearer " & TEST_API_KEY, _
            "the Authorization header is not ""Bearer "" and the test key (the value is not shown)"
    Else
        Check False, "no header Authorization"
    End If
End Sub

' A header that is absent gives "".
Private Function TextOf(ByVal Dict As Object, ByVal Name As String) As String
    If Dict.Exists(Name) Then TextOf = Dict.Item(Name)
End Function

' Reads a file of the testdata folder as UTF-8 and parses it.
Private Function ReadJsonFile(ByVal FileName As String) As Object
    Dim filePath As String
    Dim fileNumber As Integer
    Dim content() As Byte
    filePath = mDataFolder & FileName
    If Len(Dir$(filePath)) = 0 Then Err.Raise vbObjectError + 1000, "TestCases", "The file " & filePath & " does not exist."
    fileNumber = FreeFile
    Open filePath For Binary Access Read As #fileNumber
    If LOF(fileNumber) > 0 Then
        ReDim content(0 To LOF(fileNumber) - 1)
        Get #fileNumber, , content
    End If
    Close #fileNumber
    Set ReadJsonFile = CalyxParseJson(CalyxUtf8Decode(content))
End Function

' Lower-case hex, like the bodyHex of the fake backend.
Private Function BytesToHex(Bytes() As Byte) As String
    Dim i As Long
    Dim hexText As String
    For i = LBound(Bytes) To UBound(Bytes)
        hexText = hexText & LCase$(Right$("0" & Hex$(Bytes(i)), 2))
    Next i
    BytesToHex = hexText
End Function

Private Function HexToBytes(ByVal HexDigits As String) As Byte()
    Dim madeBytes() As Byte
    Dim i As Long
    ReDim madeBytes(0 To Len(HexDigits) \ 2 - 1)
    For i = 0 To Len(HexDigits) \ 2 - 1
        madeBytes(i) = CByte("&H" & Mid$(HexDigits, i * 2 + 1, 2))
    Next i
    HexToBytes = madeBytes
End Function

' The hex of an ASCII text, without the SDK, so the expected bytes do not depend on it.
Private Function AsciiHex(ByVal Text As String) As String
    Dim i As Long
    Dim hexText As String
    For i = 1 To Len(Text)
        hexText = hexText & LCase$(Right$("0" & Hex$(AscW(Mid$(Text, i, 1))), 2))
    Next i
    AsciiHex = hexText
End Function
