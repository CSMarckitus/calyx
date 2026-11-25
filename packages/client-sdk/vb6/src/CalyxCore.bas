Attribute VB_Name = "CalyxCore"
' Calyx SDK for VB6
Option Explicit

Public Const CALYX_SDK_VERSION As String = "0.1.0"

' The shared functions of the SDK: text encoding, idempotency keys, checks, field access, error
' mapping and answer handling. Most are pure, so the VB6 tests can call them directly.
' CalyxPrepareRequest and CalyxSendRequest open and send a request.

' The time-out number 12002 is confirmed: the Windows run of 2026-11-25 passed the time-out cases
' (tests/TestCases.bas, groups 15, 16, 58, 62 and 66). Provisional: the other numbers, from the
' design. They are not checked on their own; the general run passed.
Private Const WINHTTP_TIMEOUT As Long = 12002
Private Const WINHTTP_TLS_FIRST As Long = 12037
Private Const WINHTTP_TLS_LAST As Long = 12045
Private Const WINHTTP_SECURE_CHANNEL_ERROR As Long = 12157
Private Const WINHTTP_SECURE_FAILURE As Long = 12175
Private Const WINHTTP_CERT_REV_FAILED As Long = 12057
Private Const WINHTTP_SECURE_INVALID_CERT As Long = 12169
Private Const WINHTTP_SECURE_CERT_REVOKED As Long = 12170

' An HRESULT of the Win32 facility has the form &H8007xxxx.
Private Const HRESULT_FACILITY_MASK As Long = &HFFFF0000
Private Const HRESULT_WIN32 As Long = &H80070000
' WinHTTP raises its errors as an HRESULT &H8007xxxx, or as the plain number 12000 to 12200.
Private Const WINHTTP_ERROR_FIRST As Long = 12000
Private Const WINHTTP_ERROR_LAST As Long = 12200

' The request options of both paths: CalyxCall (asynchronous) and SendNow of CalyxClient.
' Option(6) = False is confirmed: groups 19, 20 and 40 to 44 get a 302 and do not follow it.
' Provisional: Option(9) = TLS 1.2 and the time limits. Each request sets them and the run passed,
' but the tests use plain http, so they are not checked on their own; the general run passed.
Private Const OPTION_SECURE_PROTOCOLS As Long = 9
Private Const OPTION_ENABLE_REDIRECTS As Long = 6
Private Const PROTOCOL_TLS12 As Long = &H800&
Private Const RESOLVE_TIMEOUT_MS As Long = 30000
Private Const CONNECT_TIMEOUT_MS As Long = 30000
Private Const ACCEPT_TYPES As String = "application/json, application/problem+json"
Private Const JSON_CONTENT_TYPE As String = "application/json; charset=utf-8"

Private Const CP_UTF8 As Long = 65001
Private Const KEY_MAX_LENGTH As Long = 255
Private Const KEY_RULE As String = "The idempotency key must have 1 to 255 characters from ! to ~ (printable ASCII with no space)."
Private Const URL_RULE As String = "BaseUrl must start with https://. http:// is allowed only for localhost and 127.0.0.1."

Private Type GuidData
    Data1 As Long
    Data2 As Integer
    Data3 As Integer
    Data4(0 To 7) As Byte
End Type

Private Declare Function CoCreateGuid Lib "ole32" (ByRef Guid As GuidData) As Long
Private Declare Function WideCharToMultiByte Lib "kernel32" (ByVal CodePage As Long, _
    ByVal dwFlags As Long, ByVal lpWideCharStr As Long, ByVal cchWideChar As Long, _
    ByVal lpMultiByteStr As Long, ByVal cbMultiByte As Long, ByVal lpDefaultChar As Long, _
    ByVal lpUsedDefaultChar As Long) As Long
Private Declare Function MultiByteToWideChar Lib "kernel32" (ByVal CodePage As Long, _
    ByVal dwFlags As Long, ByVal lpMultiByteStr As Long, ByVal cbMultiByte As Long, _
    ByVal lpWideCharStr As Long, ByVal cchWideChar As Long) As Long
' For the saved answers of CalyxClient. A Private Declare is not visible in a class, so
' CalyxTickCount wraps it.
Private Declare Function GetTickCount Lib "kernel32" () As Long

