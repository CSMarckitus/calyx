Attribute VB_Name = "TestMain"
' The test program of the Calyx SDK for VB6.
'
' Usage: CalyxTests.exe <base URL of the fake backend> <folder of packages\client-sdk\testdata>
' Example, from packages\client-sdk\vb6:
'   tests\CalyxTests.exe http://127.0.0.1:8080 ..\testdata
'
' The second mode checks one real connection, for example the TLS of
' Windows 7 SP1. It needs no fake backend and calls only GetInstanceSync:
'   tests\CalyxTests.exe tls-check <BaseUrl> <ApiKey> <InstanceId>
'
' It writes results.txt next to CalyxTests.exe: one line for each case and a total. A missing
' total line means that the program stopped before the end. The file never has the API key.
'
' Events in a program with no form: WinHTTP sends its events to this thread as window
' messages. Sub Main has no message loop, so the wait loop calls DoEvents to deliver them.
Option Explicit

Private Declare Sub Sleep Lib "kernel32" (ByVal dwMilliseconds As Long)

Private Const SECONDS_IN_A_DAY As Single = 86400
Private Const TLS_CHECK_MODE As String = "tls-check"

Private mCaseName As String
Private mCaseOpen As Boolean
Private mCaseProblems As String
Private mPassed As Long
Private mFailed As Long

Public Sub Main()
    Dim arguments As Collection
    On Error GoTo Crashed
    StartResults
    Set arguments = SplitArguments(Command$)
    If IsTlsCheck(arguments) Then
        RunTlsCheck arguments
    ElseIf arguments.Count < 2 Then
        BeginCase "command-line arguments"
        Check False, "Usage: CalyxTests.exe <base URL of the fake backend> <testdata folder>"
        EndCase
    Else
        RunAllCases arguments.Item(1), arguments.Item(2)
    End If
    WriteLine "TOTAL: " & (mPassed + mFailed) & " cases, " & mPassed & " passed, " & mFailed & " failed"
    Exit Sub
Crashed:
    ' Best effort: the line tells the author that the program stopped here.
    On Error Resume Next
    WriteLine "STOPPED: VB6 error " & Err.Number & ": " & Err.Description
End Sub

' VB6 does not stop an And after a False part, so Item(1) is read only when it exists.
Private Function IsTlsCheck(ByVal CommandParts As Collection) As Boolean
    If CommandParts.Count = 0 Then Exit Function
    IsTlsCheck = (LCase$(CommandParts.Item(1)) = TLS_CHECK_MODE)
End Function

' One GetInstanceSync call with the given settings. It writes the instance or the CalyxError.
' It never writes the API key: the SDK keeps the key out of Detail and of the VB6 error text.
Private Sub RunTlsCheck(ByVal CommandParts As Collection)
    Dim checkClient As CalyxClient
    Dim checkInstance As Object
    Dim tlsFailure As CalyxError
    Dim errNumber As Long
    Dim errDescription As String
    BeginCase "tls-check: GetInstanceSync"
    If CommandParts.Count < 4 Then
        Check False, "Usage: CalyxTests.exe tls-check <BaseUrl> <ApiKey> <InstanceId>"
        EndCase
        Exit Sub
    End If
    WriteLine "BaseUrl: " & CommandParts.Item(2)
    WriteLine "InstanceId: " & CommandParts.Item(4)
    Set checkClient = New CalyxClient
    checkClient.BaseUrl = CommandParts.Item(2)
    checkClient.ApiKey = CommandParts.Item(3)
    On Error Resume Next
    Set checkInstance = checkClient.GetInstanceSync(CommandParts.Item(4))
    errNumber = Err.Number
    errDescription = Err.Description
    On Error GoTo 0
    If errNumber = 0 Then
        WriteLine "Result: id " & FieldText(checkInstance, "id") & ", status " & _
            FieldText(checkInstance, "status") & ", provider " & FieldText(checkInstance, "provider") & _
            ", display_name " & FieldText(checkInstance, "display_name")
        EndCase
        Exit Sub
    End If
    Set tlsFailure = checkClient.LastError
    If tlsFailure Is Nothing Then
        Check False, "VB6 error " & errNumber & ": " & errDescription
    Else
        WriteLine "CalyxError: Source " & tlsFailure.Source & ", Code " & tlsFailure.Code & _
            ", Status " & tlsFailure.Status & ", WinHttpError " & tlsFailure.WinHttpError & _
            ", RetryText " & tlsFailure.RetryText & ", RequestId " & tlsFailure.RequestId
        WriteLine "Detail: " & tlsFailure.Detail
        Check False, "GetInstanceSync failed with " & tlsFailure.Code
    End If
    EndCase
End Sub

' A field of the answer as text. Null or absent gives "(none)". An object field is not shown.
Private Function FieldText(ByVal Answer As Object, ByVal Name As String) As String
    Dim fieldValue As Variant
    If Answer Is Nothing Then
        FieldText = "(no answer)"
        Exit Function
    End If
    If TypeName(Answer) <> "Dictionary" Then
        FieldText = "(not an object)"
        Exit Function
    End If
    If IsObject(CalyxField(Answer, Name, "(none)")) Then
        FieldText = "(an object)"
        Exit Function
    End If
    fieldValue = CalyxField(Answer, Name, "(none)")
    FieldText = CStr(fieldValue)
