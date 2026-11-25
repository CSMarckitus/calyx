VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxFieldError"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' One invalid field of a request, from the "errors" list of a problem+json answer.

Private mLocation As String
Private mMessage As String

Public Property Get Location() As String
    Location = mLocation
End Property

Friend Property Let Location(ByVal Value As String)
    mLocation = Value
End Property

Public Property Get Message() As String
    Message = mMessage
End Property

Friend Property Let Message(ByVal Value As String)
    mMessage = Value
End Property