' The milliseconds since Windows started. The Long is negative after 24.8 days and wraps to 0
' after 49.7 days.
Public Function CalyxTickCount() As Long
    CalyxTickCount = GetTickCount()
End Function

' A UUID version 4 in lower case, with no braces. CoCreateGuid makes random (version 4) GUIDs.
Public Function CalyxNewIdempotencyKey() As String
    Dim g As GuidData
    Dim hr As Long
    Dim s As String
    Dim i As Long
    hr = CoCreateGuid(g)
    If hr <> 0 Then FailCheck "CoCreateGuid failed with HRESULT &H" & Hex$(hr) & "."
    s = HexPad(g.Data1, 8) & "-" & HexPad(g.Data2, 4) & "-" & HexPad(g.Data3, 4) & "-"
    For i = 0 To 7
        If i = 2 Then s = s & "-"
        s = s & HexPad(g.Data4(i), 2)
    Next i
    CalyxNewIdempotencyKey = LCase$(s)
End Function

' Right$ keeps the low digits, so a negative Integer (sign-extended to a Long) also works.
Private Function HexPad(ByVal Number As Long, ByVal Digits As Long) As String
    HexPad = Right$(String$(Digits, "0") & Hex$(Number), Digits)
End Function

Public Function CalyxUtf8Encode(ByVal Text As String) As Byte()
    Dim utf8() As Byte
    Dim byteTotal As Long
    ' A String assigned to a Byte() gives an array with no elements (UBound -1).
    utf8 = ""
    If Len(Text) > 0 Then
        byteTotal = WideCharToMultiByte(CP_UTF8, 0, StrPtr(Text), Len(Text), 0, 0, 0, 0)
        If byteTotal > 0 Then
            ReDim utf8(0 To byteTotal - 1)
            WideCharToMultiByte CP_UTF8, 0, StrPtr(Text), Len(Text), VarPtr(utf8(0)), byteTotal, 0, 0
        End If
    End If
    CalyxUtf8Encode = utf8
End Function

Public Function CalyxUtf8Decode(Bytes() As Byte) As String
    Dim byteTotal As Long
    Dim charTotal As Long
    Dim wide As String
    byteTotal = ByteCount(Bytes)
    If byteTotal = 0 Then Exit Function
    charTotal = MultiByteToWideChar(CP_UTF8, 0, VarPtr(Bytes(LBound(Bytes))), byteTotal, 0, 0)
    If charTotal <= 0 Then Exit Function
    wide = String$(charTotal, vbNullChar)
    MultiByteToWideChar CP_UTF8, 0, VarPtr(Bytes(LBound(Bytes))), byteTotal, StrPtr(wide), charTotal
    CalyxUtf8Decode = wide
End Function

' An array with no elements, or one that was never sized, makes UBound raise error 9.
Private Function ByteCount(Bytes() As Byte) As Long
    On Error GoTo NoElements
    ByteCount = UBound(Bytes) - LBound(Bytes) + 1
    Exit Function
NoElements:
    ByteCount = 0
End Function

' Percent-encodes the UTF-8 bytes of a path parameter. Only the unreserved characters of
' RFC 3986 stay as they are.
Public Function CalyxEncodePathPart(ByVal Value As String) As String
    Dim utf8() As Byte
    Dim encoded As String
    Dim i As Long
    utf8 = CalyxUtf8Encode(Value)
    For i = LBound(utf8) To UBound(utf8)
        Select Case utf8(i)
        Case 48 To 57, 65 To 90, 97 To 122, 45, 46, 95, 126
            encoded = encoded & Chr$(utf8(i))
        Case Else
            encoded = encoded & "%" & Right$("0" & Hex$(utf8(i)), 2)
        End Select
    Next i
    CalyxEncodePathPart = encoded
End Function