End Function

' Splits the command line at spaces. Double quotes keep a path with spaces in one argument.
Private Function SplitArguments(ByVal CommandLine As String) As Collection
    Dim parts As Collection
    Dim current As String
    Dim inQuotes As Boolean
    Dim i As Long
    Dim ch As String
    Set parts = New Collection
    For i = 1 To Len(CommandLine)
        ch = Mid$(CommandLine, i, 1)
        If ch = """" Then
            inQuotes = Not inQuotes
        ElseIf ch = " " And Not inQuotes Then
            If Len(current) > 0 Then parts.Add current
            current = ""
        Else
            current = current & ch
        End If
    Next i
    If Len(current) > 0 Then parts.Add current
    Set SplitArguments = parts
End Function

' Waits for the first event of the listener. Returns False when no event came in time.
Public Function WaitForEvent(ByVal Listener As TestListener, ByVal LimitSeconds As Single) As Boolean
    Dim startTime As Single
    startTime = Timer
    Do While Not Listener.Done
        If SecondsSince(startTime) >= LimitSeconds Then Exit Function
        DoEvents
        Sleep 10
    Loop
    WaitForEvent = True
End Function

' Delivers events for a fixed time, so a late event can arrive.
Public Sub PumpEvents(ByVal Seconds As Single)
    Dim startTime As Single
    startTime = Timer
    Do While SecondsSince(startTime) < Seconds
        DoEvents
        Sleep 10
    Loop
End Sub

' Timer counts the seconds since midnight, so it goes back to 0 at midnight.
Public Function SecondsSince(ByVal FromTime As Single) As Single
    Dim elapsed As Single
    elapsed = Timer - FromTime
    If elapsed < 0 Then elapsed = elapsed + SECONDS_IN_A_DAY
    SecondsSince = elapsed
End Function

Public Sub BeginCase(ByVal CaseName As String)
    If mCaseOpen Then EndCase
    mCaseName = CaseName
    mCaseProblems = ""
    mCaseOpen = True
End Sub

Public Function CaseIsOpen() As Boolean
    CaseIsOpen = mCaseOpen
End Function

' Records a problem of the open case when Condition is False.
Public Sub Check(ByVal Condition As Boolean, ByVal Message As String)
    If Condition Then Exit Sub
    If Not mCaseOpen Then BeginCase "(a check outside a case)"
    If Len(mCaseProblems) > 0 Then mCaseProblems = mCaseProblems & "; "
    mCaseProblems = mCaseProblems & Message
End Sub

' Do not use this for a value that can hold the API key: a failure writes both values.
Public Sub CheckEqual(ByVal Actual As Variant, ByVal Expected As Variant, ByVal What As String)
    Dim same As Boolean
    If IsObject(Actual) Or IsObject(Expected) Then
        Check False, What & ": an object, not a value"
        Exit Sub
    End If
    If IsNull(Actual) Or IsNull(Expected) Then
        same = IsNull(Actual) And IsNull(Expected)
    Else
        same = SameValue(Actual, Expected)
    End If
    If Not same Then Check False, What & ": expected " & Shown(Expected) & ", got " & Shown(Actual)
End Sub

' A comparison of two values of different types can raise an error. That counts as different.
Private Function SameValue(ByVal Actual As Variant, ByVal Expected As Variant) As Boolean
    On Error GoTo Different
    SameValue = (Actual = Expected)
    Exit Function
Different:
    SameValue = False
End Function

Private Function Shown(ByVal Value As Variant) As String
    If IsNull(Value) Then
        Shown = "Null"
    ElseIf IsEmpty(Value) Then
        Shown = "Empty"
    ElseIf VarType(Value) = vbString Then
        Shown = """" & Value & """"
    Else
        Shown = CStr(Value) & " (" & TypeName(Value) & ")"
    End If
End Function

Public Sub EndCase()
    If Not mCaseOpen Then Exit Sub
    mCaseOpen = False
    If Len(mCaseProblems) = 0 Then
        mPassed = mPassed + 1
        WriteLine "PASS  " & mCaseName
    Else
        mFailed = mFailed + 1
        WriteLine "FAIL  " & mCaseName & ": " & mCaseProblems
    End If
End Sub

Private Function ResultPath() As String
    Dim folder As String
    folder = App.Path
    If Right$(folder, 1) <> "\" Then folder = folder & "\"
    ResultPath = folder & "results.txt"
End Function

Private Sub StartResults()
    Dim fileNumber As Integer
    fileNumber = FreeFile
    Open ResultPath() For Output As #fileNumber
    Print #fileNumber, "Calyx SDK for VB6 " & CALYX_SDK_VERSION & ", tests of " & Format$(Now, "yyyy-mm-dd hh:nn:ss")
    Close #fileNumber
End Sub

' Opens and closes the file for each line, so a crash keeps the earlier lines.
Private Sub WriteLine(ByVal Text As String)
    Dim fileNumber As Integer
    fileNumber = FreeFile
    Open ResultPath() For Append As #fileNumber
    Print #fileNumber, Text
    Close #fileNumber
End Sub
