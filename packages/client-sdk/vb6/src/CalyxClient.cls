VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxClient"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' The only class that the program creates. A form declares it WithEvents.
'
' Each asynchronous method checks its settings and arguments, builds a CalyxRequest and starts
' a CalyxCall. The answer comes back through CallFinished as the event Completed or Failed.
' The blocking methods (the names that end in Sync) build the same request and send it with
' SendNow, which waits for the answer.
' A request builder exists only where a Sync method shares the request. The other methods build
' the request inline.

Public Event Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
Public Event Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)

' sendMessage waits longer than the 5 minutes of the backend (CONTRACT.md part 8, rule 1).
Private Const DEFAULT_SEND_TIMEOUT_SECONDS As Long = 360
Private Const DEFAULT_CALL_TIMEOUT_SECONDS As Long = 150
' CalyxPrepareRequest gives WinHTTP the limit in milliseconds, which must fit in a Long.
Private Const MAX_TIMEOUT_SECONDS As Long = 2147483
' The age limit of a saved answer of GetBulkJob or ListBulkJobs.
Private Const SAVED_ANSWER_MS As Double = 25000

Private mBaseUrl As String
Private mApiKey As String
Private mSendTimeoutSeconds As Long
Private mCallTimeoutSeconds As Long
Private mLastError As CalyxError
' The pending calls, with the key CStr(ObjPtr(call)). The Collection keeps each call alive
' until its answer.
Private mCalls As Collection
' The last call that finished. A finished call ends inside its own WinHTTP event when nothing
' else keeps it, so the client keeps it until a different call finishes. It is no cycle,
' because the call has already cleared its reference to the client.
Private mLastFinished As CalyxCall
' The saved answers of GetBulkJob and of ListBulkJobs. The key is the path of the request. Each
' item is Array(tick count, answer).
Private mSavedJobs As Scripting.Dictionary
Private mSavedLists As Scripting.Dictionary

Private Sub Class_Initialize()
    Set mCalls = New Collection
    Set mSavedJobs = New Scripting.Dictionary
    Set mSavedLists = New Scripting.Dictionary
    mSendTimeoutSeconds = DEFAULT_SEND_TIMEOUT_SECONDS
    mCallTimeoutSeconds = DEFAULT_CALL_TIMEOUT_SECONDS
End Sub

' A pending call keeps the client alive, so this runs only when no call is pending, or when VB6
' ends. It raises no event, because the program is gone.
Private Sub Class_Terminate()
    Dim entry As Variant
    Dim leftCall As CalyxCall
    On Error Resume Next
    For Each entry In mCalls
        Set leftCall = entry
        leftCall.Abort
    Next entry
    Set mCalls = Nothing
    Set mLastFinished = Nothing
End Sub

' https:// only. http:// is allowed for localhost and 127.0.0.1. The methods check it.
Public Property Get BaseUrl() As String
    BaseUrl = mBaseUrl
End Property

Public Property Let BaseUrl(ByVal Value As String)
    mBaseUrl = Value
End Property

Public Property Get ApiKey() As String
    ApiKey = mApiKey
End Property

Public Property Let ApiKey(ByVal Value As String)
    mApiKey = Value
End Property

Public Property Get SendTimeoutSeconds() As Long
    SendTimeoutSeconds = mSendTimeoutSeconds
End Property

Public Property Let SendTimeoutSeconds(ByVal Value As Long)
    mSendTimeoutSeconds = Value
End Property

Public Property Get CallTimeoutSeconds() As Long
    CallTimeoutSeconds = mCallTimeoutSeconds
End Property

Public Property Let CallTimeoutSeconds(ByVal Value As Long)
    mCallTimeoutSeconds = Value
End Property

' The failure of the last VB6 error that the SDK raised. Each public method clears it first.
Public Property Get LastError() As CalyxError
    Set LastError = mLastError
End Property