' http:// is only for the tests, so the API key never goes to another host in clear text.
Public Function CalyxCheckBaseUrl(ByVal Url As String) As String
    Dim lowered As String
    Dim authority As String
    Dim host As String
    lowered = LCase$(Url)
    If Left$(lowered, 8) = "https://" Then
        authority = Mid$(lowered, 9)
    ElseIf Left$(lowered, 7) = "http://" Then
        authority = Mid$(lowered, 8)
    Else
        FailCheck URL_RULE
    End If
    ' The SDK appends each path after BaseUrl, so a query or a fragment would swallow the path.
    If InStr(Url, "?") > 0 Or InStr(Url, "#") > 0 Then
        FailCheck "BaseUrl must not have a query (""?"") or a fragment (""#"")."
    End If
    authority = TextBefore(authority, "/")
    ' WinHTTP reads the text before "@" as a user name: http://localhost:1@evil.example goes to
    ' evil.example, with the API key in clear text.
    If InStr(authority, "@") > 0 Then FailCheck "BaseUrl must not have a user name or a password (""@"")."
    host = TextBefore(authority, ":")
    If Len(host) = 0 Then FailCheck "BaseUrl has no host name."
    If Left$(lowered, 7) = "http://" And host <> "localhost" And host <> "127.0.0.1" Then FailCheck URL_RULE
    If Right$(Url, 1) = "/" Then Url = Left$(Url, Len(Url) - 1)
    CalyxCheckBaseUrl = Url
End Function

' The text up to the first Mark, or all the text when Mark is not in it.
Private Function TextBefore(ByVal Text As String, ByVal Mark As String) As String
    Dim cut As Long
    cut = InStr(Text, Mark)
    If cut > 0 Then
        TextBefore = Left$(Text, cut - 1)
    Else
        TextBefore = Text
    End If
End Function

' VBA-JSON writes a text of 16 to 100 characters from "0" to "9", ".", "E" and "e" as a JSON
' number, with no quotes. The option quotes every text. Only the writing of a body uses it.
Public Function CalyxBodyJson(ByVal Fields As Object) As String
    Dim savedOption As Boolean
    Dim errNumber As Long
    Dim errSource As String
    Dim errDescription As String
    savedOption = CalyxJsonOptions.UseDoubleForLargeNumbers
    On Error GoTo ConvertFailed
    CalyxJsonOptions.UseDoubleForLargeNumbers = True
    CalyxBodyJson = CalyxConvertToJson(Fields)
    CalyxJsonOptions.UseDoubleForLargeNumbers = savedOption
    Exit Function
ConvertFailed:
    errNumber = Err.Number
    errSource = Err.Source
    errDescription = Err.Description
    CalyxJsonOptions.UseDoubleForLargeNumbers = savedOption
    Err.Raise errNumber, errSource, errDescription
End Function

Public Sub CalyxCheckIdempotencyKey(ByVal Key As String)
    Dim i As Long
    Dim ch As Long
    If Len(Key) < 1 Or Len(Key) > KEY_MAX_LENGTH Then FailCheck KEY_RULE
    For i = 1 To Len(Key)
        ch = AscW(Mid$(Key, i, 1))
        If ch < 33 Or ch > 126 Then FailCheck KEY_RULE
    Next i
End Sub

' A check only raises. The client turns the error into a failure with CalyxFailureFromErr.
Private Sub FailCheck(ByVal Detail As String)
    CalyxRaise CalyxClientFailure(CALYX_CODE_INVALID_CONFIG, Detail)
End Sub

' Gives the failure of an error that CalyxRaise raised, or Nothing for any other error. The
' caller passes Err.Number, Err.Source and Err.Description, because Err changes with each call.
Public Function CalyxFailureFromErr(ByVal Number As Long, ByVal Source As String, _
    ByVal Description As String) As CalyxError
    Dim cut As Long
    If Number <> vbObjectError + 513 Or Source <> "Calyx" Then Exit Function
    cut = InStr(Description, ": ")
    If cut = 0 Then
        Set CalyxFailureFromErr = CalyxClientFailure(Description, "")
    Else
        Set CalyxFailureFromErr = CalyxClientFailure(Left$(Description, cut - 1), Mid$(Description, cut + 2))
    End If
End Function

' A Null field put in a String raises error 94, so the program reads fields through this.
' Dictionary.Item adds a missing key, so the function asks Exists first.
Public Function CalyxField(ByVal Dict As Object, ByVal Name As String, ByVal Default As Variant) As Variant
    If Not Dict Is Nothing Then
        If Dict.Exists(Name) Then
            If IsObject(Dict.Item(Name)) Then
                Set CalyxField = Dict.Item(Name)
                Exit Function
            ElseIf Not IsNull(Dict.Item(Name)) Then
                CalyxField = Dict.Item(Name)
                Exit Function
            End If
        End If
    End If
    If IsObject(Default) Then
        Set CalyxField = Default
    Else
        CalyxField = Default
    End If
