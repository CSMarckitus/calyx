VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxBulkMessage"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' One message of CreateBulkJobCustom. The program creates it and sets the properties.
'
' DelayMs stays Empty when the program does not set it. The SDK then leaves the field delay_ms
' out, and the pacing of the core decides the delay. CreateBulkJobCustom checks the values.

Private mRecipient As String
Private mText As String
Private mDelayMs As Variant

Public Property Get Recipient() As String
    Recipient = mRecipient
End Property

Public Property Let Recipient(ByVal Value As String)
    mRecipient = Value
End Property

Public Property Get Text() As String
    Text = mText
End Property

Public Property Let Text(ByVal Value As String)
    mText = Value
End Property

' A whole number of milliseconds, from 0 to 2147483647, or Empty.
Public Property Get DelayMs() As Variant
    DelayMs = mDelayMs
End Property

Public Property Let DelayMs(ByVal Value As Variant)
    mDelayMs = Value
End Property
