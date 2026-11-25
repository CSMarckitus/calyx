VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "TestListener"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Receives the events of one CalyxClient for the tests. A .bas module cannot use WithEvents.
Option Explicit

Private WithEvents mClient As CalyxClient
Attribute mClient.VB_VarHelpID = -1

Private mDone As Boolean
Private mEventCount As Long
Private mEventName As String
Private mOperation As String
Private mTag As Variant
Private mResult As Object
Private mFailure As CalyxError

Public Sub Attach(ByVal Target As CalyxClient)
    Set mClient = Target
    ClearEvent
End Sub

' Ends the WithEvents link, so the client and the listener can end.
Public Sub Detach()
    Set mClient = Nothing
    ClearEvent
End Sub

' Clears the last event before the next call.
Public Sub ClearEvent()
    mDone = False
    mEventCount = 0
    mEventName = ""
    mOperation = ""
    mTag = Empty
    Set mResult = Nothing
    Set mFailure = Nothing
End Sub

Public Property Get Client() As CalyxClient
    Set Client = mClient
End Property

' True after the first event since ClearEvent.
Public Property Get Done() As Boolean
    Done = mDone
End Property

Public Property Get EventCount() As Long
    EventCount = mEventCount
End Property

' "Completed" or "Failed" for the last event. "" when no event came.
Public Property Get EventName() As String
    EventName = mEventName
End Property

Public Property Get Operation() As String
    Operation = mOperation
End Property

Public Property Get Tag() As Variant
    If IsObject(mTag) Then
        Set Tag = mTag
    Else
        Tag = mTag
    End If
End Property

Public Property Get Result() As Object
    Set Result = mResult
End Property

Public Property Get Failure() As CalyxError
    Set Failure = mFailure
End Property

' An unhandled error in an event handler ends a compiled EXE, so each handler has On Error.
Private Sub mClient_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    On Error Resume Next
    mEventCount = mEventCount + 1
    mEventName = "Completed"
    mOperation = Operation
    KeepTag Tag
    Set mResult = Result
    Set mFailure = Nothing
    mDone = True
End Sub

Private Sub mClient_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    On Error Resume Next
    mEventCount = mEventCount + 1
    mEventName = "Failed"
    mOperation = Operation
    KeepTag Tag
    Set mResult = Nothing
    Set mFailure = Failure
    mDone = True
End Sub

Private Sub KeepTag(ByVal Value As Variant)
    If IsObject(Value) Then
        Set mTag = Value
    Else
        mTag = Value
    End If
End Sub