End Function

' A failure that the SDK found itself. Status stays 0.
Public Function CalyxClientFailure(ByVal Code As String, ByVal Detail As String) As CalyxError
    Dim made As CalyxError
    Set made = New CalyxError
    made.Source = "client"
    made.Code = Code
    made.Detail = Detail
    Set CalyxClientFailure = made
End Function

' The body decides, like the other SDKs: a JSON object with a string "code" is a backend error.
' ContentType is not needed for this rule.
Public Function CalyxMapBackendError(ByVal Status As Long, ByVal ContentType As String, _
    ByVal RetryAfterHeader As String, ByVal RequestIdHeader As String, _
    ByVal BodyText As String) As CalyxError
    Dim problem As Object
    Dim mapped As CalyxError
    Set problem = ParseObject(BodyText)
    If problem Is Nothing Then
        Set CalyxMapBackendError = NotProblem(Status)
        Exit Function
    End If
    If VarType(CalyxField(problem, "code", Null)) <> vbString Then
        Set CalyxMapBackendError = NotProblem(Status)
        Exit Function
    End If
    Set mapped = New CalyxError
    mapped.Source = "backend"
    mapped.Code = problem.Item("code")
    mapped.Status = Status
    mapped.Detail = TextField(problem, "detail")
    If Len(mapped.Detail) = 0 Then mapped.Detail = TextField(problem, "title")
    mapped.RequestId = RequestIdHeader
    mapped.RetryAfter = RetryAfterSeconds(RetryAfterHeader)
    AddFieldErrors mapped, CalyxField(problem, "errors", Nothing)
    Set CalyxMapBackendError = mapped
End Function

Private Function NotProblem(ByVal Status As Long) As CalyxError
    Set NotProblem = CalyxClientFailure(CALYX_CODE_INVALID_RESPONSE, _
        "The backend answered " & Status & " with a body that is not problem+json.")
End Function

' Only whole seconds count: an optional "+", then digits. Leading zeros are fine.
' A date, a minus sign, a value above the Long range, or any other form gives 0.
Private Function RetryAfterSeconds(ByVal Header As String) As Long
    Dim numberText As String
    Dim i As Long
    numberText = Trim$(Header)
    If Left$(numberText, 1) = "+" Then numberText = Mid$(numberText, 2)
    If Len(numberText) = 0 Then Exit Function
    For i = 1 To Len(numberText)
        If Mid$(numberText, i, 1) < "0" Or Mid$(numberText, i, 1) > "9" Then Exit Function
    Next i
    ' Without the leading zeros, ten digits can still be too big for a Long.
    Do While Len(numberText) > 1 And Left$(numberText, 1) = "0"
        numberText = Mid$(numberText, 2)
    Loop
    If Len(numberText) > 10 Then Exit Function
    If Len(numberText) = 10 And numberText > "2147483647" Then Exit Function
    RetryAfterSeconds = CLng(numberText)
End Function

Private Sub AddFieldErrors(ByVal Target As CalyxError, ByVal List As Variant)
    Dim entry As Variant
    If TypeName(List) <> "Collection" Then Exit Sub
    For Each entry In List
        If TypeName(entry) = "Dictionary" Then
            Target.AddFieldError TextField(entry, "location"), TextField(entry, "message")
        End If
    Next entry
End Sub

' A field that is absent or not a string gives "".
Private Function TextField(ByVal Dict As Object, ByVal Name As String) As String
    If Dict.Exists(Name) Then
        If VarType(Dict.Item(Name)) = vbString Then TextField = Dict.Item(Name)
    End If
End Function

' Gives Nothing when the text is not a JSON object.
Private Function ParseObject(ByVal Json As String) As Object
    Dim parsed As Object
    Set parsed = ParseAny(Json)
    If Not parsed Is Nothing Then
        If TypeName(parsed) = "Dictionary" Then Set ParseObject = parsed
    End If
End Function

' Gives Nothing when the text is not a JSON object or array. CalyxParseJson raises on bad JSON.
Private Function ParseAny(ByVal Json As String) As Object
    If Len(Json) = 0 Then Exit Function
    On Error GoTo BadJson
    Set ParseAny = CalyxParseJson(Json)
    Exit Function