' Returns the idempotency key before any event, so the program can store it.
Public Function SendMessage(ByVal InstanceId As String, ByVal Recipient As String, _
    ByVal Text As String, Optional ByVal IdempotencyKey As Variant, _
    Optional ByVal Tag As Variant) As String
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    Set callRequest = SendMessageRequest(InstanceId, Recipient, Text, checkedUrl, IdempotencyKey, Tag)
    StartCall callRequest, checkedUrl
    SendMessage = callRequest.IdempotencyKey
End Function

Public Sub GetInstance(ByVal InstanceId As String, Optional ByVal Tag As Variant)
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    Set callRequest = GetInstanceRequest(InstanceId, checkedUrl, Tag)
    StartCall callRequest, checkedUrl
End Sub

Public Sub ListInstances(Optional ByVal Tag As Variant)
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_LIST_INSTANCES, "", mCallTimeoutSeconds, Tag)
    callRequest.Path = CalyxOperationPath(CALYX_OP_LIST_INSTANCES)
    checkedUrl = CheckedSettings(callRequest)
    On Error GoTo 0
    StartCall callRequest, checkedUrl
    Exit Sub
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Sub

' One text to many recipients. Returns the idempotency key before any event. The core applies
' its default pacing.
Public Function CreateBulkJobSameText(ByVal InstanceId As String, ByVal Recipients As Collection, _
    ByVal Text As String, Optional ByVal IdempotencyKey As Variant, _
    Optional ByVal Tag As Variant) As String
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Dim jobFields As Scripting.Dictionary
    Dim listPath As String
    Set mLastError = Nothing
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_CREATE_BULK_JOB, KeyOrNew(IdempotencyKey), _
        mCallTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_CREATE_BULK_JOB, InstanceId)
    CheckRecipients Recipients
    Set jobFields = New Scripting.Dictionary
    jobFields.Add "recipients", Recipients
    jobFields.Add "text", Text
    callRequest.BodyJson = CalyxBodyJson(jobFields)
    checkedUrl = CheckedSettings(callRequest)
    listPath = PathWithId(CALYX_OP_LIST_BULK_JOBS, InstanceId)
    On Error GoTo 0
    ForgetSaved mSavedLists, listPath
    StartCall callRequest, checkedUrl
    CreateBulkJobSameText = callRequest.IdempotencyKey
    Exit Function
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Function

' Each item of Messages is a CalyxBulkMessage. Returns the idempotency key before any event.
Public Function CreateBulkJobCustom(ByVal InstanceId As String, ByVal Messages As Collection, _
    Optional ByVal IdempotencyKey As Variant, Optional ByVal Tag As Variant) As String
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Dim jobFields As Scripting.Dictionary
    Dim listPath As String
    Set mLastError = Nothing
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_CREATE_BULK_JOB, KeyOrNew(IdempotencyKey), _
        mCallTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_CREATE_BULK_JOB, InstanceId)
    Set jobFields = New Scripting.Dictionary
    jobFields.Add "messages", MessageFieldList(Messages)
    callRequest.BodyJson = CalyxBodyJson(jobFields)
    checkedUrl = CheckedSettings(callRequest)
    listPath = PathWithId(CALYX_OP_LIST_BULK_JOBS, InstanceId)
    On Error GoTo 0
    ForgetSaved mSavedLists, listPath
    StartCall callRequest, checkedUrl
    CreateBulkJobCustom = callRequest.IdempotencyKey
    Exit Function
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Function

Public Sub GetBulkJob(ByVal BulkJobId As String, Optional ByVal Tag As Variant)
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Dim keepAlive As CalyxClient
    ' A Completed handler of the program can release the last reference to this client.
    Set keepAlive = Me
    Set mLastError = Nothing
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_GET_BULK_JOB, "", mCallTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_GET_BULK_JOB, BulkJobId)
    checkedUrl = CheckedSettings(callRequest)
    On Error GoTo 0
    If RaiseSaved(mSavedJobs, callRequest) Then Exit Sub
    StartCall callRequest, checkedUrl
    Exit Sub
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Sub

