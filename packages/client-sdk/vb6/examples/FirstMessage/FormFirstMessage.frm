VERSION 5.00
Begin VB.Form FormFirstMessage
   Caption         =   "Calyx: first message"
   ClientHeight    =   3720
   ClientLeft      =   60
   ClientTop       =   345
   ClientWidth     =   6015
   LinkTopic       =   "Form1"
   ScaleHeight     =   3720
   ScaleWidth      =   6015
   StartUpPosition =   3  'Windows Default
   Begin VB.TextBox txtRecipient
      Height          =   315
      Left            =   120
      TabIndex        =   1
      Text            =   ""
      Top             =   360
      Width           =   5775
   End
   Begin VB.CommandButton cmdSend
      Caption         =   "Send"
      Height          =   375
      Left            =   120
      TabIndex        =   4
      Top             =   1800
      Width           =   1335
   End
   Begin VB.Label lblRecipient
      Caption         =   "Phone number (country code and number, digits only):"
      Height          =   255
      Left            =   120
      TabIndex        =   0
      Top             =   120
      Width           =   5775
   End
   Begin VB.Label lblTextTitle
      Caption         =   "Message:"
      Height          =   255
      Left            =   120
      TabIndex        =   2
      Top             =   840
      Width           =   5775
   End
   Begin VB.Label lblText
      Height          =   615
      Left            =   120
      TabIndex        =   3
      Top             =   1080
      Width           =   5775
   End
   Begin VB.Label lblStatus
      Caption         =   "Ready."
      Height          =   1335
      Left            =   120
      TabIndex        =   5
      Top             =   2280
      Width           =   5775
   End
End
Attribute VB_Name = "FormFirstMessage"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = False
Attribute VB_PredeclaredId = True
Attribute VB_Exposed = False
Option Explicit

' The first message from a form. The window stays responsive while the message goes out.
'
' Before you start:
' 1. Copy calyx-example.ini.sample to calyx-example.ini in this folder.
' 2. Fill in BaseUrl, ApiKey and InstanceId.
' Caution: send only to your own test number. A message to a real number can get the number
' of the instance banned.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long
Private Declare Function WritePrivateProfileString Lib "kernel32" Alias "WritePrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpString As String, _
    ByVal lpFileName As String) As Long

' WithEvents gives this form the Completed and Failed events of the client.
Private WithEvents mCalyx As CalyxClient
Private mInstanceId As String

Private Sub Form_Load()
    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    lblText.Caption = MessageText()
    If mCalyx.BaseUrl = "" Or mCalyx.ApiKey = "" Or mInstanceId = "" Then
        lblStatus.Caption = "Copy calyx-example.ini.sample to " & IniPath() & _
            ", and fill in BaseUrl, ApiKey and InstanceId."
        cmdSend.Enabled = False
    End If
End Sub

Private Sub cmdSend_Click()
    Dim sendKey As String
    On Error GoTo SendFailed
    ' Make the idempotency key first, and store it with its time before the send. A real
    ' program stores them with its own record (an invoice, an order). If the program closes
    ' during the send, it can send the same request with this key within 24 hours, and the
    ' backend sends the message at most once.
    sendKey = CalyxNewIdempotencyKey()
    WriteSetting "LastSend", "IdempotencyKey", sendKey
    WriteSetting "LastSend", "MadeAt", Format$(Now, "yyyy-mm-dd hh:nn:ss")
    ' SendMessage returns at once. The answer comes later as Completed or Failed.
    mCalyx.SendMessage mInstanceId, Trim$(txtRecipient.Text), MessageText(), sendKey
    cmdSend.Enabled = False
    lblStatus.Caption = "Sending. The window stays responsive. Key: " & sendKey
    Exit Sub
SendFailed:
    ' A wrong setting or argument raises a VB6 error at once. LastError holds the details.
    If mCalyx.LastError Is Nothing Then
        lblStatus.Caption = "Error: " & Err.Description
    Else
        ShowFailure mCalyx.LastError
    End If
End Sub

' For sendMessage, Result is a CalyxSendResult.
Private Sub mCalyx_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    Dim sent As CalyxSendResult
    If Operation <> CALYX_OP_SEND_MESSAGE Then Exit Sub
    Set sent = Result
    ' Replayed is True when the backend already had this key and did not send again.
    lblStatus.Caption = "Sent. Message ID: " & sent.MessageId & ". Replayed: " & sent.Replayed & "."
    cmdSend.Enabled = True
End Sub

Private Sub mCalyx_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    ShowFailure Failure
    cmdSend.Enabled = True
End Sub

' The example RetryWithResend shows what to do for each advice.
Private Sub ShowFailure(ByVal Failure As CalyxError)
    lblStatus.Caption = "Failed: " & Failure.Code & " (advice: " & Failure.RetryText & "). " & _
        Failure.Detail & " Key: " & Failure.IdempotencyKey
End Sub

Private Sub Form_Unload(Cancel As Integer)
    ' Stop the wait for every pending call. Without it, the client stays alive after the form
    ' closes. The backend can still send a message that it got.
    mCalyx.AbortAll
    Set mCalyx = Nothing
End Sub

' The code editor and the controls of VB6 use the ANSI code page, so the accents are written
' with ChrW$. A VB6 String is Unicode, and the SDK sends it with every accent.
Private Function MessageText() As String
    MessageText = "Hello from Calyx: caf" & ChrW$(233) & ", cr" & ChrW$(232) & "me br" & _
        ChrW$(251) & "l" & ChrW$(233) & "e, jalape" & ChrW$(241) & "o."
End Function

Private Function IniPath() As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    IniPath = folder & "calyx-example.ini"
End Function

Private Function ReadSetting(ByVal SectionName As String, ByVal KeyName As String) As String
    Dim buffer As String
    Dim copied As Long
    buffer = String$(1024, vbNullChar)
    copied = GetPrivateProfileString(SectionName, KeyName, "", buffer, Len(buffer), IniPath())
    ReadSetting = Left$(buffer, copied)
End Function

Private Sub WriteSetting(ByVal SectionName As String, ByVal KeyName As String, ByVal NewValue As String)
    WritePrivateProfileString SectionName, KeyName, NewValue, IniPath()
End Sub
