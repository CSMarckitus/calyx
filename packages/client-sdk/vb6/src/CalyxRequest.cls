VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxRequest"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' One request of the SDK, kept so that Resend can send it again without a change.
' It is internal: the program never creates one. It never holds the API key.

Private mOperation As String
Private mMethod As String
Private mPath As String
Private mBodyJson As String
Private mIdempotencyKey As String
Private mTag As Variant
Private mTimeoutSeconds As Long

Public Property Get Operation() As String
    Operation = mOperation
End Property

Friend Property Let Operation(ByVal Value As String)
    mOperation = Value
End Property

Public Property Get Method() As String
    Method = mMethod
End Property

Friend Property Let Method(ByVal Value As String)
    mMethod = Value
End Property

Public Property Get Path() As String
    Path = mPath
End Property

Friend Property Let Path(ByVal Value As String)
    mPath = Value
End Property

Public Property Get BodyJson() As String
    BodyJson = mBodyJson
End Property

Friend Property Let BodyJson(ByVal Value As String)
    mBodyJson = Value
End Property

Public Property Get IdempotencyKey() As String
    IdempotencyKey = mIdempotencyKey
End Property

Friend Property Let IdempotencyKey(ByVal Value As String)
    mIdempotencyKey = Value
End Property

' The tag can be a value or an object, so it has a Let and a Set.
Public Property Get Tag() As Variant
    If IsObject(mTag) Then
        Set Tag = mTag
    Else
        Tag = mTag
    End If
End Property

Friend Property Let Tag(ByVal Value As Variant)
    mTag = Value
End Property

Friend Property Set Tag(ByVal Value As Variant)
    Set mTag = Value
End Property

Public Property Get TimeoutSeconds() As Long
    TimeoutSeconds = mTimeoutSeconds
End Property

Friend Property Let TimeoutSeconds(ByVal Value As Long)
    mTimeoutSeconds = Value
End Property
