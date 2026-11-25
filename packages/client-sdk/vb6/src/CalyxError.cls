VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "CalyxError"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = True
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
' Calyx SDK for VB6
Option Explicit

' The failure of one call. The SDK fills it. The program only reads it.
' It keeps the request of the call, so that Resend can send it again.

Private mSource As String
Private mCode As String
Private mStatus As Long
Private mDetail As String
Private mRequestId As String
Private mIdempotencyKey As String
Private mOperation As String
Private mTag As Variant
Private mRetryAfter As Long
Private mWinHttpError As Long
Private mErrors As Collection
Private mRequest As CalyxRequest

Private Sub Class_Initialize()
    Set mErrors = New Collection
End Sub

' "backend" (the backend answered with an error) or "client" (the SDK found the failure).
Public Property Get Source() As String
    Source = mSource
End Property

Friend Property Let Source(ByVal Value As String)
    mSource = Value
End Property

Public Property Get Code() As String
    Code = mCode
End Property

Friend Property Let Code(ByVal Value As String)
    mCode = Value
End Property

Public Property Get Status() As Long
    Status = mStatus
End Property

Friend Property Let Status(ByVal Value As Long)
    mStatus = Value
End Property

Public Property Get Detail() As String
    Detail = mDetail
End Property

Friend Property Let Detail(ByVal Value As String)
    mDetail = Value
End Property

Public Property Get RequestId() As String
    RequestId = mRequestId
End Property

Friend Property Let RequestId(ByVal Value As String)
    mRequestId = Value
End Property

Public Property Get IdempotencyKey() As String
    IdempotencyKey = mIdempotencyKey
End Property

Public Property Get Operation() As String
    Operation = mOperation
End Property

Public Property Get Tag() As Variant
    If IsObject(mTag) Then
        Set Tag = mTag
    Else
        Tag = mTag
    End If
End Property

' The advice of the SDK for this failure.
Public Property Get Retry() As CalyxRetry
    Retry = CalyxAdviceFor(LCase$(mSource), mCode)
End Property

' The wire value of Retry, for example "retry_later".
Public Property Get RetryText() As String
    RetryText = CalyxRetryText(Retry)
End Property

' Seconds from the Retry-After header. 0 when the backend sends none.
Public Property Get RetryAfter() As Long
    RetryAfter = mRetryAfter
End Property

Friend Property Let RetryAfter(ByVal Value As Long)
    mRetryAfter = Value
End Property

' A new Collection of CalyxFieldError on each read. It is empty when the answer has no field errors.
' The program can change the copy. The error keeps its own list.
Public Property Get Errors() As Collection
    Dim duplicate As Collection
    Dim field As Variant
    Set duplicate = New Collection
    For Each field In mErrors
        duplicate.Add field
    Next field
    Set Errors = duplicate
End Property

' The WinHTTP error number. 0 when there is none.
Public Property Get WinHttpError() As Long
    WinHttpError = mWinHttpError
End Property

Friend Property Let WinHttpError(ByVal Value As Long)
    mWinHttpError = Value
End Property

' True when the failure has a request and the advice allows Resend.
Public Property Get CanResend() As Boolean
    Dim advice As CalyxRetry
    If mRequest Is Nothing Then Exit Property
    advice = Retry
    CanResend = (advice = calyxRetryNow) Or (advice = calyxRetryLater) Or (advice = calyxRetryUnknown)
End Property

Friend Property Get Request() As CalyxRequest
    Set Request = mRequest
End Property

Friend Sub AddFieldError(ByVal Location As String, ByVal Message As String)
    Dim made As CalyxFieldError
    Set made = New CalyxFieldError
    made.Location = Location
    made.Message = Message
    mErrors.Add made
End Sub

' Keeps the request and copies the values that the program can read.
Friend Sub AttachRequest(ByVal Request As CalyxRequest)
    Set mRequest = Request
    mOperation = Request.Operation
    mIdempotencyKey = Request.IdempotencyKey
    If IsObject(Request.Tag) Then
        Set mTag = Request.Tag
    Else
        mTag = Request.Tag
    End If
End Sub
