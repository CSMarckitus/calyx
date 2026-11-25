VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxCall"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' One pending asynchronous call. WithEvents cannot be used on an array, so each call has its
' own WinHttpRequest. It is internal: only CalyxClient creates one.
'
' The reference cycle: the client keeps this call in its Collection, and mOwner points back to
' the client. Every way out of a call clears mOwner (the two handlers, Abort, a failed Start),
' and the client removes the call from the Collection, so both objects can end.

Private WithEvents mHttp As WinHttp.WinHttpRequest
Attribute mHttp.VB_VarHelpID = -1
Private mOwner As CalyxClient
Private mRequest As CalyxRequest
Private mResult As Object
Private mFailure As CalyxError

' Sends the request and returns at once. It returns True when the request is sent, or False
' with Failure set. It never raises an error, so the client can set LastError first.
Friend Function Start(ByVal Owner As CalyxClient, ByVal Request As CalyxRequest, _
    ByVal BaseUrl As String, ByVal ApiKey As String) As Boolean
    Dim errNumber As Long
    Dim errDescription As String
    On Error GoTo Failed
    Set mRequest = Request
    Set mResult = Nothing
    Set mFailure = Nothing
    Set mHttp = New WinHttp.WinHttpRequest
    CalyxPrepareRequest mHttp, Request, BaseUrl, ApiKey, True
    ' The owner is set before Send, so that a handler never finds it missing for a sent call.
    Set mOwner = Owner
    CalyxSendRequest mHttp, Request
    Start = True
    Exit Function
Failed:
    errNumber = Err.Number
    errDescription = Err.Description
    ' Resume ends the active error handler, so an error in the clean-up cannot leave Start.
    Resume CleanUp
CleanUp:
    On Error Resume Next
    Set mOwner = Nothing
    If Not mHttp Is Nothing Then mHttp.Abort
    Set mHttp = Nothing
    Set mFailure = CalyxTransportFailure(errNumber, errDescription)
    mFailure.AttachRequest Request
    Start = False
End Function

' Stops the request with no event. The client makes the aborted failure itself.
Friend Sub Abort()
    On Error Resume Next
    Set mOwner = Nothing
    If Not mHttp Is Nothing Then mHttp.Abort
    Set mHttp = Nothing
End Sub

' True while the call waits for its answer. A handler or Abort clears the owner.
Friend Property Get IsPending() As Boolean
    IsPending = Not (mOwner Is Nothing)
End Property

' The parsed answer of a call that ended with no failure. Nothing for a 204 or an empty body.
Friend Property Get Result() As Object
    Set Result = mResult
End Property

Friend Property Get Failure() As CalyxError
    Set Failure = mFailure
End Property

Friend Property Get Request() As CalyxRequest
    Set Request = mRequest
End Property

' An unhandled error in an event handler ends a compiled EXE, so every handler has On Error.
Private Sub mHttp_OnResponseFinished()
    Dim keepAlive As CalyxCall
    Dim answerResult As Object
    Dim answerFailure As CalyxError
    Dim errNumber As Long
    Dim errDescription As String
    On Error GoTo Internal
    ' Nothing here means that the call was aborted. The client has already reported it.
    If mOwner Is Nothing Then Exit Sub
    ' The client removes this call from its Collection in CallFinished. This reference keeps the
    ' object alive until the handler ends.
    Set keepAlive = Me
    CalyxReadAnswer mHttp, mRequest, answerResult, answerFailure
    Set mResult = answerResult
    Set mFailure = answerFailure
    ReportToOwner
    Exit Sub
Internal:
    errNumber = Err.Number
    errDescription = Err.Description
    Resume InternalFailure
InternalFailure:
    On Error Resume Next
    If mOwner Is Nothing Then Exit Sub
    Set mResult = Nothing
    Set mFailure = CalyxClientFailure(CALYX_CODE_INVALID_RESPONSE, _
        "The SDK could not read the answer: VB6 error " & errNumber & ", " & errDescription)
    mFailure.AttachRequest mRequest
    ReportToOwner
End Sub

Private Sub mHttp_OnError(ByVal ErrorNumber As Long, ByVal ErrorDescription As String)
    Dim keepAlive As CalyxCall
    Dim errNumber As Long
    Dim errDescription As String
    On Error GoTo Internal
    If mOwner Is Nothing Then Exit Sub
    Set keepAlive = Me
    Set mResult = Nothing
    Set mFailure = CalyxMapWinHttpError(ErrorNumber, ErrorDescription)
    mFailure.AttachRequest mRequest
    ReportToOwner
    Exit Sub
Internal:
    errNumber = Err.Number
    errDescription = Err.Description
    Resume InternalFailure
InternalFailure:
    On Error Resume Next
    If mOwner Is Nothing Then Exit Sub
    Set mResult = Nothing
    Set mFailure = CalyxClientFailure(CALYX_CODE_INVALID_RESPONSE, _
        "The SDK could not read the WinHTTP error: VB6 error " & errNumber & ", " & errDescription)
    mFailure.AttachRequest mRequest
    ReportToOwner
End Sub

' Clears mOwner before the client hears of the call, so a later event does nothing and the
' reference cycle ends.
Private Sub ReportToOwner()
    Dim finishedOwner As CalyxClient
    Set finishedOwner = mOwner
    Set mOwner = Nothing
    If finishedOwner Is Nothing Then Exit Sub
    ' An error from an event handler of the program must not reach the WinHTTP event.
    On Error Resume Next
    finishedOwner.CallFinished Me
End Sub