Public Sub ListBulkJobs(ByVal InstanceId As String, Optional ByVal Tag As Variant)
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Dim keepAlive As CalyxClient
    ' A Completed handler of the program can release the last reference to this client.
    Set keepAlive = Me
    Set mLastError = Nothing
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_LIST_BULK_JOBS, "", mCallTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_LIST_BULK_JOBS, InstanceId)
    checkedUrl = CheckedSettings(callRequest)
    On Error GoTo 0
    If RaiseSaved(mSavedLists, callRequest) Then Exit Sub
    StartCall callRequest, checkedUrl
    Exit Sub
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Sub

' A POST with no body and no idempotency key. The answer is the job.
' The job answer has the instance, but the request does not, so every saved list is removed.
Public Sub CancelBulkJob(ByVal BulkJobId As String, Optional ByVal Tag As Variant)
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Dim jobPath As String
    Set mLastError = Nothing
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_CANCEL_BULK_JOB, "", mCallTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_CANCEL_BULK_JOB, BulkJobId)
    checkedUrl = CheckedSettings(callRequest)
    jobPath = PathWithId(CALYX_OP_GET_BULK_JOB, BulkJobId)
    On Error GoTo 0
    ForgetSaved mSavedJobs, jobPath
    mSavedLists.RemoveAll
    StartCall callRequest, checkedUrl
    Exit Sub
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Sub

' Sends the request of the failure again without a change: the same key, body, tag and time
' limit. Returns the idempotency key. The SDK never calls it by itself.
Public Function Resend(ByVal Failure As CalyxError) As String
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    Set callRequest = ResendRequest(Failure, checkedUrl)
    StartCall callRequest, checkedUrl
    Resend = callRequest.IdempotencyKey
End Function

' Blocking methods. Each one waits for the answer and returns the result, so the window of a
' form freezes for up to the time limit. A failure raises a VB6 error, and LastError holds the
' CalyxError. They raise no event.

' Returns the CalyxSendResult. The key is in its IdempotencyKey, and in LastError on a failure.
Public Function SendMessageSync(ByVal InstanceId As String, ByVal Recipient As String, _
    ByVal Text As String, Optional ByVal IdempotencyKey As Variant, _
    Optional ByVal Tag As Variant) As CalyxSendResult
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    Set callRequest = SendMessageRequest(InstanceId, Recipient, Text, checkedUrl, IdempotencyKey, Tag)
    Set SendMessageSync = SendNow(callRequest, checkedUrl)
End Function

' Returns the instance as a Scripting.Dictionary.
Public Function GetInstanceSync(ByVal InstanceId As String, Optional ByVal Tag As Variant) As Object
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    Set callRequest = GetInstanceRequest(InstanceId, checkedUrl, Tag)
    Set GetInstanceSync = SendNow(callRequest, checkedUrl)
End Function

' The rules of Resend. Returns the result of the operation of the failure: a CalyxSendResult
' for sendMessage, the parsed answer for the other operations, or Nothing for an empty answer.
Public Function ResendSync(ByVal Failure As CalyxError) As Object
    Dim callRequest As CalyxRequest
    Dim checkedUrl As String
    Set mLastError = Nothing
    Set callRequest = ResendRequest(Failure, checkedUrl)
    Set ResendSync = SendNow(callRequest, checkedUrl)
End Function

' The request of sendMessage, after the checks. Url gets BaseUrl with no final "/". A failed
' check sets LastError, with the request when it is built, and raises.
Private Function SendMessageRequest(ByVal InstanceId As String, ByVal Recipient As String, _
    ByVal Text As String, Url As String, Optional ByVal IdempotencyKey As Variant, _
    Optional ByVal Tag As Variant) As CalyxRequest
    Dim callRequest As CalyxRequest
    Dim sendFields As Scripting.Dictionary
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_SEND_MESSAGE, KeyOrNew(IdempotencyKey), _
        mSendTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_SEND_MESSAGE, InstanceId)
    Set sendFields = New Scripting.Dictionary
    sendFields.Add "to", Recipient
    sendFields.Add "text", Text
    callRequest.BodyJson = CalyxBodyJson(sendFields)
    Url = CheckedSettings(callRequest)
    Set SendMessageRequest = callRequest
    Exit Function
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Function