BadJson:
    Set ParseAny = Nothing
End Function

' WinHTTP gives the number as an HRESULT (&H80072EE2) or as the bare number (12002). Both give
' the same result.
Public Function CalyxMapWinHttpError(ByVal Number As Long, ByVal Description As String) As CalyxError
    Dim winHttpNumber As Long
    Dim mappedCode As String
    Dim note As String
    Dim mapped As CalyxError
    winHttpNumber = Number
    ' &HFFFF& is the Long 65535. &HFFFF is the Integer -1, which keeps every bit.
    If (Number And HRESULT_FACILITY_MASK) = HRESULT_WIN32 Then winHttpNumber = Number And &HFFFF&
    Select Case winHttpNumber
    Case WINHTTP_TIMEOUT
        mappedCode = CALYX_CODE_TIMEOUT
        note = "The backend did not answer in time."
    Case WINHTTP_TLS_FIRST To WINHTTP_TLS_LAST, WINHTTP_SECURE_CHANNEL_ERROR, _
        WINHTTP_SECURE_FAILURE, WINHTTP_CERT_REV_FAILED, WINHTTP_SECURE_INVALID_CERT, WINHTTP_SECURE_CERT_REVOKED
        mappedCode = CALYX_CODE_NETWORK_ERROR
        note = "The TLS connection failed. See the README section ""TLS on Windows 7""."
    Case Else
        mappedCode = CALYX_CODE_NETWORK_ERROR
        note = "The connection to the backend failed."
    End Select
    note = "WinHTTP error " & winHttpNumber & " (" & WinHttpErrorName(winHttpNumber) & "): " & note
    If Len(Trim$(Description)) > 0 Then
        note = note & " WinHTTP text: " & Trim$(Replace(Description, vbCrLf, " "))
    End If
    Set mapped = CalyxClientFailure(mappedCode, note)
    mapped.WinHttpError = winHttpNumber
    Set CalyxMapWinHttpError = mapped
End Function

Private Function WinHttpErrorName(ByVal Number As Long) As String
    Select Case Number
    Case 12001: WinHttpErrorName = "ERROR_WINHTTP_OUT_OF_HANDLES"
    Case 12002: WinHttpErrorName = "ERROR_WINHTTP_TIMEOUT"
    Case 12004: WinHttpErrorName = "ERROR_WINHTTP_INTERNAL_ERROR"
    Case 12005: WinHttpErrorName = "ERROR_WINHTTP_INVALID_URL"
    Case 12006: WinHttpErrorName = "ERROR_WINHTTP_UNRECOGNIZED_SCHEME"
    Case 12007: WinHttpErrorName = "ERROR_WINHTTP_NAME_NOT_RESOLVED"
    Case 12009: WinHttpErrorName = "ERROR_WINHTTP_INVALID_OPTION"
    Case 12011: WinHttpErrorName = "ERROR_WINHTTP_OPTION_NOT_SETTABLE"
    Case 12012: WinHttpErrorName = "ERROR_WINHTTP_SHUTDOWN"
    Case 12017: WinHttpErrorName = "ERROR_WINHTTP_OPERATION_CANCELLED"
    Case 12029: WinHttpErrorName = "ERROR_WINHTTP_CANNOT_CONNECT"
    Case 12030: WinHttpErrorName = "ERROR_WINHTTP_CONNECTION_ERROR"
    Case 12037: WinHttpErrorName = "ERROR_WINHTTP_SECURE_CERT_DATE_INVALID"
    Case 12038: WinHttpErrorName = "ERROR_WINHTTP_SECURE_CERT_CN_INVALID"
    Case 12044: WinHttpErrorName = "ERROR_WINHTTP_CLIENT_AUTH_CERT_NEEDED"
    Case 12045: WinHttpErrorName = "ERROR_WINHTTP_SECURE_INVALID_CA"
    Case 12057: WinHttpErrorName = "ERROR_WINHTTP_SECURE_CERT_REV_FAILED"
    Case 12152: WinHttpErrorName = "ERROR_WINHTTP_INVALID_SERVER_RESPONSE"
    Case 12157: WinHttpErrorName = "ERROR_WINHTTP_SECURE_CHANNEL_ERROR"
    Case 12169: WinHttpErrorName = "ERROR_WINHTTP_SECURE_INVALID_CERT"
    Case 12170: WinHttpErrorName = "ERROR_WINHTTP_SECURE_CERT_REVOKED"
    Case 12175: WinHttpErrorName = "ERROR_WINHTTP_SECURE_FAILURE"
    Case Else: WinHttpErrorName = "no known name"
    End Select
