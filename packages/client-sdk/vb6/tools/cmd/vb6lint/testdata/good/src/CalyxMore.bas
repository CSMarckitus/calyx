Attribute VB_Name = "CalyxMore"
' Calyx SDK for VB6
Option Explicit

Public Enum CalyxRetry
    calyxRetryUnknown = 0
    calyxRetryNow = 1 ' a comment
End Enum

Public Type CalyxPoint
    X As Long
    Y As Long
End Type

Public Const CALYX_CODE_TIMEOUT As String = "timeout"
Public calyxCounter As Long
Private Const Hidden As Long = 1

Public Function CalyxAdd(ByVal A As Long) As Long
    CalyxAdd = A
End Function

Private Sub Helper()
End Sub

Public Declare Function calyxSleep Lib "kernel32" Alias "Sleep" (ByVal ms As Long) As Long