Private Function GetInstanceRequest(ByVal InstanceId As String, Url As String, _
    Optional ByVal Tag As Variant) As CalyxRequest
    Dim callRequest As CalyxRequest
    On Error GoTo CheckFailed
    Set callRequest = NewRequest(CALYX_OP_GET_INSTANCE, "", mCallTimeoutSeconds, Tag)
    callRequest.Path = PathWithId(CALYX_OP_GET_INSTANCE, InstanceId)
    Url = CheckedSettings(callRequest)
    Set GetInstanceRequest = callRequest
    Exit Function
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Function

' The request of the failure, unchanged, after the checks of Resend. It also removes the saved
' answers that a create or a cancel makes old.
Private Function ResendRequest(ByVal Failure As CalyxError, Url As String) As CalyxRequest
    Dim callRequest As CalyxRequest
    On Error GoTo CheckFailed
    If Failure Is Nothing Then FailCheck "Failure is Nothing."
    Set callRequest = Failure.Request
    If callRequest Is Nothing Then FailCheck "The failure has no request to send again."
    Select Case Failure.Retry
    Case calyxRetryCheckIfSent
        FailCheck "Resend refuses " & Failure.Code & ": the key can never send again. Check the chat."
    Case calyxRetryDoNotRetry
        FailCheck "Resend refuses " & Failure.Code & ": the same request fails again."
    End Select
    Url = CheckedSettings(callRequest)
    On Error GoTo 0
    ' The saved answers hold a path with an ID, and the create path equals the list path.
    ' Removing all saved answers is a simple, safe superset.
    If callRequest.Operation = CALYX_OP_CREATE_BULK_JOB Or _
        callRequest.Operation = CALYX_OP_CANCEL_BULK_JOB Then
        mSavedJobs.RemoveAll
        mSavedLists.RemoveAll
    End If
    Set ResendRequest = callRequest
    Exit Function
CheckFailed:
    RaiseCheckFailure Err.Number, Err.Source, Err.Description, callRequest
End Function

' Raises Failed with aborted for each pending call before it returns. The backend can still
' finish a call that it got, so a send is resent with the same key.
Public Sub AbortAll()
    Dim stopped As Collection
    Dim entry As Variant
    Dim abortedCall As CalyxCall
    Dim abortedFailure As CalyxError
    Dim keepAlive As CalyxClient
    ' A Failed handler of the program can release the last reference to this client.
    Set keepAlive = Me
    Set mLastError = Nothing
    ' A new Collection first, so a call that an event handler starts is not aborted.
    Set stopped = mCalls
    Set mCalls = New Collection
    ' Abort every call before the first event, because an event handler can call AbortAll again.
    For Each entry In stopped
        Set abortedCall = entry
        abortedCall.Abort
    Next entry
    ' An error from an event handler of the program must not stop the other events.
    On Error Resume Next
    For Each entry In stopped
        Set abortedCall = entry
        Set abortedFailure = CalyxClientFailure(CALYX_CODE_ABORTED, _
            "AbortAll stopped the wait for the answer. The backend can still finish the call.")
        abortedFailure.AttachRequest abortedCall.Request
        RaiseEvent Failed(abortedFailure.Tag, abortedFailure.Operation, abortedFailure)
    Next entry
End Sub

