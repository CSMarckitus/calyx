VERSION 5.00
Begin VB.Form FormRetry
   Caption         =   "Calyx: retry with Resend"
   ClientHeight    =   3720
   ClientLeft      =   60
   ClientTop       =   345
   ClientWidth     =   6015
   LinkTopic       =   "Form1"
   ScaleHeight     =   3720
   ScaleWidth      =   6015
   StartUpPosition =   3  'Windows Default
   Begin VB.Timer tmrRetry
      Enabled         =   0   'False
      Interval        =   1000
      Left            =   5400
      Top             =   1800
   End
   Begin VB.TextBox txtRecipient
      Height          =   315
      Left            =   120
      TabIndex        =   1
      Text            =   ""
      Top             =   360
      Width           =   5775
   End
   Begin VB.TextBox txtMessage
      Height          =   315
      Left            =   120
      TabIndex        =   3
      Text            =   "Your order is ready."
      Top             =   1080
      Width           =   5775
   End
   Begin VB.CommandButton cmdSend
      Caption         =   "Send"
      Height          =   375
      Left            =   120
      TabIndex        =   4
      Top             =   1560
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
   Begin VB.Label lblMessage
      Caption         =   "Message:"
      Height          =   255
      Left            =   120
      TabIndex        =   2
      Top             =   840
      Width           =   5775
   End
   Begin VB.Label lblStatus
      Caption         =   "Ready."
      Height          =   1575
      Left            =   120
      TabIndex        =   5
      Top             =   2040
      Width           =   5775
   End
End
Attribute VB_Name = "FormRetry"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = False
Attribute VB_PredeclaredId = True
Attribute VB_Exposed = False
Option Explicit

' A retry that cannot duplicate. Resend sends the same body with the same idempotency
' key, so the core sends the message at most once.
'
' What the form does with each advice (Failure.Retry):
' - retry_now: Resend after 1 second.
' - retry_later, unknown: Resend after RetryAfter seconds. When RetryAfter is 0, after 30 seconds.
' - check_if_sent: stop. The result is unknown, and the key can never send again.
' - do_not_retry: stop. The same request fails again.
' After 6 minutes of retries, the form stops, because the result is unknown.
'
' Before you start, copy calyx-example.ini.sample to calyx-example.ini in this folder, and fill
' in BaseUrl, ApiKey and InstanceId.
' Caution: send only to your own test number. A message to a real number can get the number
' of the instance banned.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long

' A Timer starts every retry. With retry_now too, a short wait keeps a computer with no network
' from sending the request many times each second.
Private Const RETRY_NOW_SECONDS As Long = 1
' The wait when the backend sends no Retry-After (RetryAfter is 0).
Private Const RETRY_LATER_SECONDS As Long = 30
Private Const RETRY_LIMIT_SECONDS As Long = 360

Private WithEvents mCalyx As CalyxClient
Private mInstanceId As String
' The failure that waits for the Timer.
Private mWaiting As CalyxError
' A Timer holds at most about 65 seconds, and RetryAfter can be 240. So the Timer ticks each
' second, and this variable counts the seconds that are left.
Private mSecondsLeft As Long
' True from the first failure of a send to its end. mFirstFailureAt is the time of that failure.
Private mRetrying As Boolean
Private mFirstFailureAt As Date
Private mClosing As Boolean

Private Sub Form_Load()
    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    If mCalyx.BaseUrl = "" Or mCalyx.ApiKey = "" Or mInstanceId = "" Then
        lblStatus.Caption = "Copy calyx-example.ini.sample to " & IniPath() & _
            ", and fill in BaseUrl, ApiKey and InstanceId."
        cmdSend.Enabled = False
    End If
End Sub

Private Sub cmdSend_Click()
    Dim sendKey As String
    mRetrying = False
    cmdSend.Enabled = False
    On Error GoTo SendFailed
    ' With no key, SendMessage makes one and returns it before any event.
    sendKey = mCalyx.SendMessage(mInstanceId, Trim$(txtRecipient.Text), txtMessage.Text)
    lblStatus.Caption = "Sending. Key: " & sendKey
    Exit Sub
