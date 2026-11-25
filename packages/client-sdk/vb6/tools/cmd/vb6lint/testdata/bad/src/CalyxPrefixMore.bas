Attribute VB_Name = "CalyxPrefixMore"
' Calyx SDK for VB6
Option Explicit

Public Enum CalyxKind
    CalyxKindA = 1
    BadMember = 2
End Enum
Public Const BadConst As Long = 1
Public BadVar As Long
Public Declare Function BadDeclare Lib "kernel32" () As Long
Public CalyxOk As Long, BadSecond As Long
Public Const CalyxA = 1, BadSecondConst = 2
Public CalyxArr(1, 2) As Long, BadAfterArray As Long
Public Const CalyxText = "a, b", CalyxB = 2 ' c, BadInComment
#If Win32 Then
    Public BadIndented As Long
#End If
