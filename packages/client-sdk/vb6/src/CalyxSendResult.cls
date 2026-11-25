VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxSendResult"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' The result of sendMessage. The SDK fills it. The program only reads it.

Private mMessageId As String
Private mReplayed As Boolean
Private mIdempotencyKey As String
Private mRequestId As String

Public Property Get MessageId() As String
    MessageId = mMessageId
End Property

Friend Property Let MessageId(ByVal Value As String)
    mMessageId = Value
End Property

' True when the backend answered from its saved result of an earlier send with the same key.
Public Property Get Replayed() As Boolean
    Replayed = mReplayed
End Property

Friend Property Let Replayed(ByVal Value As Boolean)
    mReplayed = Value
End Property

Public Property Get IdempotencyKey() As String
    IdempotencyKey = mIdempotencyKey
End Property

Friend Property Let IdempotencyKey(ByVal Value As String)
    mIdempotencyKey = Value
End Property

Public Property Get RequestId() As String
    RequestId = mRequestId
End Property

Friend Property Let RequestId(ByVal Value As String)
    mRequestId = Value
End Property