SendFailed:
    ShowCallError Err.Description
End Sub

Private Sub mCalyx_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    Dim sent As CalyxSendResult
    If Operation <> CALYX_OP_SEND_MESSAGE Then Exit Sub
    Set sent = Result
    ' After a Resend, Replayed is True when the first request had already sent the message.
    Finish "Sent. Message ID: " & sent.MessageId & ". Replayed: " & sent.Replayed & "."
End Sub

Private Sub mCalyx_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    ' AbortAll in Form_Unload raises Failed with aborted (advice retry_now). Do not resend then.
    If mClosing Then Exit Sub
    HandleFailure Failure
End Sub

Private Sub HandleFailure(ByVal Failure As CalyxError)
    If Not mRetrying Then
        mRetrying = True
        mFirstFailureAt = Now
    End If
    ' CanResend is True for retry_now, retry_later and unknown, when the failure has a request.
    If Failure.CanResend Then
        If Failure.Retry = calyxRetryNow Then
            WaitThenResend Failure, RETRY_NOW_SECONDS
        ElseIf Failure.RetryAfter > 0 Then
            WaitThenResend Failure, Failure.RetryAfter
        Else
            WaitThenResend Failure, RETRY_LATER_SECONDS
        End If
    ElseIf Failure.Retry = calyxRetryCheckIfSent Then
        Finish "The result is unknown (" & Failure.Code & "). The message can still arrive. " & _
            "Check the chat before you send it again. Do not send it again with a new key now."
    Else
        Finish "The send failed (" & Failure.Code & "), and the same request fails again. " & _
            "Correct the cause first. " & Failure.Detail
    End If
End Sub

Private Sub WaitThenResend(ByVal Failure As CalyxError, ByVal WaitSeconds As Long)
    If DateDiff("s", mFirstFailureAt, Now) >= RETRY_LIMIT_SECONDS Then
        Finish "The result is unknown. The message can still arrive. " & _
            "Do not send it again with a new key. Key: " & Failure.IdempotencyKey
        Exit Sub
    End If
    Set mWaiting = Failure
    mSecondsLeft = WaitSeconds
    tmrRetry.Interval = 1000
    tmrRetry.Enabled = True
    ShowWait
End Sub

Private Sub ShowWait()
    lblStatus.Caption = mWaiting.Code & ": the form sends the same request again in " & _
        mSecondsLeft & " s. Key: " & mWaiting.IdempotencyKey
End Sub

Private Sub tmrRetry_Timer()
    Dim waiting As CalyxError
    If mWaiting Is Nothing Then
        tmrRetry.Enabled = False
        Exit Sub
    End If
    mSecondsLeft = mSecondsLeft - 1
    If mSecondsLeft > 0 Then
        ShowWait
        Exit Sub
    End If
    tmrRetry.Enabled = False
    Set waiting = mWaiting
    Set mWaiting = Nothing
    If waiting Is Nothing Then Exit Sub
    On Error GoTo ResendFailed
    ' Resend uses the key, the body and the time limit of the first call.
    mCalyx.Resend waiting
    lblStatus.Caption = "Sent again with the same key: " & waiting.IdempotencyKey
    Exit Sub
ResendFailed:
    ShowCallError Err.Description
End Sub

' A method raises a VB6 error at once for a wrong setting, or when WinHTTP cannot start the
' call. LastError holds the CalyxError, with its advice.
Private Sub ShowCallError(ByVal ErrorText As String)
    If mCalyx.LastError Is Nothing Then
        Finish "Error: " & ErrorText
    Else
        HandleFailure mCalyx.LastError
    End If
End Sub

Private Sub Finish(ByVal Explanation As String)
    lblStatus.Caption = Explanation
    mRetrying = False
    cmdSend.Enabled = True
End Sub

Private Sub Form_Unload(Cancel As Integer)
    mClosing = True
    tmrRetry.Enabled = False
    ' Stop the wait for every pending call. The backend can still send a message that it got.
    mCalyx.AbortAll
    Set mCalyx = Nothing
End Sub

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
