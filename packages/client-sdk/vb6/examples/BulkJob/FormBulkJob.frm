VERSION 5.00
Begin VB.Form FormBulkJob
   Caption         =   "Calyx: bulk job"
   ClientHeight    =   5280
   ClientLeft      =   60
   ClientTop       =   345
   ClientWidth     =   6015
   LinkTopic       =   "Form1"
   ScaleHeight     =   5280
   ScaleWidth      =   6015
   StartUpPosition =   3  'Windows Default
   Begin VB.Timer tmrPoll
      Enabled         =   0   'False
      Interval        =   30000
      Left            =   5400
      Top             =   3240
   End
   Begin VB.TextBox txtRecipients
      Height          =   1455
      Left            =   120
      MultiLine       =   -1  'True
      ScrollBars      =   2  'Vertical
      TabIndex        =   1
      Text            =   ""
      Top             =   600
      Width           =   5775
   End
   Begin VB.TextBox txtMessage
      Height          =   315
      Left            =   120
      TabIndex        =   3
      Text            =   "Reminder: your appointment is tomorrow."
      Top             =   2400
      Width           =   5775
   End
   Begin VB.CommandButton cmdStart
      Caption         =   "Start"
      Height          =   375
      Left            =   120
      TabIndex        =   4
      Top             =   2880
      Width           =   1335
   End
   Begin VB.CommandButton cmdCancel
      Caption         =   "Cancel the job"
      Enabled         =   0   'False
      Height          =   375
      Left            =   1560
      TabIndex        =   5
      Top             =   2880
      Width           =   1695
   End
   Begin VB.Label lblRecipients
      Caption         =   "Phone numbers, one on each line. Only customers who asked for this message:"
      Height          =   375
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
      Top             =   2160
      Width           =   5775
   End
   Begin VB.Label lblStatus
      Caption         =   "Ready."
      Height          =   1695
      Left            =   120
      TabIndex        =   6
      Top             =   3480
      Width           =   5775
   End
End
Attribute VB_Name = "FormBulkJob"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = False
Attribute VB_PredeclaredId = True
Attribute VB_Exposed = False
Option Explicit

' A bulk job for a list of customers who asked for the message. Use bulk jobs only for
' transactional or consented messages. The core applies its pacing to the job, so a long list
' takes a long time.
'
' The form creates the job, then asks for its progress every 30 seconds, and offers a cancel.
'
' Before you start, copy calyx-example.ini.sample to calyx-example.ini in this folder, and fill
' in BaseUrl, ApiKey and InstanceId.
' Caution: send only to your own test numbers. A message to a real number can get the number
' of the instance banned.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long

Private WithEvents mCalyx As CalyxClient
Private mInstanceId As String
Private mJobId As String
' True from the GetBulkJob call to its Completed or Failed event.
Private mPollPending As Boolean
' A create that failed with an unknown result. Start sends it again with Resend.
Private mCreateFailure As CalyxError

Private Sub Form_Load()
    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    If mCalyx.BaseUrl = "" Or mCalyx.ApiKey = "" Or mInstanceId = "" Then
        lblStatus.Caption = "Copy calyx-example.ini.sample to " & IniPath() & _
            ", and fill in BaseUrl, ApiKey and InstanceId."
        cmdStart.Enabled = False
    End If
End Sub

Private Sub cmdStart_Click()
    Dim phones As Collection
    Dim failedCreate As CalyxError
    Dim jobKey As String
    On Error GoTo StartFailed
    If mCreateFailure Is Nothing Then
        Set phones = PhoneList(txtRecipients.Text)
        If phones.Count = 0 Then
            lblStatus.Caption = "Type at least one phone number."
            Exit Sub
        End If
        jobKey = mCalyx.CreateBulkJobSameText(mInstanceId, phones, txtMessage.Text)
    Else
        ' The same body with the same idempotency key, so the backend creates the job at most
        ' once. Changes to the list after the failure are not sent.
        Set failedCreate = mCreateFailure
        Set mCreateFailure = Nothing
        jobKey = mCalyx.Resend(failedCreate)
    End If
    cmdStart.Enabled = False
    lblStatus.Caption = "Creating the bulk job. Key: " & jobKey
    Exit Sub
StartFailed:
    ShowCallError Err.Description
End Sub

Private Sub tmrPoll_Timer()
    ' Skip this tick while the last poll waits for its answer.
    If mPollPending Then Exit Sub
    ' Set the flag before the call. Within 25 seconds of the last answer, GetBulkJob raises
    ' Completed with the saved answer before it returns.
    mPollPending = True
    On Error GoTo PollFailed
    mCalyx.GetBulkJob mJobId
    Exit Sub
PollFailed:
    mPollPending = False
    ShowCallError Err.Description
