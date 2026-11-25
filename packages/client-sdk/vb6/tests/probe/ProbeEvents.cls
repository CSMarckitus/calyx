VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "ProbeEvents"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Receives the events of one asynchronous request. A .bas module cannot use WithEvents.
Option Explicit

Private WithEvents mHttp As WinHttp.WinHttpRequest
Attribute mHttp.VB_VarHelpID = -1

Public Done As Boolean
Public Outcome As String

' Starts the request. Returns "" when Send returned, or the error text.
Public Function Start(ByVal Url As String, ByVal ReceiveSeconds As Long) As String
    Dim stage As String
    On Error GoTo Failed
    Done = False
    Outcome = ""
    stage = "New WinHttpRequest"
    Set mHttp = New WinHttp.WinHttpRequest
    ' Time-outs go before Open, as in the SDK (WinHTTP documentation).
    If ReceiveSeconds > 0 Then
        stage = "SetTimeouts"
        mHttp.SetTimeouts 30000, 30000, ReceiveSeconds * 1000, ReceiveSeconds * 1000
    End If
    stage = "Open"
    mHttp.Open "GET", Url, True
    stage = "SetRequestHeader"
    mHttp.SetRequestHeader "Authorization", "Bearer test-key"
    stage = "Send"
    mHttp.Send
    Exit Function
Failed:
    Start = "error in " & stage & ": " & ProbeErrorText(Err.Number, Err.Description)
End Function

Public Sub Abort()
    On Error Resume Next
    If Not mHttp Is Nothing Then mHttp.Abort
    Set mHttp = Nothing
End Sub

Private Sub AddOutcome(ByVal Text As String)
    If Len(Outcome) > 0 Then Outcome = Outcome & " | "
    Outcome = Outcome & Text
End Sub

' An unhandled error in an event handler ends a compiled EXE, so every handler has On Error.
Private Sub mHttp_OnResponseStart(ByVal Status As Long, ByVal ContentType As String)
    On Error Resume Next
    AddOutcome "OnResponseStart: status " & Status
End Sub

Private Sub mHttp_OnResponseFinished()
    On Error Resume Next
    AddOutcome "OnResponseFinished: status " & mHttp.Status
    Done = True
End Sub

Private Sub mHttp_OnError(ByVal ErrorNumber As Long, ByVal ErrorDescription As String)
    On Error Resume Next
    AddOutcome "OnError: " & ProbeErrorText(ErrorNumber, ErrorDescription)
    Done = True
End Sub
