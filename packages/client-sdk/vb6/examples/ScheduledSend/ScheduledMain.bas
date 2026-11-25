Attribute VB_Name = "ScheduledMain"
Option Explicit

' A program with no window. The Windows Task Scheduler starts it, for example once a day.
' It checks the instance with GetInstanceSync, then sends each reminder with SendMessageSync.
'
' A Sync method blocks until the answer, so it is for a program with no form. In a form, the
' window freezes for up to the time limit (6 minutes for a send). A failure raises a VB6 error,
' and LastError holds the CalyxError. A Sync method raises no event.
'
' Before you start, copy calyx-example.ini.sample to calyx-example.ini in this folder. Fill in
' BaseUrl, ApiKey, InstanceId and the section [Reminders].
' Caution: send only to your own test number. A message to a real number can get the number
' of the instance banned.
'
' The program writes each result to scheduled-send.log in this folder.

Private Declare Function GetPrivateProfileString Lib "kernel32" Alias "GetPrivateProfileStringA" ( _
    ByVal lpAppName As String, ByVal lpKeyName As String, ByVal lpDefault As String, _
    ByVal lpReturnedString As String, ByVal nSize As Long, ByVal lpFileName As String) As Long

Private mCalyx As CalyxClient
Private mInstanceId As String

Public Sub Main()
    Dim instanceInfo As Object
    Dim instanceStatus As String
    Dim reminderCount As Long
    Dim reminderIndex As Long

    Set mCalyx = New CalyxClient
    mCalyx.BaseUrl = ReadSetting("Calyx", "BaseUrl")
    mCalyx.ApiKey = ReadSetting("Calyx", "ApiKey")
    mInstanceId = ReadSetting("Calyx", "InstanceId")
    WriteLog "Start."

    On Error GoTo CheckFailed
    Set instanceInfo = mCalyx.GetInstanceSync(mInstanceId)
    On Error GoTo 0
    ' The status field can be Null. CalyxField returns the default for a Null or absent field.
    instanceStatus = CalyxField(instanceInfo, "status", "")
    If instanceStatus <> "CONNECTED" Then
        WriteLog "The instance is not connected (status " & instanceStatus & "). No reminder sent."
        Exit Sub
    End If

    reminderCount = Val(ReadSetting("Reminders", "Count"))
    For reminderIndex = 1 To reminderCount
        SendReminder reminderIndex
    Next reminderIndex
    WriteLog "End."
    Exit Sub
CheckFailed:
    LogFailure "GetInstanceSync", Err.Description
End Sub

' The core applies its pacing, so each send can wait some minutes before it returns.
Private Sub SendReminder(ByVal ReminderNumber As Long)
    Dim reminderKey As String
    Dim sent As CalyxSendResult
    ' One idempotency key for each reminder of the day. If the Task Scheduler starts the program
    ' again on the same day, the backend sends nothing new: it answers with Replayed = True, or
    ' with send_in_progress while the first send waits. The backend keeps a key for 24 hours.
    reminderKey = "reminder-" & Format$(Date, "yyyymmdd") & "-" & ReminderNumber
    On Error GoTo SendFailed
    Set sent = mCalyx.SendMessageSync(mInstanceId, _
        ReadSetting("Reminders", "Recipient" & ReminderNumber), _
        ReadSetting("Reminders", "Text" & ReminderNumber), reminderKey)
    WriteLog "Reminder " & ReminderNumber & ": sent, message " & sent.MessageId & _
        ", replayed " & sent.Replayed & ", key " & reminderKey
    Exit Sub
SendFailed:
    LogFailure "Reminder " & ReminderNumber, Err.Description
End Sub

' The log gets the code and the key of the failure from LastError, and never the API key.
Private Sub LogFailure(ByVal What As String, ByVal ErrorText As String)
    Dim lastFailure As CalyxError
    Set lastFailure = mCalyx.LastError
    If lastFailure Is Nothing Then
        WriteLog What & ": failed: " & ErrorText
    Else
        WriteLog What & ": failed, code " & lastFailure.Code & ", advice " & _
            lastFailure.RetryText & ", key " & lastFailure.IdempotencyKey & ". " & lastFailure.Detail
    End If
End Sub

' A log failure must not stop an unattended run: an error dialog in a Task Scheduler session
' hangs the task.
Private Sub WriteLog(ByVal LogText As String)
    On Error Resume Next
    Dim fileNumber As Integer
    fileNumber = FreeFile
    Open AppFile("scheduled-send.log") For Append As #fileNumber
    Print #fileNumber, Format$(Now, "yyyy-mm-dd hh:nn:ss") & "  " & LogText
    Close #fileNumber
End Sub

Private Function AppFile(ByVal FileName As String) As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    AppFile = folder & FileName
End Function

' GetPrivateProfileStringA reads the file in the ANSI code page of Windows.
Private Function ReadSetting(ByVal SectionName As String, ByVal KeyName As String) As String
    Dim buffer As String
    Dim copied As Long
    buffer = String$(1024, vbNullChar)
    copied = GetPrivateProfileString(SectionName, KeyName, "", buffer, Len(buffer), _
        AppFile("calyx-example.ini"))
    ReadSetting = Left$(buffer, copied)
End Function