' The only callback of CalyxCall. The call has already cleared its reference to the client.
Friend Sub CallFinished(ByVal Finished As CalyxCall)
    Dim finishedRequest As CalyxRequest
    ' CalyxCall calls this from its WinHTTP event, where an error would end a compiled EXE.
    On Error Resume Next
    ' Removed first, so an event handler that calls AbortAll does not see this call.
    mCalls.Remove CStr(ObjPtr(Finished))
    Set mLastFinished = Finished
    Set finishedRequest = Finished.Request
    If Finished.Failure Is Nothing Then
        SaveAnswer finishedRequest, Finished.Result
        RaiseEvent Completed(finishedRequest.Tag, finishedRequest.Operation, Finished.Result)
    Else
        RaiseEvent Failed(finishedRequest.Tag, finishedRequest.Operation, Finished.Failure)
    End If
End Sub

' Raises Completed with the saved answer of the same path when it is younger than 25 seconds,
' and returns True. The program gets the same object as the first caller.
Private Function RaiseSaved(ByVal Saved As Scripting.Dictionary, ByVal Pending As CalyxRequest) As Boolean
    Dim savedItem As Variant
    Dim savedAnswer As Object
    Dim elapsed As Double
    If Not Saved.Exists(Pending.Path) Then Exit Function
    savedItem = Saved.Item(Pending.Path)
    ' A Double, so the difference cannot overflow when the counter wraps. Negative means wrapped.
    elapsed = CDbl(CalyxTickCount()) - CDbl(savedItem(0))
    If elapsed < 0 Or elapsed >= SAVED_ANSWER_MS Then
        Saved.Remove Pending.Path
        Exit Function
    End If
    Set savedAnswer = savedItem(1)
    RaiseSaved = True
    ' The same rule as CallFinished: an error from an event handler of the program is ignored.
    On Error Resume Next
    RaiseEvent Completed(Pending.Tag, Pending.Operation, savedAnswer)
End Function

' Saves a successful answer of GetBulkJob or ListBulkJobs with the time it came.
Private Sub SaveAnswer(ByVal Done As CalyxRequest, ByVal Answer As Object)
    If Answer Is Nothing Then Exit Sub
    Select Case Done.Operation
    Case CALYX_OP_GET_BULK_JOB
        mSavedJobs.Item(Done.Path) = Array(CalyxTickCount(), Answer)
    Case CALYX_OP_LIST_BULK_JOBS
        mSavedLists.Item(Done.Path) = Array(CalyxTickCount(), Answer)
    End Select
End Sub

Private Sub ForgetSaved(ByVal Saved As Scripting.Dictionary, ByVal Path As String)
    If Saved.Exists(Path) Then Saved.Remove Path
End Sub

' The operation, method, key, time limit and tag of a new request. A missing Tag gives Empty.
Private Function NewRequest(ByVal Operation As String, ByVal IdempotencyKey As String, _
    ByVal TimeoutSeconds As Long, Optional ByVal Tag As Variant) As CalyxRequest
    Dim made As CalyxRequest
    Set made = New CalyxRequest
    made.Operation = Operation
    made.Method = CalyxOperationMethod(Operation)
    made.IdempotencyKey = IdempotencyKey
    made.TimeoutSeconds = TimeoutSeconds
    If IsMissing(Tag) Then
        made.Tag = Empty
    ElseIf IsObject(Tag) Then
        Set made.Tag = Tag
    Else
        made.Tag = Tag
    End If
    Set NewRequest = made
End Function

' The key that the program gives, after the check, or a new UUID.
Private Function KeyOrNew(Optional ByVal IdempotencyKey As Variant) As String
    If IsMissing(IdempotencyKey) Then
        KeyOrNew = CalyxNewIdempotencyKey()
        Exit Function
    End If
    If VarType(IdempotencyKey) <> vbString Then FailCheck "IdempotencyKey must be a String."
    CalyxCheckIdempotencyKey IdempotencyKey
    KeyOrNew = IdempotencyKey
End Function

' The path of the operation with the percent-encoded ID.
Private Function PathWithId(ByVal Operation As String, ByVal Id As String) As String
    If Len(Id) = 0 Then FailCheck "The ID of " & Operation & " is empty."
    PathWithId = Replace(CalyxOperationPath(Operation), "{id}", CalyxEncodePathPart(Id))
End Function

