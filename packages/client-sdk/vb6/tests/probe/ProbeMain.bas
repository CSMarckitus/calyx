Attribute VB_Name = "ProbeMain"
' The Windows facts probe of the Calyx SDK for VB6.
' The steps are in tests/windows-facts.md. The probe calls only 127.0.0.1 and the HTTPS URL
' of the command line. It sends no message.
'
' Usage:
'   Probe.exe local              Windows 10: fakebackend.exe on 127.0.0.1:8080 and the
'                                silent listener on 127.0.0.1:8081 must run.
'   Probe.exe tls <https URL>    Windows 10 and Windows 7 SP1: one GET to the URL, two times.
'
' Every result line goes to probe-results.txt next to Probe.exe.
Option Explicit

' Set this to 0 when CalyxJson.bas does not compile (see windows-facts.md).
#Const PROBE_JSON = 1

Private Declare Sub Sleep Lib "kernel32" (ByVal dwMilliseconds As Long)

Private Const HELD_URL As String = "http://127.0.0.1:8080/api/v1/instances/00000000-0000-4000-8000-000000000005"
Private Const SILENT_URL As String = "http://127.0.0.1:8081/"
Private Const CLOSED_URL As String = "http://127.0.0.1:1/"
Private Const OPTION_SECURE_PROTOCOLS As Long = 9
Private Const TLS12 As Long = &H800&
Private Const WAIT_LIMIT_SECONDS As Single = 40

Public Sub Main()
    Dim args() As String
    args = Split(Trim$(Command$), " ")
    Report "== Probe " & Format$(Now, "yyyy-mm-dd hh:nn:ss") & ", arguments: " & Command$
    If UBound(args) < 0 Then
        Usage
    ElseIf LCase$(args(0)) = "local" Then
        RunLocal
    ElseIf LCase$(args(0)) = "tls" And UBound(args) >= 1 Then
        RunTls args(1)
    Else
        Usage
    End If
    Report "== End"
    MsgBox "The probe ended. The results are in " & ResultPath(), vbInformation, "Probe"
End Sub

Private Sub Usage()
    Report "Usage: Probe.exe local"
    Report "       Probe.exe tls <https URL>"
End Sub

Private Sub RunLocal()
    ProbeOptionNine
    ProbeSync "closed port", CLOSED_URL, 0, 0
    ProbeAsync "closed port", CLOSED_URL, 0
    ProbeSync "default time limits, silent listener", SILENT_URL, 0, 0
    ProbeSync "receive time limit 5 s, fakebackend scenario 5 (control)", HELD_URL, 5, 0
    ProbeAsync "receive time limit 5 s, fakebackend scenario 5", HELD_URL, 5
#If PROBE_JSON Then
    ProbeJson
#Else
    Report "-- VBA-JSON: not in this build (PROBE_JSON = 0)"
#End If
End Sub

Private Sub RunTls(ByVal Url As String)
    If LCase$(Left$(Url, 8)) <> "https://" Then
        Report "The URL must start with https://"
        Exit Sub
    End If
    ProbeOptionNine
    ProbeSync "default protocols", Url, 0, 0
    ProbeSync "Option(9) = TLS 1.2", Url, 0, TLS12
End Sub

' Reads and sets Option(9) on a request that is opened but never sent.
Private Sub ProbeOptionNine()
    Dim req As WinHttp.WinHttpRequest
    Dim stage As String
    Report "-- Option(9) on a request that is not sent"
    On Error GoTo Failed
    stage = "New WinHttpRequest"
    Set req = New WinHttp.WinHttpRequest
    stage = "Open"
    req.Open "GET", "https://127.0.0.1/", False
    stage = "read Option(9)"
    Report "   value before the set: &H" & Hex$(req.Option(OPTION_SECURE_PROTOCOLS))
    stage = "set Option(9) to &H800"
    req.Option(OPTION_SECURE_PROTOCOLS) = TLS12
    Report "   the set gave no error"
    stage = "read Option(9) after the set"
    Report "   value after the set: &H" & Hex$(req.Option(OPTION_SECURE_PROTOCOLS))
    Exit Sub
Failed:
    Report "   error in " & stage & ": " & ProbeErrorText(Err.Number, Err.Description)
End Sub