End Function

' Reads one answer of the backend. It returns True with Result set, or False with Failure set.
' Result is a CalyxSendResult for sendMessage, the parsed JSON for the other operations, and
' Nothing for a 204 or an empty body.
Public Function CalyxHandleAnswer(ByVal Request As CalyxRequest, ByVal Status As Long, _
    ByVal ContentType As String, ByVal RetryAfter As String, ByVal RequestId As String, _
    Body() As Byte, Result As Object, Failure As CalyxError) As Boolean
    Dim answerText As String
    Dim parsed As Object
    Set Result = Nothing
    Set Failure = Nothing
    On Error GoTo Internal
    answerText = CalyxUtf8Decode(Body)
    If Status >= 200 And Status <= 299 Then
        If Request.Operation = CALYX_OP_SEND_MESSAGE Then
            Set parsed = ParseObject(answerText)
            If parsed Is Nothing Then
                Set Failure = NotJson(Status)
            Else
                Set Result = NewSendResult(parsed, Request.IdempotencyKey, RequestId)
            End If
        ElseIf Status <> 204 And Len(answerText) > 0 Then
            Set parsed = ParseAny(answerText)
            If parsed Is Nothing Then
                Set Failure = NotJson(Status)
            Else
                Set Result = parsed
            End If
        End If
    ElseIf Status >= 300 And Status <= 399 Then
        ' A redirect means a wrong BaseUrl. Following it could send the API key to another host.
        Set Failure = CalyxClientFailure(CALYX_CODE_INVALID_CONFIG, "The backend answered " & _
            Status & ", a redirect. The SDK does not follow redirects. Check BaseUrl.")
    Else
        Set Failure = CalyxMapBackendError(Status, ContentType, RetryAfter, RequestId, answerText)
    End If
    If Failure Is Nothing Then
        CalyxHandleAnswer = True
    Else
        KeepRequest Failure, Request
    End If
    Exit Function
Internal:
    Set Result = Nothing
    Set Failure = CalyxClientFailure(CALYX_CODE_INVALID_RESPONSE, _
        "The SDK could not read the answer: VB6 error " & Err.Number & ", " & Err.Description)
    KeepRequest Failure, Request
End Function

Private Function NotJson(ByVal Status As Long) As CalyxError
    Set NotJson = CalyxClientFailure(CALYX_CODE_INVALID_RESPONSE, _
        "The backend answered " & Status & " with a body that is not JSON.")
End Function

Private Function NewSendResult(ByVal Answer As Object, ByVal IdempotencyKey As String, _
    ByVal RequestId As String) As CalyxSendResult
    Dim made As CalyxSendResult
    Set made = New CalyxSendResult
    made.MessageId = TextField(Answer, "message_id")
    made.Replayed = (CalyxField(Answer, "replayed", False) = True)
    made.IdempotencyKey = IdempotencyKey
    made.RequestId = RequestId
    Set NewSendResult = made
End Function

Private Sub KeepRequest(ByVal Failure As CalyxError, ByVal Request As CalyxRequest)
    If Not Request Is Nothing Then Failure.AttachRequest Request
End Sub