' VBA-JSON writes a number with no quotes, and a long phone number can lose digits as a Double.
' So every recipient must be a String. The backend checks the values.
Private Sub CheckRecipients(ByVal Recipients As Collection)
    Dim entry As Variant
    Dim itemNumber As Long
    If Recipients Is Nothing Then FailCheck "Recipients is Nothing."
    For Each entry In Recipients
        itemNumber = itemNumber + 1
        If VarType(entry) <> vbString Then FailCheck "Recipients item " & itemNumber & " is not a String."
    Next entry
End Sub

' The field messages: one Dictionary with to, text and (when set) delay_ms for each item.
Private Function MessageFieldList(ByVal Messages As Collection) As Collection
    Dim fieldList As Collection
    Dim entry As Variant
    Dim bulkMessage As CalyxBulkMessage
    Dim messageFields As Scripting.Dictionary
    Dim itemNumber As Long
    If Messages Is Nothing Then FailCheck "Messages is Nothing."
    Set fieldList = New Collection
    For Each entry In Messages
        itemNumber = itemNumber + 1
        If TypeName(entry) <> "CalyxBulkMessage" Then
            FailCheck "Messages item " & itemNumber & " is not a CalyxBulkMessage."
        End If
        Set bulkMessage = entry
        Set messageFields = New Scripting.Dictionary
        messageFields.Add "to", bulkMessage.Recipient
        messageFields.Add "text", bulkMessage.Text
        ' Null also means "not set", so the field stays out.
        If Not IsEmpty(bulkMessage.DelayMs) And Not IsNull(bulkMessage.DelayMs) Then
            messageFields.Add "delay_ms", DelayValue(bulkMessage.DelayMs, itemNumber)
        End If
        fieldList.Add messageFields
    Next entry
    Set MessageFieldList = fieldList
End Function

' CLng turns "500" into a number, so VBA-JSON writes it with no quotes.
Private Function DelayValue(ByVal DelayMs As Variant, ByVal Number As Long) As Long
    Dim delayNumber As Double
    Dim rule As String
    rule = "DelayMs of Messages item " & Number & " must be a whole number from 0 to 2147483647."
    ' True is numeric in VB6 (-1), so a Boolean is refused first.
    If VarType(DelayMs) = vbBoolean Then FailCheck rule
    ' IsNumeric and CDbl follow the regional settings ("1.5" becomes 15 in es-ES), so a String
    ' may hold only the digits 0 to 9, and any other type must be a numeric type.
    If VarType(DelayMs) = vbString Then
        If Not IsDigitsOnly(CStr(DelayMs)) Then FailCheck rule
    ElseIf Not IsNumberType(VarType(DelayMs)) Then
        FailCheck rule
    End If
    delayNumber = CDbl(DelayMs)
    If delayNumber < 0 Or delayNumber > 2147483647# Then FailCheck rule
    If delayNumber <> Int(delayNumber) Then FailCheck rule
    DelayValue = CLng(delayNumber)
End Function

Private Function IsDigitsOnly(ByVal Text As String) As Boolean
    Dim position As Long
    If Len(Text) = 0 Then Exit Function
    For position = 1 To Len(Text)
        If Mid$(Text, position, 1) < "0" Or Mid$(Text, position, 1) > "9" Then Exit Function
    Next position
    IsDigitsOnly = True
End Function

Private Function IsNumberType(ByVal Kind As Long) As Boolean
    Select Case Kind
    Case vbByte, vbInteger, vbLong, vbSingle, vbDouble, vbCurrency, vbDecimal
        IsNumberType = True
    End Select
End Function

