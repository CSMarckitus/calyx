Attribute VB_Name = "CalyxPublicForms"
' Calyx SDK for VB6
Option Explicit

Function BadFunction() As Long
End Function
Sub BadSub()
End Sub
Property Get BadProperty() As Long
End Property
Property Let BadPropertyLet(ByVal V As Long)
End Property
Enum BadEnum
    BadEnumMember = 1
    CalyxEnumOk = 2
End Enum
Type BadType
    Size As Long
End Type
Declare Function BadDeclareBare Lib "kernel32" () As Long
Declare Sub BadDeclareSub Lib "kernel32" ()
Global BadGlobal As Long
Global Const BadGlobalConst = 1
Static Function BadStaticFunction() As Long
End Function
Static Sub BadStaticSub()
End Sub
Private Function PrivateOk() As Long
End Function
Private Enum PrivateEnum
    PrivateMember = 1
End Enum
Private Declare Function PrivateDeclare Lib "kernel32" () As Long
Private Static Function PrivateStatic() As Long
End Function
Dim DimOk As Long
Const ConstOk = 1
Friend Function FriendOk() As Long
End Function
Function CalyxFunctionOk() As Long
End Function
Global CalyxGlobalOk As Long