' Opens the request and sets the time limits, the options and the headers. The asynchronous
' path (CalyxCall) and the blocking path (SendNow) both use it, so they send the same request.
' It can raise a WinHTTP error. CalyxTransportFailure turns that error into a failure.
Public Sub CalyxPrepareRequest(ByVal Transport As WinHttp.WinHttpRequest, _
    ByVal Request As CalyxRequest, ByVal BaseUrl As String, ByVal ApiKey As String, _
    ByVal Async As Boolean)
    Dim limitMs As Long
    limitMs = Request.TimeoutSeconds * 1000
    ' The WinHTTP documentation says that time-outs must be set before Open.
    Transport.SetTimeouts RESOLVE_TIMEOUT_MS, CONNECT_TIMEOUT_MS, limitMs, limitMs
    Transport.Open Request.Method, BaseUrl & Request.Path, Async
    Transport.Option(OPTION_SECURE_PROTOCOLS) = PROTOCOL_TLS12
    ' A redirect means a wrong BaseUrl. Following it could send the API key to another host.
    Transport.Option(OPTION_ENABLE_REDIRECTS) = False
    Transport.SetRequestHeader "Authorization", "Bearer " & ApiKey
    Transport.SetRequestHeader "Accept", ACCEPT_TYPES
    Transport.SetRequestHeader "User-Agent", "calyx-vb6-sdk/" & CALYX_SDK_VERSION
    If Len(Request.IdempotencyKey) > 0 Then
        Transport.SetRequestHeader "Idempotency-Key", Request.IdempotencyKey
    End If
    If Len(Request.BodyJson) > 0 Then
        Transport.SetRequestHeader "Content-Type", JSON_CONTENT_TYPE
    End If
End Sub

' Sends the body as UTF-8 bytes, or no body. With Async False, Send blocks until the answer.
Public Sub CalyxSendRequest(ByVal Transport As WinHttp.WinHttpRequest, ByVal Request As CalyxRequest)
    Dim sendBody() As Byte
    If Len(Request.BodyJson) > 0 Then
        sendBody = CalyxUtf8Encode(Request.BodyJson)
        Transport.Send sendBody
    Else
        Transport.Send
    End If
End Sub

' Reads the status, the headers and the body of a finished request, and gives them to
' CalyxHandleAnswer. It can raise an error when WinHTTP has no answer.
Public Function CalyxReadAnswer(ByVal Transport As WinHttp.WinHttpRequest, _
    ByVal Request As CalyxRequest, Result As Object, Failure As CalyxError) As Boolean
    Dim answerBody() As Byte
    answerBody = ResponseBytes(Transport)
    CalyxReadAnswer = CalyxHandleAnswer(Request, Transport.Status, _
        ResponseHeader(Transport, "Content-Type"), ResponseHeader(Transport, "Retry-After"), _
        ResponseHeader(Transport, "X-Request-Id"), answerBody, Result, Failure)
End Function

' ResponseBody is Empty when the answer has no body.
Private Function ResponseBytes(ByVal Transport As WinHttp.WinHttpRequest) As Byte()
    Dim raw As Variant
    Dim answerBytes() As Byte
    raw = Transport.ResponseBody
    If VarType(raw) = (vbArray Or vbByte) Then answerBytes = raw
    ResponseBytes = answerBytes
End Function

' GetResponseHeader raises an error when the header is absent, so an absent header gives "".
Private Function ResponseHeader(ByVal Transport As WinHttp.WinHttpRequest, ByVal HeaderName As String) As String
    On Error Resume Next
    ResponseHeader = Transport.GetResponseHeader(HeaderName)
End Function

' The failure of an error that WinHTTP or VB6 raised while a request was prepared or sent.
' Number and Description are the values of Err.
Public Function CalyxTransportFailure(ByVal Number As Long, ByVal Description As String) As CalyxError
    If IsWinHttpError(Number) Then
        Set CalyxTransportFailure = CalyxMapWinHttpError(Number, Description)
    Else
        ' Not a network error, so it must not look like one. The key is never in the text.
        Set CalyxTransportFailure = CalyxClientFailure(CALYX_CODE_INVALID_RESPONSE, _
            "VB6 error " & Number & ": " & Description)
    End If
End Function

' Every HRESULT of the Win32 facility (&H8007xxxx) counts, whatever its low 16 bits, so
' &H80070005 goes to CalyxMapWinHttpError. A number of another facility counts only when it is
' a plain number from 12000 to 12200.
Private Function IsWinHttpError(ByVal Number As Long) As Boolean
    If (Number And HRESULT_FACILITY_MASK) = HRESULT_WIN32 Then
        IsWinHttpError = True
        Exit Function
    End If
    IsWinHttpError = (Number >= WINHTTP_ERROR_FIRST And Number <= WINHTTP_ERROR_LAST)
End Function

' Only raises. The client sets LastError before it calls this.
Public Sub CalyxRaise(ByVal Failure As CalyxError)
    Err.Raise vbObjectError + 513, "Calyx", Failure.Code & ": " & Failure.Detail
End Sub