' Checks the settings for one request and returns BaseUrl with no final "/".
Private Function CheckedSettings(ByVal Pending As CalyxRequest) As String
    CheckedSettings = CalyxCheckBaseUrl(mBaseUrl)
    ' The key itself never goes into Detail.
    If Len(mApiKey) = 0 Then FailCheck "ApiKey is empty."
    ' A character outside 33..126 makes SetRequestHeader fail, and that would look like a
    ' network error.
    If Not IsHeaderSafe(mApiKey) Then FailCheck "ApiKey has a character that is not allowed."
    If Pending.TimeoutSeconds < 1 Or Pending.TimeoutSeconds > MAX_TIMEOUT_SECONDS Then
        FailCheck "The time limit of " & Pending.Operation & " must be from 1 to " & _
            MAX_TIMEOUT_SECONDS & " seconds."
    End If
End Function

' True when every character of the text is printable ASCII, from 33 to 126.
Private Function IsHeaderSafe(ByVal Text As String) As Boolean
    Dim i As Long
    Dim charCode As Long
    For i = 1 To Len(Text)
        charCode = AscW(Mid$(Text, i, 1))
        If charCode < 33 Or charCode > 126 Then Exit Function
    Next i
    IsHeaderSafe = True
End Function

' On False the call is never in the Collection. LastError is set before the raise.
Private Sub StartCall(ByVal Pending As CalyxRequest, ByVal Url As String)
    Dim newCall As CalyxCall
    Set newCall = New CalyxCall
    If newCall.Start(Me, Pending, Url, mApiKey) Then
        ' WinHTTP can raise an event inside Send. A call that has ended must not stay in the
        ' Collection.
        If newCall.IsPending Then mCalls.Add newCall, CStr(ObjPtr(newCall))
    Else
        Set mLastError = newCall.Failure
        CalyxRaise mLastError
    End If
End Sub

' The blocking path: a local WinHttpRequest with Async False, the same request and the same
' answer handling as CalyxCall. It returns the result, or sets LastError and raises. The
' caller must have no active error handler, so the failure keeps every field (Status,
' RequestId, WinHttpError, the request), which CalyxFailureFromErr would not rebuild.
Private Function SendNow(ByVal Pending As CalyxRequest, ByVal Url As String) As Object
    Dim blockingHttp As WinHttp.WinHttpRequest
    Dim answerResult As Object
    Dim answerFailure As CalyxError
    Dim answered As Boolean
    Dim errNumber As Long
    Dim errDescription As String
    On Error GoTo SendFailed
    Set blockingHttp = New WinHttp.WinHttpRequest
    CalyxPrepareRequest blockingHttp, Pending, Url, mApiKey, False
    CalyxSendRequest blockingHttp, Pending
    answered = CalyxReadAnswer(blockingHttp, Pending, answerResult, answerFailure)
    On Error GoTo 0
    If answered Then
        SaveAnswer Pending, answerResult
        Set SendNow = answerResult
        Exit Function
    End If
    Set mLastError = answerFailure
    CalyxRaise mLastError
    Exit Function
SendFailed:
    errNumber = Err.Number
    errDescription = Err.Description
    ' Resume ends the active error handler. On Error GoTo 0 then lets CalyxRaise leave SendNow.
    Resume RaiseTransportFailure
RaiseTransportFailure:
    On Error GoTo 0
    Set mLastError = CalyxTransportFailure(errNumber, errDescription)
    mLastError.AttachRequest Pending
    CalyxRaise mLastError
End Function

Private Sub FailCheck(ByVal Detail As String)
    CalyxRaise CalyxClientFailure(CALYX_CODE_INVALID_CONFIG, Detail)
End Sub

' Turns a raised check into LastError, with the request when there is one, and raises it again.
' The caller passes the Err values, because Err changes with each call.
Private Sub RaiseCheckFailure(ByVal Number As Long, ByVal Source As String, _
    ByVal Description As String, ByVal Pending As CalyxRequest)
    Dim checkFailure As CalyxError
    Set checkFailure = CalyxFailureFromErr(Number, Source, Description)
    ' An error that the SDK did not raise goes on unchanged.
    If checkFailure Is Nothing Then Err.Raise Number, Source, Description
    If Not Pending Is Nothing Then checkFailure.AttachRequest Pending
    Set mLastError = checkFailure
    CalyxRaise checkFailure
End Sub