' One blocking GET. ReceiveSeconds 0 keeps the WinHTTP default time limits. Tls 0 keeps the
' default protocols.
Private Sub ProbeSync(ByVal Label As String, ByVal Url As String, ByVal ReceiveSeconds As Long, ByVal Tls As Long)
    Dim req As WinHttp.WinHttpRequest
    Dim stage As String
    Dim started As Single
    Dim errNumber As Long
    Dim errDescription As String
    Dim errSource As String
    Report "-- sync: " & Label & " (" & Url & ")"
    started = Timer
    On Error GoTo Failed
    stage = "New WinHttpRequest"
    Set req = New WinHttp.WinHttpRequest
    ' Time-outs go before Open, as in the SDK (WinHTTP documentation).
    If ReceiveSeconds > 0 Then
        stage = "SetTimeouts"
        req.SetTimeouts 30000, 30000, ReceiveSeconds * 1000, ReceiveSeconds * 1000
    End If
    stage = "Open"
    req.Open "GET", Url, False
    If Tls <> 0 Then
        stage = "set Option(9)"
        req.Option(OPTION_SECURE_PROTOCOLS) = Tls
    End If
    If IsLocal(Url) Then
        stage = "SetRequestHeader"
        req.SetRequestHeader "Authorization", "Bearer test-key"
    End If
    stage = "Send"
    started = Timer
    req.Send
    Report "   status " & req.Status & " after " & Format$(SecondsSince(started), "0.0") & " s"
    Exit Sub
Failed:
    errNumber = Err.Number
    errDescription = Err.Description
    errSource = Err.Source
    Report "   error in " & stage & " after " & Format$(SecondsSince(started), "0.0") & " s: " & _
        ProbeErrorText(errNumber, errDescription) & ", Err.Source """ & errSource & """"
End Sub

' One asynchronous GET. ProbeEvents receives the events; this loop waits with DoEvents.
Private Sub ProbeAsync(ByVal Label As String, ByVal Url As String, ByVal ReceiveSeconds As Long)
    Dim listener As ProbeEvents
    Dim started As Single
    Dim failure As String
    Report "-- async: " & Label & " (" & Url & ")"
    Set listener = New ProbeEvents
    started = Timer
    failure = listener.Start(Url, ReceiveSeconds)
    If Len(failure) > 0 Then
        Report "   the call did not start: " & failure
        Exit Sub
    End If
    Report "   Send returned after " & Format$(SecondsSince(started), "0.0") & " s"
    Do While Not listener.Done And SecondsSince(started) < WAIT_LIMIT_SECONDS
        DoEvents
        Sleep 50
    Loop
    If listener.Done Then
        Report "   events after " & Format$(SecondsSince(started), "0.0") & " s: " & listener.Outcome
    Else
        Report "   no OnError and no OnResponseFinished within " & WAIT_LIMIT_SECONDS & " s. Events: " & listener.Outcome
    End If
    listener.Abort
End Sub

#If PROBE_JSON Then
' Parses and writes JSON with CalyxJson.bas. The text has an escaped e with an acute accent.
Private Sub ProbeJson()
    Dim parsed As Object
    Report "-- VBA-JSON (CalyxJson.bas)"
    On Error GoTo Failed
    Set parsed = CalyxParseJson("{""a"":1,""b"":[true,null],""c"":""x\u00e9""}")
    Report "   TypeName " & TypeName(parsed) & ", a = " & parsed.Item("a") & _
        ", b(1) = " & parsed.Item("b").Item(1) & _
        ", IsNull(b(2)) = " & IsNull(parsed.Item("b").Item(2)) & _
        ", AscW of the last character of c = " & AscW(Right$(parsed.Item("c"), 1))
    Report "   CalyxConvertToJson: " & CalyxConvertToJson(parsed)
    Exit Sub
Failed:
    Report "   error: " & ProbeErrorText(Err.Number, Err.Description)
End Sub
#End If

' Shows a number in three forms, so the facts file can say which form WinHTTP uses.
Public Function ProbeErrorText(ByVal Number As Long, ByVal Description As String) As String
    ' &HFFFF& is a Long. &HFFFF without the & is the Integer -1, which keeps every bit.
    ProbeErrorText = "number &H" & Hex$(Number) & " (" & Number & "), low 16 bits " & _
        (Number And &HFFFF&) & ", description """ & Trim$(Replace(Description, vbCrLf, " ")) & """"
End Function

Private Function IsLocal(ByVal Url As String) As Boolean
    IsLocal = (InStr(1, Url, "://127.0.0.1:", vbTextCompare) > 0)
End Function

' Timer starts again from 0 at midnight.
Private Function SecondsSince(ByVal Started As Single) As Single
    Dim s As Single
    s = Timer - Started
    If s < 0 Then s = s + 86400
    SecondsSince = s
End Function

Private Function ResultPath() As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    ResultPath = folder & "probe-results.txt"
End Function

' Opens and closes the file for each line, so a crash keeps the earlier lines.
Private Sub Report(ByVal Message As String)
    Dim f As Integer
    f = FreeFile
    Open ResultPath() For Append As #f
    Print #f, Message
    Close #f
End Sub