End Sub

Private Sub cmdCancel_Click()
    On Error GoTo CancelFailed
    mCalyx.CancelBulkJob mJobId
    lblStatus.Caption = "Cancelling the job."
    Exit Sub
CancelFailed:
    ShowCallError Err.Description
End Sub

' For the bulk operations, Result is a Scripting.Dictionary with the fields of the job. Do not
' change it: a saved answer is the same object for every caller.
Private Sub mCalyx_Completed(ByVal Tag As Variant, ByVal Operation As String, ByVal Result As Object)
    Select Case Operation
    Case CALYX_OP_CREATE_BULK_JOB
        mJobId = CalyxField(Result, "id", "")
        If Len(mJobId) = 0 Then
            ' Without an ID, the Timer and Cancel cannot ask about the job.
            cmdStart.Enabled = True
            lblStatus.Caption = "Error: the answer has no job ID."
        Else
            cmdCancel.Enabled = True
            tmrPoll.Enabled = True
            ShowJob Result
        End If
    Case CALYX_OP_GET_BULK_JOB
        mPollPending = False
        ShowJob Result
    Case CALYX_OP_CANCEL_BULK_JOB
        ShowJob Result
    End Select
End Sub

Private Sub mCalyx_Failed(ByVal Tag As Variant, ByVal Operation As String, ByVal Failure As CalyxError)
    Select Case Operation
    Case CALYX_OP_GET_BULK_JOB
        ' The Timer asks again at the next tick.
        mPollPending = False
        ShowFailure Failure
    Case CALYX_OP_CREATE_BULK_JOB
        RememberCreateFailure Failure
    Case Else
        ShowFailure Failure
    End Select
End Sub

' Timestamps such as estimated_finish_at are UTC text. A field can be Null, so read every field
' through CalyxField.
Private Sub ShowJob(ByVal Job As Object)
    Dim jobStatus As String
    jobStatus = CalyxField(Job, "status", "")
    lblStatus.Caption = "Job " & mJobId & ": " & jobStatus & ". Sent " & _
        CalyxField(Job, "sent_messages", 0) & ", failed " & CalyxField(Job, "failed_messages", 0) & _
        ", total " & CalyxField(Job, "total_messages", 0) & " (" & _
        CalyxField(Job, "progress_percent", 0) & "%). Estimated finish (UTC): " & _
        CalyxField(Job, "estimated_finish_at", "unknown") & "."
    ' A job in one of these states does not change any more.
    Select Case jobStatus
    Case "COMPLETED", "CANCELLED", "FAILED"
        tmrPoll.Enabled = False
        cmdCancel.Enabled = False
        cmdStart.Enabled = True
    End Select
End Sub

' After a create with an unknown result, a new create with a new key can make a second job.
' So the form keeps the failure, and Start sends the same request again.
Private Sub RememberCreateFailure(ByVal Failure As CalyxError)
    cmdStart.Enabled = True
    If Failure.CanResend Then
        Set mCreateFailure = Failure
        lblStatus.Caption = "The create failed: " & Failure.Code & ". Click Start to send the " & _
            "same request again with the same key. " & Failure.Detail
    Else
        ShowFailure Failure
    End If
End Sub

' A method raises a VB6 error at once for a wrong setting, or when WinHTTP cannot start the
' call. LastError holds the CalyxError.
Private Sub ShowCallError(ByVal ErrorText As String)
    Dim callFailure As CalyxError
    Set callFailure = mCalyx.LastError
    If callFailure Is Nothing Then
        lblStatus.Caption = "Error: " & ErrorText
    ElseIf callFailure.Operation = CALYX_OP_CREATE_BULK_JOB Then
        RememberCreateFailure callFailure
    Else
        ShowFailure callFailure
    End If
End Sub

Private Sub ShowFailure(ByVal Failure As CalyxError)
    lblStatus.Caption = Failure.Operation & " failed: " & Failure.Code & " (advice: " & _
        Failure.RetryText & "). " & Failure.Detail
End Sub

' Each item must be a String. A phone number in a Double loses digits.
Private Function PhoneList(ByVal AllLines As String) As Collection
    Dim phones As Collection
    Dim parts() As String
    Dim phone As String
    Dim lineIndex As Long
    Set phones = New Collection
    parts = Split(AllLines, vbCrLf)
    For lineIndex = LBound(parts) To UBound(parts)
        phone = Trim$(parts(lineIndex))
        If Len(phone) > 0 Then phones.Add phone
    Next lineIndex
    Set PhoneList = phones
End Function

Private Sub Form_Unload(Cancel As Integer)
    tmrPoll.Enabled = False
    ' Stop the wait for every pending call. A job that the backend created goes on: cancel it
    ' first if you do not want it.
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
